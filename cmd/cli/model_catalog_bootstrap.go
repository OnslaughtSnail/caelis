package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

const modelCatalogBootstrapTimeout = 5 * time.Second
const modelCatalogCacheTTL = 24 * time.Hour

var initModelCatalogFn = initModelCatalogForCLI
var connectModelCatalogRefreshFn = refreshModelCatalogForConnect

var (
	modelCatalogRefreshMu   sync.Mutex
	lastModelCatalogRefresh time.Time
)

func defaultModelCatalogOverridePath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "model_capabilities.json")
}

func initModelCatalogForCLI(baseCtx context.Context) modelproviders.CatalogInitStatus {
	ctx := baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, modelCatalogBootstrapTimeout)
	defer cancel()
	status := modelproviders.InitModelCatalogWithStatus(timeoutCtx, nil, defaultModelCatalogOverridePath())
	if status.RemoteFetched {
		modelCatalogRefreshMu.Lock()
		lastModelCatalogRefresh = time.Now()
		modelCatalogRefreshMu.Unlock()
	}
	return status
}

func refreshModelCatalogForConnect(baseCtx context.Context) (modelproviders.CatalogInitStatus, bool) {
	if !modelCatalogRefreshDue() {
		return modelproviders.CatalogInitStatus{}, false
	}
	return initModelCatalogFn(baseCtx), true
}

func modelCatalogRefreshDue() bool {
	modelCatalogRefreshMu.Lock()
	fresh := !lastModelCatalogRefresh.IsZero() && time.Since(lastModelCatalogRefresh) < modelCatalogCacheTTL
	modelCatalogRefreshMu.Unlock()
	return !fresh
}
