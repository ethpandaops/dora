package handlers

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/sirupsen/logrus"
)

type customFileServer struct {
	handler         http.Handler
	root            http.FileSystem
	NotFoundHandler func(http.ResponseWriter, *http.Request)
}

// Custom FileServer which does the same as http.FileServer, but serves custom page on 404 error
func CustomFileServer(handler http.Handler, root http.FileSystem, NotFoundHandler http.HandlerFunc) http.Handler {
	return &customFileServer{handler: handler, root: root, NotFoundHandler: NotFoundHandler}
}

func (cfs *customFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// basically a copy of http.FileServer and of the first lines http.serveFile functions
	upath := r.URL.Path
	if !strings.HasPrefix(upath, "/") {
		upath = "/" + upath
		r.URL.Path = upath
	}
	name := path.Clean(upath)
	f, err := cfs.root.Open(name)
	if err != nil {
		handleHTTPError(err, cfs.NotFoundHandler, w, r)
		return
	}
	defer f.Close()

	_, err = f.Stat()
	if err != nil {
		handleHTTPError(err, cfs.NotFoundHandler, w, r)
		return
	}

	cfs.handler.ServeHTTP(w, r)
}

func handleHTTPError(err error, handler func(http.ResponseWriter, *http.Request), w http.ResponseWriter, r *http.Request) {
	// If error is 404, use custom handler
	if errors.Is(err, fs.ErrNotExist) {
		handler(w, r)
		return
	}
	// otherwise serve http error
	if errors.Is(err, fs.ErrPermission) {
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return
	}
	// Default:
	logrus.WithError(err).Errorf("page handler error")
	http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
}

func NotFound(w http.ResponseWriter, r *http.Request) {
	templateFiles := append(layoutTemplateFiles, "_layout/404.html")
	notFoundTemplate := templates.GetTemplate(templateFiles...)
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusNotFound)
	data := InitPageData(w, r, "blockchain", r.URL.Path, "Not Found", templateFiles)
	err := notFoundTemplate.ExecuteTemplate(w, "layout", data)
	if err != nil {
		logrus.Errorf("error executing not-found template for %v route: %v", r.URL.String(), err)
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}
