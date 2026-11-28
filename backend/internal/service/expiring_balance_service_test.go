package service

import (
	"context"
	"errors"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

type fakeExpiringBalanceRepo struct {
	summaryByUser  map[int64]ExpiringBalanceSummary
	batchCallCount int
	lastBatchIDs   []int64
	lastListUserID int64
	lastListStatus string
	lastListParams pagination.PaginationParams
	listLots       []ExpiringBalanceLot
	listPagination *pagination.PaginationResult
	archiveFn      func(ctx context.Context, limit int) ([]int64, int, error)
	err            error
}

func (f *fakeExpiringBalanceRepo) DeductTotalBalance(context.Context, int64, float64, bool, string, string) (DeductBalanceResult, error) {
	return DeductBalanceResult{}, f.err
}

func (f *fakeExpiringBalanceRepo) RefundPaymentOrderBalance(context.Context, int64, int64, float64) (bool, RefundAllocResult, error) {
	return false, RefundAllocResult{}, f.err
}

func (f *fakeExpiringBalanceRepo) GetActiveExpiringSummary(_ context.Context, userID int64) (ExpiringBalanceSummary, error) {
	if f.err != nil {
		return ExpiringBalanceSummary{}, f.err
	}
	return f.summaryByUser[userID], nil
}

func (f *fakeExpiringBalanceRepo) GetActiveExpiringSummariesBatch(_ context.Context, userIDs []int64) (map[int64]ExpiringBalanceSummary, error) {
	f.batchCallCount++
	f.lastBatchIDs = append([]int64(nil), userIDs...)
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[int64]ExpiringBalanceSummary, len(userIDs))
	for _, id := range userIDs {
		if s, ok := f.summaryByUser[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func (f *fakeExpiringBalanceRepo) ListUserExpiringLots(_ context.Context, userID int64, status string, params pagination.PaginationParams) ([]ExpiringBalanceLot, *pagination.PaginationResult, error) {
	f.lastListUserID = userID
	f.lastListStatus = status
	f.lastListParams = params
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.listLots, f.listPagination, nil
}

func (f *fakeExpiringBalanceRepo) ArchiveExpiredLotsBatch(ctx context.Context, limit int) ([]int64, int, error) {
	if f.archiveFn != nil {
		return f.archiveFn(ctx, limit)
	}
	return nil, 0, f.err
}

func TestExpiringBalanceService_GetUserSummaryAndBatch(t *testing.T) {
	ctx := context.Background()

	var nilSvc *ExpiringBalanceService
	if _, err := nilSvc.GetUserSummary(ctx, 1); err == nil {
		t.Fatalf("expected error on nil service")
	}

	exp := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	repo := &fakeExpiringBalanceRepo{
		summaryByUser: map[int64]ExpiringBalanceSummary{
			5: {ActiveBalance: 2.5, FrozenBalance: 0.5, NearestExpiresAt: &exp, NearestAmount: 1.5},
		},
	}
	svc := NewExpiringBalanceService(repo)

	// Non-positive userID returns zero summary without error.
	zeroSum, err := svc.GetUserSummary(ctx, 0)
	if err != nil || zeroSum.ActiveBalance != 0 {
		t.Fatalf("expected zero summary for userID<=0, got %+v err=%v", zeroSum, err)
	}

	got, err := svc.GetUserSummary(ctx, 5)
	if err != nil || got.ActiveBalance != 2.5 || got.NearestAmount != 1.5 {
		t.Fatalf("unexpected user 5 summary: %+v err=%v", got, err)
	}

	// Empty or all-non-positive batch short-circuits without hitting repository.
	emptyBatch, err := svc.GetUsersSummaryBatch(ctx, []int64{0, -3})
	if err != nil || len(emptyBatch) != 0 || repo.batchCallCount != 0 {
		t.Fatalf("expected short-circuit on non-positive IDs, got batch=%+v calls=%d err=%v", emptyBatch, repo.batchCallCount, err)
	}

	// Duplicate positive IDs are deduplicated before calling repo.
	batch, err := svc.GetUsersSummaryBatch(ctx, []int64{5, -1, 5, 9, 0})
	if err != nil {
		t.Fatalf("batch summaries: %v", err)
	}
	if repo.batchCallCount != 1 || len(repo.lastBatchIDs) != 2 || repo.lastBatchIDs[0] != 5 || repo.lastBatchIDs[1] != 9 {
		t.Fatalf("expected deduplicated [5, 9], got %v (calls=%d)", repo.lastBatchIDs, repo.batchCallCount)
	}
	if batch[5].ActiveBalance != 2.5 {
		t.Fatalf("unexpected batch[5]: %+v", batch[5])
	}
}

func TestExpiringBalanceService_ListUserLotsValidationAndNormalization(t *testing.T) {
	ctx := context.Background()
	repo := &fakeExpiringBalanceRepo{
		listLots:       []ExpiringBalanceLot{{ID: 1, UserID: 7, Status: ExpiringLotStatusActive}},
		listPagination: &pagination.PaginationResult{Total: 1, Page: 1, PageSize: 20, Pages: 1},
	}
	svc := NewExpiringBalanceService(repo)

	// Invalid userID rejected.
	if _, _, err := svc.ListUserLots(ctx, 0, "all", pagination.PaginationParams{}); err == nil {
		t.Fatalf("expected error for userID <= 0")
	}

	// Invalid status rejected as BadRequest.
	_, _, err := svc.ListUserLots(ctx, 7, "bogus", pagination.PaginationParams{})
	var appErr *infraerrors.ApplicationError
	if !errors.As(err, &appErr) || appErr.Code != 400 {
		t.Fatalf("expected 400 BadRequest for invalid status, got %v", err)
	}

	// "ALL" normalizes to "" and page/pageSize default cleanly.
	lots, pag, err := svc.ListUserLots(ctx, 7, " ALL ", pagination.PaginationParams{Page: 0, PageSize: 0})
	if err != nil {
		t.Fatalf("list lots: %v", err)
	}
	if len(lots) != 1 || pag.Total != 1 {
		t.Fatalf("unexpected lots/pag: %+v %+v", lots, pag)
	}
	if repo.lastListUserID != 7 || repo.lastListStatus != "" || repo.lastListParams.Page != 1 || repo.lastListParams.PageSize != 20 {
		t.Fatalf("unexpected normalized params passed to repo: uid=%d status=%q params=%+v", repo.lastListUserID, repo.lastListStatus, repo.lastListParams)
	}

	// Valid status passes through lowercased.
	if _, _, err := svc.ListUserLots(ctx, 7, "EXPIRED", pagination.PaginationParams{Page: 2, PageSize: 50}); err != nil {
		t.Fatalf("list expired lots: %v", err)
	}
	if repo.lastListStatus != ExpiringLotStatusExpired || repo.lastListParams.Page != 2 || repo.lastListParams.PageSize != 50 {
		t.Fatalf("unexpected status/params: status=%q params=%+v", repo.lastListStatus, repo.lastListParams)
	}
}

func TestExpiringBalanceService_ComputeExpiresAt(t *testing.T) {
	svc := NewExpiringBalanceService(nil)

	loc := timezone.Location()
	now := time.Date(2026, 11, 27, 23, 30, 15, 0, loc)

	// N=1 -> next day 00:00:00 in global timezone.Location().
	exp1 := svc.ComputeExpiresAt(now, 1)
	want1 := time.Date(2026, 11, 28, 0, 0, 0, 0, loc)
	if !exp1.Equal(want1) || exp1.Location() != loc {
		t.Fatalf("N=1 mismatch: want %v (%v) got %v (%v)", want1, loc, exp1, exp1.Location())
	}

	// N<=0 clamps to 1.
	exp0 := svc.ComputeExpiresAt(now, 0)
	if !exp0.Equal(want1) {
		t.Fatalf("N=0 clamp mismatch: want %v got %v", want1, exp0)
	}

	// N=7 -> +7 days 00:00:00 in global timezone.Location().
	exp7 := svc.ComputeExpiresAt(now, 7)
	want7 := time.Date(2026, 12, 4, 0, 0, 0, 0, loc)
	if !exp7.Equal(want7) {
		t.Fatalf("N=7 mismatch: want %v got %v", want7, exp7)
	}

	// Input in another timezone is converted into timezone.Location() before computing midnight.
	otherZone := time.FixedZone("UTC+8", 8*3600)
	inputOther := time.Date(2026, 11, 27, 1, 0, 0, 0, otherZone)
	wantOther := timezone.StartOfDay(inputOther).AddDate(0, 0, 2)
	expOther := svc.ComputeExpiresAt(inputOther, 2)
	if !expOther.Equal(wantOther) || expOther.Location() != loc {
		t.Fatalf("cross-zone mismatch: want %v got %v", wantOther, expOther)
	}
}
