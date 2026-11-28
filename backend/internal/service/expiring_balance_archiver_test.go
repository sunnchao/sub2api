package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type archiverTestBillingCacheInvalidator struct {
	calls     []int64
	failForID int64
}

func (f *archiverTestBillingCacheInvalidator) InvalidateUserBalance(_ context.Context, userID int64) error {
	f.calls = append(f.calls, userID)
	if f.failForID == userID {
		return errors.New("redis temporary failure")
	}
	return nil
}

type archiverTestAuthCacheInvalidator struct {
	calls []int64
}

func (f *archiverTestAuthCacheInvalidator) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	f.calls = append(f.calls, userID)
}

type archiverTestLeaderLockCache struct {
	allow       bool
	acquireErr  error
	acquireKeys []string
	releaseKeys []string
	releaseOwn  []string
}

func (f *archiverTestLeaderLockCache) TryAcquireLeaderLock(_ context.Context, key, _ string, _ time.Duration) (bool, error) {
	f.acquireKeys = append(f.acquireKeys, key)
	return f.allow, f.acquireErr
}

func (f *archiverTestLeaderLockCache) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	f.releaseKeys = append(f.releaseKeys, key)
	f.releaseOwn = append(f.releaseOwn, owner)
	return nil
}

func TestExpiringBalanceArchiver_MultiBatchDrainAndCacheInvalidation(t *testing.T) {
	batchCalls := 0
	repo := &fakeExpiringBalanceRepo{
		archiveFn: func(_ context.Context, limit int) ([]int64, int, error) {
			batchCalls++
			if limit != 2 {
				t.Fatalf("expected batchSize 2, got %d", limit)
			}
			if batchCalls == 1 {
				return []int64{1, 2, 1}, 2, nil
			}
			return []int64{2, 3, 0}, 1, nil
		},
	}
	billingCache := &archiverTestBillingCacheInvalidator{failForID: 1}
	authCache := &archiverTestAuthCacheInvalidator{}

	archiver := NewExpiringBalanceArchiver(repo, billingCache, authCache, 0, 2)
	if err := archiver.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if batchCalls != 2 {
		t.Fatalf("expected 2 batch calls, got %d", batchCalls)
	}
	// Users {1, 2, 3} deduplicated, and billingCache error on user 1 does not abort users 2 and 3.
	if len(billingCache.calls) != 3 || billingCache.calls[0] != 1 || billingCache.calls[1] != 2 || billingCache.calls[2] != 3 {
		t.Fatalf("unexpected billingCache invalidation calls: %v", billingCache.calls)
	}
	if len(authCache.calls) != 3 || authCache.calls[0] != 1 || authCache.calls[1] != 2 || authCache.calls[2] != 3 {
		t.Fatalf("unexpected authCache invalidation calls: %v", authCache.calls)
	}
}

func TestExpiringBalanceArchiver_PartialFailureStillInvalidatesEarlierBatches(t *testing.T) {
	batchCalls := 0
	repo := &fakeExpiringBalanceRepo{
		archiveFn: func(_ context.Context, _ int) ([]int64, int, error) {
			batchCalls++
			if batchCalls == 1 {
				return []int64{10, 11}, 2, nil
			}
			return nil, 0, errors.New("db timeout")
		},
	}
	billingCache := &archiverTestBillingCacheInvalidator{}
	authCache := &archiverTestAuthCacheInvalidator{}

	archiver := NewExpiringBalanceArchiver(repo, billingCache, authCache, 0, 2)
	err := archiver.RunOnce(context.Background())
	if err == nil {
		t.Fatalf("expected error from batch 2")
	}
	if len(billingCache.calls) != 2 || len(authCache.calls) != 2 {
		t.Fatalf("expected batch 1 users to still be invalidated: billing=%v auth=%v", billingCache.calls, authCache.calls)
	}
}

func TestExpiringBalanceArchiver_LeaderLockGating(t *testing.T) {
	var archiveCalls atomic.Int32
	repo := &fakeExpiringBalanceRepo{
		archiveFn: func(_ context.Context, _ int) ([]int64, int, error) {
			archiveCalls.Add(1)
			return nil, 0, nil
		},
	}

	// When peer holds leader lock, archiver skips sweep and does not release.
	lockDenied := &archiverTestLeaderLockCache{allow: false}
	archiver := NewExpiringBalanceArchiver(repo, nil, nil, 0, 0)
	archiver.SetLeaderLock(lockDenied, nil)
	if err := archiver.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce denied: %v", err)
	}
	if archiveCalls.Load() != 0 || len(lockDenied.releaseKeys) != 0 {
		t.Fatalf("expected 0 archive calls and 0 releases when lock denied, got calls=%d releases=%v", archiveCalls.Load(), lockDenied.releaseKeys)
	}

	// When lock acquired, archiver runs sweep and releases matching key/owner.
	lockGranted := &archiverTestLeaderLockCache{allow: true}
	archiver.SetLeaderLock(lockGranted, nil)
	if err := archiver.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce granted: %v", err)
	}
	if archiveCalls.Load() != 1 || len(lockGranted.releaseKeys) != 1 || lockGranted.releaseKeys[0] != expiringBalanceArchiverLeaderLockKey {
		t.Fatalf("expected 1 archive call and 1 lock release, got calls=%d releases=%v", archiveCalls.Load(), lockGranted.releaseKeys)
	}
	if lockGranted.releaseOwn[0] != archiver.instanceID {
		t.Fatalf("expected release owner %s, got %s", archiver.instanceID, lockGranted.releaseOwn[0])
	}
}

func TestExpiringBalanceArchiver_StartStopLifecycleAndNilSafety(t *testing.T) {
	var nilArchiver *ExpiringBalanceArchiver
	nilArchiver.SetLeaderLock(nil, nil)
	nilArchiver.Start()
	nilArchiver.Stop()
	if err := nilArchiver.RunOnce(context.Background()); err != nil {
		t.Fatalf("nil RunOnce: %v", err)
	}

	var calls atomic.Int32
	doneCh := make(chan struct{}, 1)
	repo := &fakeExpiringBalanceRepo{
		archiveFn: func(_ context.Context, _ int) ([]int64, int, error) {
			if calls.Add(1) == 1 {
				select {
				case doneCh <- struct{}{}:
				default:
				}
			}
			return nil, 0, nil
		},
	}

	archiver := NewExpiringBalanceArchiver(repo, nil, nil, 20*time.Millisecond, 100)
	archiver.Start()
	archiver.Start() // idempotent double-start

	select {
	case <-doneCh:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for initial archiver sweep")
	}

	archiver.Stop()
	archiver.Stop() // idempotent double-stop
}

func TestExpiringBalanceArchiver_ConcurrentStartStopRace(t *testing.T) {
	const iterations = 100
	for i := 0; i < iterations; i++ {
		repo := &fakeExpiringBalanceRepo{
			archiveFn: func(_ context.Context, _ int) ([]int64, int, error) {
				return nil, 0, nil
			},
		}
		archiver := NewExpiringBalanceArchiver(repo, nil, nil, time.Millisecond, 10)

		ready := make(chan struct{})
		var wg sync.WaitGroup
		for g := 0; g < 3; g++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-ready
				archiver.Start()
			}()
			go func() {
				defer wg.Done()
				<-ready
				archiver.Stop()
			}()
		}
		close(ready)
		wg.Wait()
		archiver.Stop()
	}

	// Calling Stop() before Start() must prevent subsequent Start() from launching runLoop.
	var calls atomic.Int32
	stoppedFirst := NewExpiringBalanceArchiver(&fakeExpiringBalanceRepo{
		archiveFn: func(_ context.Context, _ int) ([]int64, int, error) {
			calls.Add(1)
			return nil, 0, nil
		},
	}, nil, nil, 5*time.Millisecond, 10)
	stoppedFirst.Stop()
	stoppedFirst.Start()
	time.Sleep(20 * time.Millisecond)
	stoppedFirst.Stop()
	if calls.Load() != 0 {
		t.Fatalf("expected 0 archive calls when Stop precedes Start, got %d", calls.Load())
	}
}
