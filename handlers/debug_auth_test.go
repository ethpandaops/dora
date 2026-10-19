package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

func TestDebugAuth(t *testing.T) {
	prevConfig := utils.Config
	t.Cleanup(func() { utils.Config = prevConfig })

	utils.Config = &types.Config{}
	utils.Config.Frontend.PprofKey = "s3cret key+/"

	served := false
	handler := DebugAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		header     string
		cookie     string
		wantStatus int
	}{
		{name: "no key", wantStatus: http.StatusUnauthorized},
		{name: "wrong bearer", header: "Bearer nope", wantStatus: http.StatusUnauthorized},
		{name: "valid bearer", header: "Bearer s3cret key+/", wantStatus: http.StatusOK},
		{name: "wrong cookie", cookie: "nope", wantStatus: http.StatusUnauthorized},
		{name: "valid encoded cookie", cookie: "s3cret%20key%2B%2F", wantStatus: http.StatusOK},
		{name: "bearer wins over cookie", header: "Bearer nope", cookie: "s3cret%20key%2B%2F", wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			served = false
			req := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: debugKeyCookie, Value: tt.cookie})
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantStatus == http.StatusOK, served)
			if tt.wantStatus == http.StatusUnauthorized {
				assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
				assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			}
		})
	}

	t.Run("empty configured key never matches", func(t *testing.T) {
		utils.Config.Frontend.PprofKey = ""
		served = false
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
		req.Header.Set("Authorization", "Bearer ")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.False(t, served)
	})
}
