package main

import (
	"context"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/modelcatalog"
)

func TestRefreshModelCatalogForConnect_UsesTTL(t *testing.T) {
	prevInit := initModelCatalogFn
	prevRefresh := connectModelCatalogRefreshFn
	prevLast := lastModelCatalogRefresh
	lastModelCatalogRefresh = time.Time{}
	calls := 0
	initModelCatalogFn = func(_ context.Context) modelcatalog.CatalogInitStatus {
		calls++
		lastModelCatalogRefresh = time.Now()
		return modelcatalog.CatalogInitStatus{RemoteFetched: true}
	}
	connectModelCatalogRefreshFn = refreshModelCatalogForConnect
	t.Cleanup(func() {
		initModelCatalogFn = prevInit
		connectModelCatalogRefreshFn = prevRefresh
		lastModelCatalogRefresh = prevLast
	})

	if _, refreshed := refreshModelCatalogForConnect(context.Background()); !refreshed {
		t.Fatal("expected first refresh")
	}
	if _, refreshed := refreshModelCatalogForConnect(context.Background()); refreshed {
		t.Fatal("did not expect refresh within TTL")
	}
	if calls != 1 {
		t.Fatalf("expected one init call, got %d", calls)
	}
}
