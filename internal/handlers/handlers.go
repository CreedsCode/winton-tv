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
	h.render(w, "index.html", nil)
}

// Login is a placeholder until feat/v1.2 wires up Discord OAuth.
// It renders a "coming soon" page so the landing page CTA has a destination.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	h.render(w, "login.html", nil)
}

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		h.logger.Error("template render", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
