// @title Dora Explorer API
// @version 1.0
// @description API for Dora Ethereum Explorer - provides access to execution and consensus client information
// @host localhost:8080
// @BasePath /api
package main

import (
	"context"
	"flag"
	"io/fs"
	"net"
	"net/http"
	_ "net/http/pprof"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/urfave/negroni"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/handlers"
	"github.com/ethpandaops/dora/handlers/api"
	"github.com/ethpandaops/dora/handlers/middleware"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/static"
	"github.com/ethpandaops/dora/types"
	uipackage "github.com/ethpandaops/dora/ui-package"
	"github.com/ethpandaops/dora/utils"

	// Swagger
	"github.com/ethpandaops/dora/docs"
	httpSwagger "github.com/swaggo/http-swagger"
)

func main() {
	configPath := flag.String("config", "", "Path to the config file, if empty string defaults will be used")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &types.Config{}
	err := utils.ReadConfig(cfg, *configPath)
	if err != nil {
		logrus.Fatalf("error reading config file: %v", err)
	}
	utils.Config = cfg
	logWriter, logger := utils.InitLogger()
	defer logWriter.Dispose()

	logger.WithFields(logrus.Fields{
		"config":  *configPath,
		"version": utils.BuildVersion,
		"release": utils.BuildRelease,
	}).Printf("starting")

	db.MustInitDB(&cfg.Database)
	err = db.ApplyEmbeddedDbSchema(-2)
	if err != nil {
		logger.Fatalf("error initializing db schema: %v", err)
	}

	services.InitChainService(ctx, logger)

	var webserver *http.Server
	if cfg.Frontend.Enabled || cfg.Api.Enabled {
		websrv, err := startWebserver(logger)
		if err != nil {
			logger.Fatalf("error starting webserver: %v", err)
		}
		webserver = websrv
	}

	if cfg.Frontend.Enabled {
		err = services.StartFrontendCache()
		if err != nil {
			logger.Fatalf("error starting frontend cache service: %v", err)
		}
	}

	err = services.GlobalBeaconService.StartService()
	if err != nil {
		logger.Fatalf("error starting beacon service: %v", err)
	}

	err = services.StartTxSignaturesService()
	if err != nil {
		logger.Fatalf("error starting tx signature service: %v", err)
	}

	if cfg.RateLimit.Enabled {
		err = services.StartCallRateLimiter(cfg.RateLimit.ProxyCount, cfg.RateLimit.Rate, cfg.RateLimit.Burst)
		if err != nil {
			logger.Fatalf("error starting call rate limiter: %v", err)
		}
	}

	if webserver != nil {
		router := mux.NewRouter()

		if cfg.Api.Enabled {
			apiRouter := router.PathPrefix("/api").Subrouter()
			startApi(apiRouter)
		}

		if cfg.Frontend.Enabled {
			startFrontend(router)
		}

		n := negroni.New()
		n.Use(negroni.NewRecovery())
		//n.Use(gzip.Gzip(gzip.DefaultCompression))
		n.UseHandler(router)

		webserver.Handler = n
	}

	utils.WaitForCtrlC()
	logger.Println("exiting...")
	services.GlobalBeaconService.StopService()
	db.MustCloseDB()
}

func startWebserver(logger logrus.FieldLogger) (*http.Server, error) {
	// build a early router that serves the cl clients page only
	// the frontend relies on a properly initialized chain service and will be served by the main router later
	router := mux.NewRouter()

	router.HandleFunc("/", handlers.ClientsCL).Methods("GET")

	fileSys := http.FS(static.Files)
	router.PathPrefix("/").Handler(handlers.CustomFileServer(http.FileServer(fileSys), fileSys, handlers.NotFound))

	n := negroni.New()
	n.Use(negroni.NewRecovery())
	n.UseHandler(router)

	if utils.Config.Frontend.HttpWriteTimeout == 0 {
		utils.Config.Frontend.HttpWriteTimeout = time.Second * 15
	}
	if utils.Config.Frontend.HttpReadTimeout == 0 {
		utils.Config.Frontend.HttpReadTimeout = time.Second * 15
	}
	if utils.Config.Frontend.HttpIdleTimeout == 0 {
		utils.Config.Frontend.HttpIdleTimeout = time.Second * 60
	}
	srv := &http.Server{
		Addr:         utils.Config.Server.Host + ":" + utils.Config.Server.Port,
		WriteTimeout: utils.Config.Frontend.HttpWriteTimeout,
		ReadTimeout:  utils.Config.Frontend.HttpReadTimeout,
		IdleTimeout:  utils.Config.Frontend.HttpIdleTimeout,
		Handler:      n,
	}

	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return nil, err
	}

	logger.Printf("http server listening on %v", srv.Addr)
	go func() {
		if err := srv.Serve(listener); err != nil {
			logger.WithError(err).Fatal("Error serving frontend")
		}
	}()

	return srv, nil
}

func startFrontend(router *mux.Router) {
	router.HandleFunc("/", handlers.Index).Methods("GET")
	router.HandleFunc("/index", handlers.Index).Methods("GET")
	router.HandleFunc("/index/data", handlers.IndexData).Methods("GET")
	router.HandleFunc("/clients/consensus", handlers.ClientsCL).Methods("GET")
	router.HandleFunc("/clients/consensus/refresh", handlers.ClientsCLRefresh).Methods("POST")
	router.HandleFunc("/clients/consensus/refresh/status", handlers.ClientsCLRefreshStatus).Methods("GET")
	router.HandleFunc("/clients/execution", handlers.ClientsEl).Methods("GET")
	router.HandleFunc("/clients/execution/refresh", handlers.ClientsELRefresh).Methods("POST")
	router.HandleFunc("/clients/execution/refresh/status", handlers.ClientsELRefreshStatus).Methods("GET")
	router.HandleFunc("/forks", handlers.Forks).Methods("GET")
	router.HandleFunc("/epochs", handlers.Epochs).Methods("GET")
	router.HandleFunc("/epoch/{epoch}", handlers.Epoch).Methods("GET")
	router.HandleFunc("/slots", handlers.Slots).Methods("GET")
	router.HandleFunc("/slots/filtered", handlers.SlotsFiltered).Methods("GET")
	router.HandleFunc("/slot/{slotOrHash}", handlers.Slot).Methods("GET")
	router.HandleFunc("/slot/{root}/blob/{commitment}", handlers.SlotBlob).Methods("GET")
	router.HandleFunc("/blocks", handlers.Blocks).Methods("GET")
	router.HandleFunc("/mev/blocks", handlers.MevBlocks).Methods("GET")

	router.HandleFunc("/search", handlers.Search).Methods("GET")
	router.HandleFunc("/search/{type}", handlers.SearchAhead).Methods("GET")
	router.HandleFunc("/validators", handlers.Validators).Methods("GET")
	router.HandleFunc("/validators/activity", handlers.ValidatorsActivity).Methods("GET")
	router.HandleFunc("/validators/offline", handlers.ValidatorsOffline).Methods("GET")
	router.HandleFunc("/validators/deposits", handlers.Deposits).Methods("GET")
	router.HandleFunc("/validators/deposits/submit", handlers.SubmitDeposit).Methods("GET", "POST")
	router.HandleFunc("/validators/initiated_deposits", handlers.InitiatedDeposits).Methods("GET")
	router.HandleFunc("/validators/included_deposits", handlers.IncludedDeposits).Methods("GET")
	router.HandleFunc("/validators/queued_deposits", handlers.QueuedDeposits).Methods("GET")
	router.HandleFunc("/validators/voluntary_exits", handlers.VoluntaryExits).Methods("GET")
	router.HandleFunc("/validators/slashings", handlers.Slashings).Methods("GET")
	router.HandleFunc("/validators/el_withdrawals", handlers.ElWithdrawals).Methods("GET")
	router.HandleFunc("/validators/el_consolidations", handlers.ElConsolidations).Methods("GET")
	router.HandleFunc("/validators/submit_consolidations", handlers.SubmitConsolidation).Methods("GET")
	router.HandleFunc("/validators/submit_withdrawals", handlers.SubmitWithdrawal).Methods("GET")
	router.HandleFunc("/validator/{idxOrPubKey}", handlers.Validator).Methods("GET")
	router.HandleFunc("/validator/{index}/slots", handlers.ValidatorSlots).Methods("GET")

	if utils.Config.Frontend.Pprof {
		// add pprof handler
		router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)
		router.HandleFunc("/debug/cache", handlers.DebugCache).Methods("GET")
	}

	if utils.Config.Frontend.Debug {
		// serve files from local directory when debugging, instead of from go embed file
		templatesHandler := http.FileServer(http.Dir("templates"))
		router.PathPrefix("/templates").Handler(http.StripPrefix("/templates/", templatesHandler))

		cssHandler := http.FileServer(http.Dir("static/css"))
		router.PathPrefix("/css").Handler(http.StripPrefix("/css/", cssHandler))

		doraUiHandler := http.FileServer(http.Dir("ui-package/dist"))
		router.PathPrefix("/ui-package").Handler(http.StripPrefix("/ui-package/", doraUiHandler))

		jsHandler := http.FileServer(http.Dir("static/js"))
		router.PathPrefix("/js").Handler(http.StripPrefix("/js/", jsHandler))
	} else {
		// serve dora ui package from go embed
		uiEmbedFS, _ := fs.Sub(uipackage.Files, "dist")
		uiFileSys := http.FS(uiEmbedFS)
		uiHandler := handlers.CustomFileServer(http.FileServer(uiFileSys), uiFileSys, handlers.NotFound)
		router.PathPrefix("/ui-package").Handler(http.StripPrefix("/ui-package/", uiHandler))
	}

	// serve static files from go embed
	fileSys := http.FS(static.Files)
	router.PathPrefix("/").Handler(handlers.CustomFileServer(http.FileServer(fileSys), fileSys, handlers.NotFound))
}

func createSwaggerHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Import the docs package to access SwaggerInfo
		swaggerInfo := docs.SwaggerInfo

		// Set the host dynamically based on the request
		swaggerInfo.Host = r.Host

		// Determine scheme based on request
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		swaggerInfo.Schemes = []string{scheme}

		// Serve the swagger UI with dynamic host configuration
		httpSwagger.Handler(
			httpSwagger.URL("/api/swagger/doc.json"),
			httpSwagger.DeepLinking(true),
		).ServeHTTP(w, r)
	})
}

func startApi(router *mux.Router) {
	// Add the CORS middleware to all API routes
	router.Use(middleware.CorsMiddleware)

	router.HandleFunc("/v1/validator/{indexOrPubkey}", api.ApiValidatorGetV1).Methods("GET", "OPTIONS")
	router.HandleFunc("/v1/validator", api.ApiValidatorPostV1).Methods("POST", "OPTIONS")
	router.HandleFunc("/v1/validator/eth1/{eth1address}", api.ApiValidatorByEth1AddressV1).Methods("GET", "OPTIONS")
	router.HandleFunc("/v1/validator/withdrawalCredentials/{withdrawalCredentialsOrEth1address}", api.ApiWithdrawalCredentialsValidatorsV1).Methods("GET", "OPTIONS")
	router.HandleFunc("/v1/validator/{indexOrPubkey}/deposits", api.ApiValidatorDepositsV1).Methods("GET", "OPTIONS")
	router.HandleFunc("/v1/epoch/{epoch}", api.ApiEpochV1).Methods("GET", "OPTIONS")
	router.HandleFunc("/v1/slot/{slotOrHash}", api.APISlotV1).Methods("GET", "OPTIONS")
	router.HandleFunc("/v1/clients/execution", api.APIExecutionClients).Methods("GET", "OPTIONS")
	router.HandleFunc("/v1/clients/consensus", api.APIConsensusClients).Methods("GET", "OPTIONS")
	router.HandleFunc("/v1/das-guardian/scan", api.APIDasGuardianScan).Methods("POST", "OPTIONS")
	router.HandleFunc("/v1/das-guardian/mass-scan", api.APIDasGuardianMassScan).Methods("POST", "OPTIONS")

	// Swagger UI with dynamic host
	router.PathPrefix("/swagger/").Handler(createSwaggerHandler())
}
