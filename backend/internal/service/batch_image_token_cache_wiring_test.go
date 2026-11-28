package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type batchImageTokenCacheStub struct {
	token string
}

func (s *batchImageTokenCacheStub) GetAccessToken(context.Context, string) (string, error) {
	return s.token, nil
}
func (s *batchImageTokenCacheStub) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}
func (s *batchImageTokenCacheStub) DeleteAccessToken(context.Context, string) error {
	return nil
}
func (s *batchImageTokenCacheStub) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (s *batchImageTokenCacheStub) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

func TestBatchImageServicesWireTokenCacheAndBillingCacheRaceFree(t *testing.T) {
	cfg := &config.Config{}
	cfg.BatchImage.Enabled = true
	cfg.BatchImage.QueueEnabled = true

	tokenCache := &batchImageTokenCacheStub{token: "cached-sa-token"}
	billingCache := NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billingCache.Stop()

	pub := ProvideBatchImagePublicService(nil, nil, nil, nil, nil, nil, nil, nil, billingCache, tokenCache, cfg)
	require.NotNil(t, pub.BillingCache)
	vp, ok := mustVertexBatchProvider(t, pub.ProviderRegistry)
	require.True(t, ok)
	require.Equal(t, tokenCache, vp.tokenCache)

	dl := ProvideBatchImageDownloadService(nil, nil, nil, tokenCache, cfg)
	vpDL, ok := mustVertexBatchProvider(t, dl.ProviderRegistry)
	require.True(t, ok)
	require.Equal(t, tokenCache, vpDL.tokenCache)

	cleanup := ProvideBatchImageCleanupServiceWithTokenCache(nil, nil, tokenCache, cfg)
	defer cleanup.Stop()
	vpCleanup, ok := mustVertexBatchProvider(t, cleanup.ProviderRegistry)
	require.True(t, ok)
	require.Equal(t, tokenCache, vpCleanup.tokenCache)

	rtRepo := newM2BatchImageRepoStub()
	rt := ProvideBatchImageWorkerRuntimeWithBillingAndTokenCache(rtRepo, nil, nil, nil, nil, nil, nil, billingCache, tokenCache, cfg)
	defer rt.Stop()
	require.True(t, rt.Running())
	require.Equal(t, billingCache, rt.billingRecovery.BillingCache)

	pipeline, ok := rt.worker.processor.(*BatchImagePipelineProcessor)
	require.True(t, ok)
	require.Equal(t, billingCache, pipeline.ProviderProcessor.BillingCache)
	require.Equal(t, billingCache, pipeline.SettlementService.BillingCache)
	vpWorker, ok := mustVertexBatchProvider(t, pipeline.ProviderProcessor.ProviderRegistry)
	require.True(t, ok)
	require.Equal(t, tokenCache, vpWorker.tokenCache)

	// Typed nil tokenCache must normalize cleanly to nil.
	var typedNilCache *batchImageTokenCacheStub
	pub.SetTokenCache(typedNilCache)
	require.Nil(t, vp.tokenCache)
}

func TestProvideBillingCacheServiceWiresExpiringServiceAndRepo(t *testing.T) {
	cfg := &config.Config{}
	expSvc := NewExpiringBalanceService(nil)
	svc := ProvideBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil, nil, expSvc)
	defer svc.Stop()
	require.Equal(t, expSvc, svc.expiringBalanceSvc)
}

func mustVertexBatchProvider(t *testing.T, reg *BatchImageProviderRegistry) (*VertexBatchImageProvider, bool) {
	t.Helper()
	p, ok := reg.Get(BatchImageProviderVertex)
	if !ok {
		return nil, false
	}
	vp, ok := p.(*VertexBatchImageProvider)
	return vp, ok
}
