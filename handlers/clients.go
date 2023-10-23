package handlers

import (
	"net/http"
	"time"

	"github.com/pk910/dora/services"
	"github.com/pk910/dora/templates"
	"github.com/pk910/dora/types/models"
	"github.com/pk910/dora/utils"
	"github.com/sirupsen/logrus"
)

// Clients will return the main "clients" page using a go template
func Clients(w http.ResponseWriter, r *http.Request) {
	var clientsTemplateFiles = append(layoutTemplateFiles,
		"clients/clients.html",
	)

	var pageTemplate = templates.GetTemplate(clientsTemplateFiles...)
	data := InitPageData(w, r, "clients", "/clients", "Clients", clientsTemplateFiles)

	var pageError error
	data.Data, pageError = getClientsPageData()
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "clients.go", "Clients", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getClientsPageData() (*models.ClientsPageData, error) {
	pageData := &models.ClientsPageData{}
	pageCacheKey := "clients"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildClientsPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ClientsPageData)
		if !resOk {
			return nil, InvalidPageModelError
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildClientsPageData() (*models.ClientsPageData, time.Duration) {
	logrus.Debugf("clients page called")
	pageData := &models.ClientsPageData{
		Clients: []*models.ClientsPageDataClient{},
	}
	cacheTime := time.Duration(utils.Config.Chain.Config.SecondsPerSlot) * time.Second

	for _, client := range services.GlobalBeaconService.GetClients() {
		lastHeadSlot, lastHeadRoot, clientRefresh := client.GetLastHead()
		if lastHeadSlot < 0 {
			lastHeadSlot = 0
		}
		resClient := &models.ClientsPageDataClient{
			Index:       int(client.GetIndex()) + 1,
			Name:        client.GetName(),
			Version:     client.GetVersion(),
			HeadSlot:    uint64(lastHeadSlot),
			HeadRoot:    lastHeadRoot,
			Status:      client.GetStatus(),
			LastRefresh: clientRefresh,
			LastError:   client.GetLastClientError(),
		}
		pageData.Clients = append(pageData.Clients, resClient)
	}
	pageData.ClientCount = uint64(len(pageData.Clients))

	return pageData, cacheTime
}
