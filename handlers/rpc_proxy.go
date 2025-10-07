package handlers

import (
	"net/http"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

var rpcProxy *services.RPCProxy

// InitRPCProxy initializes the RPC proxy service
func InitRPCProxy() {
	if !utils.Config.RpcProxy.Enabled {
		return
	}

	if utils.Config.RpcProxy.UpstreamURL == "" {
		logrus.Fatal("RPC proxy is enabled but no upstream URL is configured")
		return
	}

	config := &services.RPCProxyConfig{
		UpstreamURL:       utils.Config.RpcProxy.UpstreamURL,
		RequestsPerMinute: utils.Config.RpcProxy.RequestsPerMinute,
		BurstLimit:        utils.Config.RpcProxy.BurstLimit,
		Timeout:           utils.Config.RpcProxy.Timeout,
		LogRequests:       utils.Config.RpcProxy.LogRequests,
		AllowedMethods:    utils.Config.RpcProxy.AllowedMethods,
	}

	rpcProxy = services.NewRPCProxy(config)
	
	logrus.WithField("upstream", config.UpstreamURL).Info("RPC proxy initialized")
}

// RPCProxyHandler handles filtered JSON-RPC requests
func RPCProxyHandler(w http.ResponseWriter, r *http.Request) {
	if rpcProxy == nil {
		http.Error(w, "RPC proxy not available", http.StatusServiceUnavailable)
		return
	}

	rpcProxy.ServeHTTP(w, r)
}