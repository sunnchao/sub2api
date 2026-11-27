package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// PlanTemplateInfo holds template metadata and effective quota limits for a SubscriptionPlan.
type PlanTemplateInfo struct {
	TemplateID        int64
	TemplateName      string
	DailyLimitUSD     *float64
	WeeklyLimitUSD    *float64
	MonthlyLimitUSD   *float64
	HourlyLimitUSD    *float64
	HourlyWindowHours int
}

// GetPlanTemplateMap returns a map of plan_id -> PlanTemplateInfo for plans linked to templates.
func (s *PaymentConfigService) GetPlanTemplateMap(ctx context.Context, plans []*dbent.SubscriptionPlan) map[int64]PlanTemplateInfo {
	out := make(map[int64]PlanTemplateInfo)
	if s == nil || s.entClient == nil || len(plans) == 0 {
		return out
	}
	planIDs := make([]int64, 0, len(plans))
	for _, p := range plans {
		if p != nil && p.ID > 0 {
			planIDs = append(planIDs, p.ID)
		}
	}
	extStore := newEntSubscriptionExtStore(s.entClient)
	tplStore := newEntSubscriptionTemplateStore(s.entClient)
	if extStore == nil || tplStore == nil {
		return out
	}
	planToTpl, err := extStore.GetPlanTemplateIDs(ctx, planIDs)
	if err != nil || len(planToTpl) == 0 {
		return out
	}
	tplIDs := make([]int64, 0, len(planToTpl))
	seen := make(map[int64]struct{}, len(planToTpl))
	for _, tid := range planToTpl {
		if _, ok := seen[tid]; !ok {
			seen[tid] = struct{}{}
			tplIDs = append(tplIDs, tid)
		}
	}
	templates, err := tplStore.GetByIDs(ctx, tplIDs)
	if err != nil {
		return out
	}
	for planID, tid := range planToTpl {
		tpl, ok := templates[tid]
		if !ok || tpl == nil {
			continue
		}
		out[planID] = PlanTemplateInfo{
			TemplateID:        tpl.ID,
			TemplateName:      tpl.Name,
			DailyLimitUSD:     cloneFloat64Ptr(tpl.DailyLimitUSD),
			WeeklyLimitUSD:    cloneFloat64Ptr(tpl.WeeklyLimitUSD),
			MonthlyLimitUSD:   cloneFloat64Ptr(tpl.MonthlyLimitUSD),
			HourlyLimitUSD:    cloneFloat64Ptr(tpl.HourlyLimitUSD),
			HourlyWindowHours: tpl.Limits().NormalizedHourlyWindowHours(),
		}
	}
	return out
}

func (s *PaymentConfigService) resolveActiveTemplateForPlan(ctx context.Context, templateID int64) (*SubscriptionTemplate, error) {
	tplStore := newEntSubscriptionTemplateStore(s.entClient)
	if tplStore == nil {
		return nil, ErrSubscriptionTemplateNotFound
	}
	tpl, err := tplStore.GetByID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if !tpl.IsActive() {
		return nil, ErrSubscriptionTemplateInactive
	}
	return tpl, nil
}

func (s *PaymentConfigService) setPlanTemplateBinding(ctx context.Context, planID, templateID int64) error {
	extStore := newEntSubscriptionExtStore(s.entClient)
	if extStore == nil {
		return nil
	}
	return extStore.SetPlanTemplate(ctx, planID, templateID)
}

func (s *PaymentConfigService) clearPlanTemplateBinding(ctx context.Context, planID int64) error {
	extStore := newEntSubscriptionExtStore(s.entClient)
	if extStore == nil {
		return nil
	}
	return extStore.ClearPlanTemplate(ctx, planID)
}

func (s *PaymentConfigService) getPlanTemplateID(ctx context.Context, planID int64) (*int64, error) {
	extStore := newEntSubscriptionExtStore(s.entClient)
	if extStore == nil {
		return nil, nil
	}
	return extStore.GetPlanTemplateID(ctx, planID)
}

// validateSubOrderGroupAndTarget validates a subscription order's group, optional template,
// and optional renewal target_subscription_id.
func (s *PaymentService) validateSubOrderGroupAndTarget(ctx context.Context, req CreateOrderRequest, plan *dbent.SubscriptionPlan) error {
	group, err := s.groupRepo.GetByID(ctx, plan.GroupID)
	if err != nil || group.Status != payment.EntityStatusActive {
		return infraerrors.NotFound("GROUP_NOT_FOUND", "subscription group is no longer available")
	}

	var planTemplateID *int64
	if s.configService != nil {
		tid, err := s.configService.getPlanTemplateID(ctx, plan.ID)
		if err != nil {
			return err
		}
		planTemplateID = tid
	}

	if planTemplateID != nil && *planTemplateID > 0 {
		tplStore := newEntSubscriptionTemplateStore(s.entClient)
		if tplStore == nil {
			return ErrSubscriptionTemplateNotFound
		}
		tpl, err := tplStore.GetByID(ctx, *planTemplateID)
		if err != nil {
			return err
		}
		if !tpl.IsActive() {
			return ErrSubscriptionTemplateInactive
		}
		if tpl.GroupID != plan.GroupID {
			return ErrSubscriptionTemplateInvalid
		}
		// Templates are allowed to bind either standard or subscription active groups.
	} else if !group.IsSubscriptionType() {
		return infraerrors.BadRequest("GROUP_TYPE_MISMATCH", "group is not a subscription type")
	}

	if req.TargetSubscriptionID != nil && *req.TargetSubscriptionID > 0 {
		if s.subscriptionSvc == nil {
			return ErrInvalidTargetSubscription
		}
		targetSub, err := s.subscriptionSvc.GetByID(ctx, *req.TargetSubscriptionID)
		if err != nil || targetSub == nil {
			return ErrInvalidTargetSubscription
		}
		if targetSub.UserID != req.UserID || targetSub.GroupID != plan.GroupID {
			return ErrInvalidTargetSubscription
		}
		if planTemplateID != nil && *planTemplateID > 0 {
			if targetSub.TemplateID == nil || *targetSub.TemplateID != *planTemplateID {
				return ErrInvalidTargetSubscription
			}
		}
	}

	return nil
}

func (s *PaymentService) persistOrderSubscriptionMetaInTx(ctx context.Context, orderID, planID int64, targetSubID *int64) error {
	extStore := newEntSubscriptionExtStore(s.entClient)
	if extStore == nil {
		return nil
	}
	planTemplateID, err := extStore.GetPlanTemplateID(ctx, planID)
	if err != nil {
		return err
	}
	if planTemplateID == nil && (targetSubID == nil || *targetSubID <= 0) {
		return nil
	}
	return extStore.UpsertOrderSubscriptionMeta(ctx, &PaymentOrderSubscriptionMeta{
		OrderID:              orderID,
		TemplateID:           planTemplateID,
		TargetSubscriptionID: targetSubID,
	})
}

// fulfillOrderSubscriptionWithMeta executes template-stacked or target-renewal fulfillment inside txCtx.
// Returns (fulfilledSubID, handled, err). When handled is false, caller falls back to legacy group fulfillment.
func (s *PaymentService) fulfillOrderSubscriptionWithMeta(
	txCtx context.Context,
	o *dbent.PaymentOrder,
	days int,
	orderNote string,
) (int64, bool, error) {
	extStore := newEntSubscriptionExtStore(s.entClient)
	if extStore == nil {
		return 0, false, nil
	}
	meta, err := extStore.GetOrderSubscriptionMeta(txCtx, o.ID)
	if err != nil {
		return 0, false, fmt.Errorf("load payment order subscription meta: %w", err)
	}
	if meta == nil || (meta.TemplateID == nil && meta.TargetSubscriptionID == nil) {
		return 0, false, nil
	}
	if meta.FulfilledSubscriptionID != nil && *meta.FulfilledSubscriptionID > 0 {
		return *meta.FulfilledSubscriptionID, true, nil
	}

	if meta.TargetSubscriptionID != nil && *meta.TargetSubscriptionID > 0 {
		sub, err := s.subscriptionSvc.RenewSpecificSubscription(txCtx, *meta.TargetSubscriptionID, days, orderNote, true)
		if err != nil {
			return 0, true, fmt.Errorf("renew target subscription %d: %w", *meta.TargetSubscriptionID, err)
		}
		if err := extStore.SetOrderFulfilledSubscriptionID(txCtx, o.ID, sub.ID); err != nil {
			return 0, true, fmt.Errorf("record fulfilled subscription id: %w", err)
		}
		return sub.ID, true, nil
	}

	if meta.TemplateID != nil && *meta.TemplateID > 0 {
		sub, _, err := s.subscriptionSvc.AssignByTemplate(txCtx, &AssignTemplateSubscriptionInput{
			UserID:                 o.UserID,
			TemplateID:             *meta.TemplateID,
			ValidityDays:           days,
			AssignedBy:             0,
			Notes:                  orderNote,
			Mode:                   SubscriptionAssignModeStack,
			DeferCacheInvalidation: true,
		})
		if err != nil {
			return 0, true, fmt.Errorf("assign template subscription: %w", err)
		}
		if err := extStore.SetOrderFulfilledSubscriptionID(txCtx, o.ID, sub.ID); err != nil {
			return 0, true, fmt.Errorf("record fulfilled subscription id: %w", err)
		}
		return sub.ID, true, nil
	}

	return 0, false, nil
}

func (s *PaymentService) recordLegacyFulfilledSubscriptionID(txCtx context.Context, orderID, subID int64) {
	if orderID <= 0 || subID <= 0 {
		return
	}
	extStore := newEntSubscriptionExtStore(s.entClient)
	if extStore == nil {
		return
	}
	_ = extStore.SetOrderFulfilledSubscriptionID(txCtx, orderID, subID)
}

func (s *PaymentService) resolveOrderRefundSubscription(ctx context.Context, o *dbent.PaymentOrder) (*UserSubscription, error) {
	if s.subscriptionSvc == nil {
		return nil, ErrSubscriptionNotFound
	}
	if extStore := newEntSubscriptionExtStore(s.entClient); extStore != nil {
		if meta, err := extStore.GetOrderSubscriptionMeta(ctx, o.ID); err == nil &&
			meta != nil && meta.FulfilledSubscriptionID != nil && *meta.FulfilledSubscriptionID > 0 {
			return s.subscriptionSvc.GetByID(ctx, *meta.FulfilledSubscriptionID)
		}
	}
	if o.SubscriptionGroupID == nil {
		return nil, ErrSubscriptionNotFound
	}
	return s.subscriptionSvc.GetActiveSubscription(ctx, o.UserID, *o.SubscriptionGroupID)
}

// enrichSubscriptionPurchaseNotificationVariables populates subscription_plan (plan name ->
// template name -> group name) and resolves the exact fulfilled subscription's expiry_time
// so subscription purchase emails display the purchased plan title instead of raw order IDs.
func (s *PaymentService) enrichSubscriptionPurchaseNotificationVariables(ctx context.Context, o *dbent.PaymentOrder, variables map[string]string) {
	if o == nil || variables == nil {
		return
	}
	planTitle := ""
	if o.PlanID != nil && *o.PlanID > 0 && s.configService != nil {
		if plan, err := s.configService.GetPlan(ctx, *o.PlanID); err == nil && plan != nil {
			planTitle = firstNonEmpty(strings.TrimSpace(plan.Name), strings.TrimSpace(plan.ProductName))
		}
	}
	if sub, err := s.resolveOrderRefundSubscription(ctx, o); err == nil && sub != nil {
		variables["expiry_time"] = sub.ExpiresAt.Format("2006-01-02 15:04")
		if planTitle == "" {
			planTitle = strings.TrimSpace(sub.TemplateName)
		}
	}
	if planTitle == "" {
		planTitle = strings.TrimSpace(variables["subscription_group"])
	}
	if planTitle == "" {
		planTitle = "Subscription"
	}
	variables["subscription_plan"] = planTitle
}
