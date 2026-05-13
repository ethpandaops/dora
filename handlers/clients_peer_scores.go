package handlers

import (
	"net/http"

	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// ClientsPeerScores serves the /clients/peer_scores page.
//
// This handler is registered only when PeerScoresConfig.Enabled is true.
// It currently renders a placeholder view; the actual peer score data
// wiring (collection, normalization, rendering) is intentionally
// deferred to a follow-up change to keep the initial scaffolding small
// and reviewable.
func ClientsPeerScores(w http.ResponseWriter, r *http.Request) {
	clientsTemplateFiles := append(layoutTemplateFiles,
		"clients/clients_peer_scores.html",
	)

	pageTemplate := templates.GetTemplate(clientsTemplateFiles...)
	data := InitPageData(w, r, "clients/peer_scores", "/clients/peer_scores", "Peer scores", clientsTemplateFiles)

	pageData := &models.ClientsPeerScoresPageData{}
	if utils.Config.PeerScores != nil {
		pageData.PollIntervalSeconds = utils.Config.PeerScores.PollIntervalSeconds
	}
	data.Data = pageData

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "clients_peer_scores.go", "Peer scores", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}
