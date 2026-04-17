package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/modelcatalog"
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

func initModelCatalogForCLI(baseCtx context.Context) modelcatalog.CatalogInitStatus {
	if baseCtx == nil {
		return modelcatalog.CatalogInitStatus{RemoteError: fmt.Errorf("cli: context is required")}
	}
	timeoutCtx, cancel := context.WithTimeout(baseCtx, modelCatalogBootstrapTimeout)
	defer cancel()
	status := modelcatalog.InitModelCatalogWithStatus(timeoutCtx, nil, defaultModelCatalogOverridePath())
	if status.RemoteFetched {
		modelCatalogRefreshMu.Lock()
		lastModelCatalogRefresh = time.Now()
		modelCatalogRefreshMu.Unlock()
	}
	return status
}

func refreshModelCatalogForConnect(baseCtx context.Context) (modelcatalog.CatalogInitStatus, bool) {
	if !modelCatalogRefreshDue() {
		return modelcatalog.CatalogInitStatus{}, false
	}
	return initModelCatalogFn(baseCtx), true
}

func modelCatalogRefreshDue() bool {
	modelCatalogRefreshMu.Lock()
	fresh := !lastModelCatalogRefresh.IsZero() && time.Since(lastModelCatalogRefresh) < modelCatalogCacheTTL
	modelCatalogRefreshMu.Unlock()
	return !fresh
}
