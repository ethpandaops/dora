package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/utils"
)

// DebugCache will submit a consolidation request
func DebugCache(w http.ResponseWriter, r *http.Request) {
	var debugCacheTemplateFiles = append(layoutTemplateFiles,
		"debug_cache/debug_cache.html",
	)
	var pageTemplate = templates.GetTemplate(debugCacheTemplateFiles...)

	if !utils.Config.Frontend.Pprof {
		handlePageError(w, r, errors.New("debug pages are not enabled"))
		return
	}

	pageData := buildDebugCachePageData()
	data := InitPageData(w, r, "blockchain", "/debug_cache", "Debug Cache", debugCacheTemplateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "debug_cache.go", "Debug Cache", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func buildDebugCachePageData() string {
	logrus.Debugf("debug cache page called")

	cacheStats := services.GlobalBeaconService.GetBeaconIndexer().GetCacheDebugStats()
	jsonStats, _ := json.MarshalIndent(cacheStats, "", "  ")

	return string(jsonStats)
}
