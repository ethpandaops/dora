package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"time"

	logger "github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

var layoutTemplateFiles = []string{
	"_layout/layout.html",
	"_layout/header.html",
	"_layout/footer.html",
}

func InitPageData(w http.ResponseWriter, r *http.Request, active, path, title string, mainTemplates []string) *types.PageData {
	fullTitle := fmt.Sprintf("%v - %v", utils.Config.Frontend.SiteName, title)

	if title == "" {
		fullTitle = fmt.Sprintf("%v", utils.Config.Frontend.SiteName)
	}

	isMainnet := utils.Config.Chain.Config.ConfigName == "mainnet"
	buildTime, _ := time.Parse("2006-01-02T15:04:05Z", utils.Buildtime)
	siteDomain := utils.Config.Frontend.SiteDomain
	if siteDomain == "" {
		siteDomain = r.Host
	}

	data := &types.PageData{
		Meta: &types.Meta{
			Title:       fullTitle,
			Description: "Dora the Explorer makes the Ethereum Beacon Chain accessible to non-technical end users",
			Domain:      siteDomain,
			Path:        path,
			Templates:   strings.Join(mainTemplates, ","),
		},
		Active:                active,
		Data:                  &types.Empty{},
		Version:               utils.GetExplorerVersion(),
		BuildTime:             fmt.Sprintf("%v", buildTime.Unix()),
		Year:                  time.Now().UTC().Year(),
		ExplorerTitle:         utils.Config.Frontend.SiteName,
		ExplorerSubtitle:      utils.Config.Frontend.SiteSubtitle,
		ExplorerLogo:          utils.Config.Frontend.SiteLogo,
		ChainSlotsPerEpoch:    utils.Config.Chain.Config.SlotsPerEpoch,
		ChainSecondsPerSlot:   utils.Config.Chain.Config.SecondsPerSlot,
		ChainGenesisTimestamp: utils.Config.Chain.GenesisTimestamp,
		Mainnet:               isMainnet,
		DepositContract:       utils.Config.Chain.Config.DepositContractAddress,
		ChainConfig:           utils.Config.Chain.Config,
		Lang:                  "en-US",
		Debug:                 utils.Config.Frontend.Debug,
		MainMenuItems:         createMenuItems(active),
	}

	if utils.Config.Frontend.SiteDescription != "" {
		data.Meta.Description = utils.Config.Frontend.SiteDescription
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

func createMenuItems(active string) []types.MainMenuItem {
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
							Label: "Overview",
							Path:  "/",
							Icon:  "fa-home",
						},
					},
				},
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
							Label: "Clients",
							Path:  "/clients",
							Icon:  "fa-server",
						},
						{
							Label: "Forks",
							Path:  "/forks",
							Icon:  "fa-code-fork",
						},
					},
				},
			},
		},
		{
			Label:    "Validators",
			IsActive: active == "validators",
			Groups: []types.NavigationGroup{
				{
					Links: []types.NavigationLink{
						{
							Label: "Validators",
							Path:  "/validators",
							Icon:  "fa-table",
						},
						{
							Label: "Validator Activity",
							Path:  "/validators/activity",
							Icon:  "fa-tachometer",
						},
					},
				},
				{
					Links: []types.NavigationLink{
						{
							Label: "Deposits",
							Path:  "/validators/deposits",
							Icon:  "fa-file-signature",
						},
					},
				},
				{
					Links: []types.NavigationLink{
						{
							Label: "Voluntary Exits",
							Path:  "/validators/voluntary_exits",
							Icon:  "fa-door-open",
						},
						{
							Label: "Slashings",
							Path:  "/validators/slashings",
							Icon:  "fa-user-slash",
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
