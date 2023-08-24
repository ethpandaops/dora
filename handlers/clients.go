package handlers

import (
	"net/http"
	"time"

	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

// Clients will return the main "clients" page using a go template
func Clients(w http.ResponseWriter, r *http.Request) {
	var clientsTemplateFiles = append(layoutTemplateFiles,
		"clients/clients.html",
	)

	var pageTemplate = templates.GetTemplate(clientsTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "clients", "/clients", "Clients", clientsTemplateFiles)

	data.Data = getClientsPageData()

	if handleTemplateError(w, r, "clients.go", "Clients", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getClientsPageData() *models.ClientsPageData {
	pageData := &models.ClientsPageData{}
	pageCacheKey := "clients"
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildClientsPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.ClientsPageData)
	return pageData
}

func buildClientsPageData() (*models.ClientsPageData, time.Duration) {
	logrus.Printf("clients page called")
	pageData := &models.ClientsPageData{
		Clients: []*models.ClientsPageDataClient{},
	}
	cacheTime := time.Duration(utils.Config.Chain.Config.SecondsPerSlot) * time.Second

	for _, client := range services.GlobalBeaconService.GetClients() {
		lastHeadSlot, lastHeadRoot := client.GetLastHead()
		if lastHeadSlot < 0 {
			lastHeadSlot = 0
		}
		resClient := &models.ClientsPageDataClient{
			Index:    int(client.GetIndex()) + 1,
			Name:     client.GetName(),
			Version:  client.GetVersion(),
			HeadSlot: uint64(lastHeadSlot),
			HeadRoot: lastHeadRoot,
			Status:   client.GetStatus(),
		}
		pageData.Clients = append(pageData.Clients, resClient)
	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	return pageData, cacheTime
}
