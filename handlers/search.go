package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pk910/light-beaconchain-explorer/templates"
)

// Search will return the main "search" page using a go template
func Search(w http.ResponseWriter, r *http.Request) {
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"search/notfound.html",
	)

	urlArgs := r.URL.Query()
	searchQuery := urlArgs.Get("q")

	_, err := strconv.Atoi(searchQuery)
	if err == nil {
		http.Redirect(w, r, "/slot/"+searchQuery, http.StatusMovedPermanently)
		return
	}

	hashQuery := strings.Replace(searchQuery, "0x", "", -1)
	if len(hashQuery) == 64 {
		http.Redirect(w, r, "/slot/0x"+hashQuery, http.StatusMovedPermanently)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "search", "/search", fmt.Sprintf("Search: %v", searchQuery), notfoundTemplateFiles)
	if handleTemplateError(w, r, "slot.go", "Slot", "blockSlot", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}
