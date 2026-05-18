// Package handlers wires HTTP handlers and HTML templates.
package handlers

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/CreedsCode/winton-tv/internal/config"
)

type Handler struct {
	cfg    *config.Config
	logger *slog.Logger
	tmpl   *template.Template
}

func New(cfg *config.Config, logger *slog.Logger) (*Handler, error) {
	tmpl, err := template.ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Handler{cfg: cfg, logger: logger, tmpl: tmpl}, nil
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	if err := h.tmpl.ExecuteTemplate(w, "index.html", nil); err != nil {
		h.logger.Error("render index", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
