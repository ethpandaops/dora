package main

import (
	"flag"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	logger "github.com/sirupsen/logrus"
	"github.com/urfave/negroni"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/handlers"
	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/static"
	"github.com/pk910/light-beaconchain-explorer/types"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

func main() {
	configPath := flag.String("config", "", "Path to the config file, if empty string defaults will be used")
	flag.Parse()

	cfg := &types.Config{}
	err := utils.ReadConfig(cfg, *configPath)
	if err != nil {
		logger.Fatalf("error reading config file: %v", err)
	}
	utils.Config = cfg
	logWriter := utils.InitLogger()
	defer logWriter.Dispose()

	logger.WithFields(logger.Fields{
		"config":    *configPath,
		"version":   utils.BuildVersion,
		"release":   utils.BuildRelease,
		"chainName": utils.Config.Chain.Config.ConfigName}).Printf("starting")

	if utils.Config.Chain.Config.SlotsPerEpoch == 0 || utils.Config.Chain.Config.SecondsPerSlot == 0 {
		utils.LogFatal(err, "invalid chain configuration specified, you must specify the slots per epoch, seconds per slot and genesis timestamp in the config file", 0)
	}

	db.MustInitDB()
	err = db.ApplyEmbeddedDbSchema(-2)
	if err != nil {
		logger.Fatalf("error initializing db schema: %v", err)
	}
	err = services.StartBeaconService()
	if err != nil {
		logger.Fatalf("error starting beacon service: %v", err)
	}

	if cfg.Frontend.Enabled {
		err = services.StartFrontendCache()
		if err != nil {
			logger.Fatalf("error starting frontend cache service: %v", err)
		}

		startFrontend()
	}

	utils.WaitForCtrlC()
	logger.Println("exiting...")
	db.MustCloseDB()
}

func startFrontend() {
	router := mux.NewRouter()

	router.HandleFunc("/", handlers.Index).Methods("GET")
	router.HandleFunc("/index", handlers.Index).Methods("GET")
	router.HandleFunc("/index/data", handlers.IndexData).Methods("GET")
	router.HandleFunc("/clients", handlers.Clients).Methods("GET")
	router.HandleFunc("/epochs", handlers.Epochs).Methods("GET")
	router.HandleFunc("/epoch/{epoch}", handlers.Epoch).Methods("GET")
	router.HandleFunc("/slots", handlers.Slots).Methods("GET")
	router.HandleFunc("/slots/filtered", handlers.SlotsFiltered).Methods("GET")
	router.HandleFunc("/slot/{slotOrHash}", handlers.Slot).Methods("GET")
	router.HandleFunc("/slot/{root}/blob/{commitment}", handlers.SlotBlob).Methods("GET")
	router.HandleFunc("/search", handlers.Search).Methods("GET")
	router.HandleFunc("/search/{type}", handlers.SearchAhead).Methods("GET")
	router.HandleFunc("/validators", handlers.Validators).Methods("GET")
	router.HandleFunc("/validator/{idxOrPubKey}", handlers.Validator).Methods("GET")
	router.HandleFunc("/validator/{index}/slots", handlers.ValidatorSlots).Methods("GET")

	if utils.Config.Frontend.Debug {
		// serve files from local directory when debugging, instead of from go embed file
		templatesHandler := http.FileServer(http.Dir("templates"))
		router.PathPrefix("/templates").Handler(http.StripPrefix("/templates/", templatesHandler))

		cssHandler := http.FileServer(http.Dir("static/css"))
		router.PathPrefix("/css").Handler(http.StripPrefix("/css/", cssHandler))

		jsHandler := http.FileServer(http.Dir("static/js"))
		router.PathPrefix("/js").Handler(http.StripPrefix("/js/", jsHandler))
	}

	fileSys := http.FS(static.Files)
	router.PathPrefix("/").Handler(handlers.CustomFileServer(http.FileServer(fileSys), fileSys, handlers.NotFound))

	n := negroni.New()
	n.Use(negroni.NewRecovery())
	//n.Use(gzip.Gzip(gzip.DefaultCompression))
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

	logger.Printf("http server listening on %v", srv.Addr)
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			logger.WithError(err).Fatal("Error serving frontend")
		}
	}()
}
