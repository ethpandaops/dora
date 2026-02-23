package handlers

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

var ErrInvalidPageModel = errors.New("invalid page model")

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

func handlePageError(w http.ResponseWriter, r *http.Request, pageError error) {
	if errors.Is(pageError, services.ErrTooManyPageRequests) {
		handleTooManyRequestsError(w, r)
		return
	}

	templateFiles := append(layoutTemplateFiles, "_layout/500.html")
	notFoundTemplate := templates.GetTemplate(templateFiles...)
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusInternalServerError)
	data := InitPageData(w, r, "blockchain", r.URL.Path, "Internal Error", templateFiles)
	errData := &models.ErrorPageData{
		CallTime: time.Now(),
		CallUrl:  r.URL.String(),
		ErrorMsg: pageError.Error(),
		Version:  utils.GetExplorerVersion(),
	}
	if fcError, isOk := pageError.(*services.FrontendCachePageError); isOk {
		errData.StackTrace = fcError.Stack()
	}
	data.Data = errData
	err := notFoundTemplate.ExecuteTemplate(w, "layout", data)
	if err != nil {
		logrus.Errorf("error executing page error template for %v route: %v", r.URL.String(), err)
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

func handleTooManyRequestsError(w http.ResponseWriter, r *http.Request) {
	templateFiles := append(layoutTemplateFiles, "_layout/429.html")
	tooManyTemplate := templates.GetTemplate(templateFiles...)
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Retry-After", "5")
	w.WriteHeader(http.StatusTooManyRequests)
	data := InitPageData(w, r, "blockchain", r.URL.Path, "Too Many Requests", templateFiles)
	err := tooManyTemplate.ExecuteTemplate(w, "layout", data)
	if err != nil {
		logrus.Errorf("error executing too-many-requests template for %v route: %v", r.URL.String(), err)
		http.Error(w, "Too many requests, please try again later", http.StatusTooManyRequests)
	}
}
