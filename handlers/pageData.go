package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"time"

	logger "github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/types"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

var layoutTemplateFiles = []string{
	"_layout/layout.html",
	"_layout/header.html",
	"_layout/footer.html",
}

func InitPageData(w http.ResponseWriter, r *http.Request, active, path, title string, mainTemplates []string) *types.PageData {
	fullTitle := fmt.Sprintf("%v - %v - %v", title, utils.Config.Frontend.SiteName, time.Now().Year())

	if title == "" {
		fullTitle = fmt.Sprintf("%v - %v", utils.Config.Frontend.SiteName, time.Now().Year())
	}

	isMainnet := utils.Config.Chain.Config.ConfigName == "mainnet"
	buildTime, _ := time.Parse("2006-01-02T15:04:05Z", utils.Buildtime)
	data := &types.PageData{
		Meta: &types.Meta{
			Title:       fullTitle,
			Description: "beaconchain makes Ethereum accessible to non-technical end users",
			Domain:      r.Host,
			Path:        path,
			Templates:   strings.Join(mainTemplates, ","),
		},
		Active:                active,
		Data:                  &types.Empty{},
		Version:               "git-" + utils.BuildVersion,
		BuildTime:             fmt.Sprintf("%v", buildTime.Unix()),
		Year:                  time.Now().UTC().Year(),
		ExplorerTitle:         utils.Config.Frontend.SiteName,
		ExplorerSubtitle:      utils.Config.Frontend.SiteSubtitle,
		ChainSlotsPerEpoch:    utils.Config.Chain.Config.SlotsPerEpoch,
		ChainSecondsPerSlot:   utils.Config.Chain.Config.SecondsPerSlot,
		ChainGenesisTimestamp: utils.Config.Chain.GenesisTimestamp,
		Mainnet:               isMainnet,
		DepositContract:       utils.Config.Chain.Config.DepositContractAddress,
		ChainConfig:           utils.Config.Chain.Config,
		Lang:                  "en-US",
		Debug:                 utils.Config.Frontend.Debug,
		MainMenuItems:         createMenuItems(active, isMainnet),
	}

	if utils.BuildRelease == "" {
		data.Version = fmt.Sprintf("git-%v", utils.BuildVersion)
	} else {
		data.Version = fmt.Sprintf("%v (git-%v)", utils.BuildRelease, utils.BuildVersion)
	}

	acceptedLangs := strings.Split(r.Header.Get("Accept-Language"), ",")
	if len(acceptedLangs) > 0 {
		if strings.Contains(acceptedLangs[0], "ru") || strings.Contains(acceptedLangs[0], "RU") {
			data.Lang = "ru-RU"
		}
	}

	for _, v := range r.Cookies() {
		if v.Name == "language" {
			data.Lang = v.Value
			break
		}
	}

	return data
}

func createMenuItems(active string, isMain bool) []types.MainMenuItem {
	hiddenFor := []string{"confirmation", "login", "register"}

	if utils.SliceContains(hiddenFor, active) {
		return []types.MainMenuItem{}
	}
	return []types.MainMenuItem{
		{
			Label:    "Blockchain",
			IsActive: active == "blockchain",
			Groups: []types.NavigationGroup{
				{
					Links: []types.NavigationLink{
						{
							Label: "Epochs",
							Path:  "/epochs",
							Icon:  "fa-history",
						},
						{
							Label: "Slots",
							Path:  "/slots",
							Icon:  "fa-cube",
						},
					},
				},
				{
					Links: []types.NavigationLink{
						{
							Label: "Validators",
							Path:  "/validators",
							Icon:  "fa-table",
						},
					},
				},
				{
					Links: []types.NavigationLink{
						{
							Label: "Clients",
							Path:  "/clients",
							Icon:  "fa-server",
						},
					},
				},
			},
		},
	}
}

// used to handle errors constructed by Template.ExecuteTemplate correctly
func handleTemplateError(w http.ResponseWriter, r *http.Request, fileIdentifier string, functionIdentifier string, infoIdentifier string, err error) error {
	// ignore network related errors
	if err != nil && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ETIMEDOUT) {
		logger.WithFields(logger.Fields{
			"file":       fileIdentifier,
			"function":   functionIdentifier,
			"info":       infoIdentifier,
			"error type": fmt.Sprintf("%T", err),
			"route":      r.URL.String(),
		}).WithError(err).Error("error executing template")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
	return err
}
