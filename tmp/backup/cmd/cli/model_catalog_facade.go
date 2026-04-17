package main

import (
	"github.com/OnslaughtSnail/caelis/internal/cli/modelcatalog"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

const (
	reasoningModeNone   = modelcatalog.ReasoningModeNone
	reasoningModeToggle = modelcatalog.ReasoningModeToggle
	reasoningModeEffort = modelcatalog.ReasoningModeEffort
	reasoningModeFixed  = modelcatalog.ReasoningModeFixed
)

func defaultCatalogModelCapabilities() modelcatalog.ModelCapabilities {
	return modelcatalog.DefaultModelCapabilities()
}

func lookupCatalogModelCapabilities(provider, modelName string) (modelcatalog.ModelCapabilities, bool) {
	return modelcatalog.LookupModelCapabilities(provider, modelName)
}

func lookupBaseCatalogModelCapabilities(provider, modelName string) (modelcatalog.ModelCapabilities, bool) {
	return modelcatalog.LookupBaseCatalogCapabilities(provider, modelName)
}

func lookupSuggestedCatalogModelCapabilities(provider, modelName string) (modelcatalog.ModelCapabilities, bool) {
	return modelcatalog.LookupSuggestedModelCapabilities(provider, modelName)
}

func lookupOverlayCatalogCapabilities(provider, modelName string) (modelcatalog.ModelCapabilities, bool) {
	return modelcatalog.LookupOverlayModelCapabilities(provider, modelName)
}

func defaultCatalogReasoningEffort(provider, modelName string) string {
	return modelcatalog.DefaultReasoningEffortForModel(provider, modelName)
}

func lookupDynamicCatalogCapabilities(provider, modelName string) (modelcatalog.ModelCapabilities, bool) {
	return modelcatalog.LookupDynamicModelCapabilities(provider, modelName)
}

func listCatalogModels(provider string) []string {
	return modelcatalog.ListCatalogModels(provider)
}

func modelcatalogApplyConfigDefaults(cfg *modelproviders.Config) {
	modelcatalog.ApplyConfigDefaults(cfg)
}

func recommendedCatalogFallbackMaxOutputTokens(contextWindow int, suggested int, supportsReasoning bool) int {
	return modelcatalog.RecommendedFallbackMaxOutputTokens(contextWindow, suggested, supportsReasoning)
}

func normalizeCatalogReasoningEffort(effort string) string {
	return modelcatalog.NormalizeReasoningEffort(effort)
}

func normalizeCatalogReasoningMode(mode string) string {
	return modelcatalog.NormalizeReasoningMode(mode)
}

func catalogSupportsReasoningEffortList(levels []string, effort string) bool {
	return modelcatalog.SupportsReasoningEffortList(levels, effort)
}
