package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const balancePaymentCurrency = "USD"

// createSubscriptionOrderWithBalance atomically deducts the user's account balance,
// creates a completed subscription payment order, fulfills the subscription (stack/renew/legacy),
// and writes the full audit trail inside a single database transaction.
func (s *PaymentService) createSubscriptionOrderWithBalance(
	ctx context.Context,
	req CreateOrderRequest,
	user *User,
	plan *dbent.SubscriptionPlan,
) (*CreateOrderResponse, error) {
	if req.OrderType != payment.OrderTypeSubscription || plan == nil {
		return nil, infraerrors.BadRequest("INVALID_PAYMENT_TYPE", "account balance can only be used to purchase subscriptions")
	}
	if plan.Price <= 0 {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "invalid subscription plan price")
	}
	if s.subscriptionSvc == nil {
		return nil, infraerrors.ServiceUnavailable("SUBSCRIPTION_UNAVAILABLE", "subscription service is unavailable")
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin balance subscription purchase tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	txCtx := dbent.NewTxContext(ctx, tx)
	txClient := tx.Client()

	// 1. Atomically deduct user balance in USD; fails with ErrBalanceNegative when insufficient.
	if _, err := s.userRepo.AdjustBalance(txCtx, req.UserID, -plan.Price); err != nil {
		if errors.Is(err, ErrBalanceNegative) {
			return nil, infraerrors.BadRequest("INSUFFICIENT_BALANCE", "insufficient account balance")
		}
		return nil, fmt.Errorf("deduct user balance for subscription: %w", err)
	}

	// 2. Create completed PaymentOrder with USD balance snapshot.
	outTradeNo, err := s.allocateOutTradeNo(txCtx, tx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	days := psComputeValidityDays(plan.ValidityDays, plan.ValidityUnit)
	tradeNo := "BAL-" + outTradeNo
	snapshot := map[string]any{
		"schema_version": 2,
		"provider_key":   payment.TypeBalance,
		"currency":       balancePaymentCurrency,
	}

	b := tx.PaymentOrder.Create().
		SetUserID(req.UserID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetNillableUserNotes(psNilIfEmpty(user.Notes)).
		SetAmount(plan.Price).
		SetPayAmount(plan.Price).
		SetFeeRate(0).
		SetBonusAmount(0).
		SetRechargeCode("").
		SetOutTradeNo(outTradeNo).
		SetPaymentType(payment.TypeBalance).
		SetPaymentTradeNo(tradeNo).
		SetProviderKey(payment.TypeBalance).
		SetProviderSnapshot(snapshot).
		SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(plan.ID).
		SetSubscriptionGroupID(plan.GroupID).
		SetSubscriptionDays(days).
		SetStatus(OrderStatusCompleted).
		SetPaidAt(now).
		SetCompletedAt(now).
		SetExpiresAt(now).
		SetClientIP(req.ClientIP).
		SetSrcHost(req.SrcHost)
	if req.SrcURL != "" {
		b.SetSrcURL(req.SrcURL)
	}
	order, err := b.Save(txCtx)
	if err != nil {
		return nil, fmt.Errorf("create balance subscription order: %w", err)
	}

	code := fmt.Sprintf("PAY-%d-%d", order.ID, time.Now().UnixNano()%100000)
	order, err = tx.PaymentOrder.UpdateOneID(order.ID).SetRechargeCode(code).Save(txCtx)
	if err != nil {
		return nil, fmt.Errorf("set balance order recharge code: %w", err)
	}

	// 3. Persist template / renewal target metadata and fulfill subscription within the same tx.
	if err := s.persistOrderSubscriptionMetaInTx(txCtx, order.ID, plan.ID, req.TargetSubscriptionID); err != nil {
		return nil, fmt.Errorf("persist balance order subscription meta: %w", err)
	}

	orderNote := paymentSubscriptionOrderNote(order.ID)
	var fulfilledSubID int64
	metaSubID, handled, metaErr := s.fulfillOrderSubscriptionWithMeta(txCtx, order, days, orderNote)
	if metaErr != nil {
		return nil, metaErr
	}
	if handled {
		fulfilledSubID = metaSubID
	} else {
		sub, _, err := s.subscriptionSvc.assignOrExtendSubscription(txCtx, &AssignSubscriptionInput{
			UserID:       order.UserID,
			GroupID:      plan.GroupID,
			ValidityDays: days,
			AssignedBy:   0,
			Notes:        orderNote,
		}, true)
		if err != nil {
			return nil, fmt.Errorf("assign subscription via balance: %w", err)
		}
		if sub != nil {
			fulfilledSubID = sub.ID
		}
		s.recordLegacyFulfilledSubscriptionID(txCtx, order.ID, fulfilledSubID)
	}

	// 4. Write audit logs inside the same transaction.
	assignDetail, _ := json.Marshal(map[string]any{
		"groupID":        plan.GroupID,
		"validityDays":   days,
		"subscriptionID": fulfilledSubID,
		"paymentType":    payment.TypeBalance,
	})
	if _, err := txClient.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_ASSIGNED").
		SetDetail(string(assignDetail)).
		SetOperator("system").
		Save(txCtx); err != nil {
		return nil, fmt.Errorf("record balance subscription assignment audit: %w", err)
	}

	successDetail, _ := json.Marshal(map[string]any{
		"rechargeCode":   order.RechargeCode,
		"creditedAmount": order.Amount,
		"payAmount":      order.PayAmount,
		"paymentType":    payment.TypeBalance,
	})
	if _, err := txClient.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("SUBSCRIPTION_SUCCESS").
		SetDetail(string(successDetail)).
		SetOperator("system").
		Save(txCtx); err != nil {
		return nil, fmt.Errorf("record balance subscription success audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit balance subscription purchase tx: %w", err)
	}
	committed = true

	// 5. Post-commit cache invalidations & notification email.
	if err := s.subscriptionSvc.invalidateSubscriptionInstanceCaches(order.UserID, plan.GroupID, fulfilledSubID); err != nil {
		slog.Warn("invalidate subscription cache after balance purchase failed", "order_id", order.ID, "error", err)
	}
	s.invalidateRefundBalanceCache(ctx, order.UserID)
	s.dispatchPaymentFulfillmentNotification(order, "SUBSCRIPTION_SUCCESS")

	return &CreateOrderResponse{
		OrderID:     order.ID,
		Amount:      order.Amount,
		PayAmount:   order.PayAmount,
		FeeRate:     0,
		BonusAmount: 0,
		Status:      OrderStatusCompleted,
		ResultType:  payment.CreatePaymentResultCompleted,
		PaymentType: payment.TypeBalance,
		OutTradeNo:  order.OutTradeNo,
		Currency:    balancePaymentCurrency,
		ExpiresAt:   order.ExpiresAt,
	}, nil
}

// refundBalancePaidOrder credits the refunded USD amount back to the user's account balance
// when an admin refunds a subscription order originally paid via account balance.
func (s *PaymentService) refundBalancePaidOrder(ctx context.Context, p *RefundPlan) (*payment.RefundResponse, error) {
	if p == nil || p.Order == nil {
		return nil, errors.New("refund plan order is required")
	}
	if p.GatewayAmount > 0 {
		if _, err := s.userRepo.AdjustBalance(ctx, p.Order.UserID, p.GatewayAmount); err != nil {
			return nil, fmt.Errorf("credit refunded balance: %w", err)
		}
		s.invalidateRefundBalanceCache(ctx, p.Order.UserID)
	}
	refundID := fmt.Sprintf("BAL-REF-%d", p.Order.ID)
	s.writeAuditLog(ctx, p.Order.ID, "BALANCE_REFUND_CREDITED", "admin", map[string]any{
		"refundID": refundID,
		"amount":   p.GatewayAmount,
		"currency": balancePaymentCurrency,
	})
	return &payment.RefundResponse{
		RefundID: refundID,
		Status:   payment.ProviderStatusSuccess,
	}, nil
}
