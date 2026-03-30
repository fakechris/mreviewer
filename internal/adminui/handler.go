package adminui

import (
	_ "embed"
	"net/http"

	"github.com/mreviewer/mreviewer/internal/adminapi"
)

//go:embed template.html
var adminHTML []byte

func NewHandler(token string) http.Handler {
	return adminapi.Protect(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(adminHTML)
	}))
}
