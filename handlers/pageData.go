package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	logger "github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/services"
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
		Active:           active,
		Data:             &types.Empty{},
		Version:          utils.GetExplorerVersion(),
		BuildTime:        fmt.Sprintf("%v", buildTime.Unix()),
		Year:             time.Now().UTC().Year(),
		ExplorerTitle:    utils.Config.Frontend.SiteName,
		ExplorerSubtitle: utils.Config.Frontend.SiteSubtitle,
		ExplorerLogo:     utils.Config.Frontend.SiteLogo,
		TokenSymbol:      utils.Config.Chain.TokenSymbol,
		Lang:             "en-US",
		Debug:            utils.Config.Frontend.Debug,
		MainMenuItems:    createMenuItems(active),
		ApiEnabled:       utils.Config.Api.Enabled && !utils.Config.Api.RequireAuth,
	}

	chainState := services.GlobalBeaconService.GetChainState()
	if specs := chainState.GetSpecs(); specs != nil {
		data.IsReady = true
		data.ChainSlotsPerEpoch = specs.SlotsPerEpoch
		data.ChainSecondsPerSlot = uint64(specs.SecondsPerSlot)
		data.ChainGenesisTimestamp = uint64(chainState.GetGenesis().GenesisTime.Unix())
		data.DepositContract = common.BytesToAddress(specs.DepositContractAddress).String()
		data.Mainnet = specs.ConfigName == "mainnet"
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

	clientsMenu := []types.NavigationGroup{}
	blockchainMenu := []types.NavigationGroup{}
	validatorMenu := []types.NavigationGroup{}

	blockchainMenu = append(blockchainMenu, types.NavigationGroup{
		Links: []types.NavigationLink{
			{
				Label: "Overview",
				Path:  "/",
				Icon:  "fa-home",
			},
		},
	})
	blockchainMenu = append(blockchainMenu, types.NavigationGroup{
		Links: []types.NavigationLink{
			{
				Label: "Epochs",
				Path:  "/epochs",
				Icon:  "fa-history",
			},
			{
				Label: "Slots",
				Path:  "/slots",
				Icon:  "fa-table-list",
			},
			{
				Label: "Blocks",
				Path:  "/blocks",
				Icon:  "fa-cube",
			},
			/*
				{
					Label: "Chain Forks",
					Path:  "/chain-forks",
					Icon:  "fa-project-diagram",
				},
			*/
		},
	})
	if len(utils.Config.MevIndexer.Relays) > 0 {
		blockchainMenu = append(blockchainMenu, types.NavigationGroup{
			Links: []types.NavigationLink{
				{
					Label: "MEV Blocks",
					Path:  "/mev/blocks",
					Icon:  "fa-money-bill",
				},
			},
		})
	}

	clientLinks := []types.NavigationLink{
		{
			Label: "Consensus",
			Path:  "/clients/consensus",
			Icon:  "fa-server",
		},
	}

	if utils.Config.ExecutionApi.Endpoint != "" || len(utils.Config.ExecutionApi.Endpoints) > 0 {
		clientLinks = append(clientLinks, types.NavigationLink{
			Label: "Execution",
			Path:  "/clients/execution",
			Icon:  "fa-circle-nodes",
		})
	}

	clientLinks = append(clientLinks, types.NavigationLink{
		Label: "Forks",
		Path:  "/forks",
		Icon:  "fa-code-fork",
	})

	clientsMenu = append(clientsMenu, types.NavigationGroup{
		Links: clientLinks,
	})

	validatorMenuLinks := []types.NavigationLink{
		{
			Label: "Validators",
			Path:  "/validators",
			Icon:  "fa-table",
		},
	}

	if utils.Config.Frontend.ShowValidatorSummary {
		validatorMenuLinks = append(validatorMenuLinks, types.NavigationLink{
			Label: "Validator Summary",
			Path:  "/validators/summary",
			Icon:  "fa-chart-pie",
		})
	}

	validatorMenuLinks = append(validatorMenuLinks, types.NavigationLink{
		Label: "Validator Activity",
		Path:  "/validators/activity",
		Icon:  "fa-tachometer",
	})

	validatorMenu = append(validatorMenu, types.NavigationGroup{
		Links: validatorMenuLinks,
	})
	validatorMenu = append(validatorMenu, types.NavigationGroup{
		Links: []types.NavigationLink{
			{
				Label: "Deposits",
				Path:  "/validators/deposits",
				Icon:  "fa-file-signature",
			},
			{
				Label: "Exits",
				Path:  "/validators/exits",
				Icon:  "fa-door-open",
			},
			{
				Label: "Slashings",
				Path:  "/validators/slashings",
				Icon:  "fa-user-slash",
			},
		},
	})

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	if specs != nil && specs.ElectraForkEpoch != nil && uint64(chainState.CurrentEpoch()) >= *specs.ElectraForkEpoch {
		validatorMenu = append(validatorMenu, types.NavigationGroup{
			Links: []types.NavigationLink{
				{
					Label: "Withdrawal Requests",
					Path:  "/validators/withdrawals",
					Icon:  "fa-money-bill-transfer",
				},
				{
					Label: "Consolidation Requests",
					Path:  "/validators/consolidations",
					Icon:  "fa-square-plus",
				},
			},
		})
	}

	submitLinks := []types.NavigationLink{}
	if utils.Config.Frontend.ShowSubmitDeposit {
		submitLinks = append(submitLinks, types.NavigationLink{
			Label: "Submit Deposits",
			Path:  "/validators/deposits/submit",
			Icon:  "fa-file-import",
		})
	}

	if utils.Config.Frontend.ShowSubmitElRequests {
		submitLinks = append(submitLinks, types.NavigationLink{
			Label: "Submit Consolidations",
			Path:  "/validators/submit_consolidations",
			Icon:  "fa-square-plus",
		})
		submitLinks = append(submitLinks, types.NavigationLink{
			Label: "Submit Withdrawals & Exits",
			Path:  "/validators/submit_withdrawals",
			Icon:  "fa-money-bill-transfer",
		})
	}

	if len(submitLinks) > 0 {
		validatorMenu = append(validatorMenu, types.NavigationGroup{
			Links: submitLinks,
		})
	}

	return []types.MainMenuItem{
		{
			Label:    "Blockchain",
			IsActive: active == "blockchain",
			Groups:   blockchainMenu,
		},
		{
			Label:    "Validators",
			IsActive: active == "validators",
			Groups:   validatorMenu,
		},
		{
			Label:    "Clients",
			IsActive: active == "clients",
			Groups:   clientsMenu,
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
