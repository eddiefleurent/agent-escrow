package ucp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

type Handler struct {
	Service *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{Service: svc}
}

func (h *Handler) WellKnown(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, h.Service.BuildWellKnownProfile())
}

func (h *Handler) CreateCheckout(w http.ResponseWriter, r *http.Request) {
	var req CreateCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	resp, err := h.Service.CreateCheckout(r.Context(), req)
	if err != nil {
		h.writeUCPError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) GetCheckout(w http.ResponseWriter, r *http.Request) {
	checkoutID := strings.TrimSpace(r.PathValue("checkout_id"))
	if checkoutID == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing checkout_id"})
		return
	}
	resp, err := h.Service.GetCheckout(r.Context(), checkoutID)
	if err != nil {
		h.writeUCPError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateCheckout(w http.ResponseWriter, r *http.Request) {
	checkoutID := strings.TrimSpace(r.PathValue("checkout_id"))
	if checkoutID == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing checkout_id"})
		return
	}
	var req UpdateCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	resp, err := h.Service.UpdateCheckout(r.Context(), checkoutID, req)
	if err != nil {
		h.writeUCPError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CompleteCheckout(w http.ResponseWriter, r *http.Request) {
	checkoutID := strings.TrimSpace(r.PathValue("checkout_id"))
	if checkoutID == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing checkout_id"})
		return
	}
	var req CompleteCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	resp, err := h.Service.CompleteCheckout(r.Context(), checkoutID, req)
	if err != nil {
		h.writeUCPError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CancelCheckout(w http.ResponseWriter, r *http.Request) {
	checkoutID := strings.TrimSpace(r.PathValue("checkout_id"))
	if checkoutID == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing checkout_id"})
		return
	}
	var req CancelCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	resp, err := h.Service.CancelCheckout(r.Context(), checkoutID, req)
	if err != nil {
		h.writeUCPError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) writeUCPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrIdempotencyConflict):
		h.writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, sql.ErrNoRows):
		h.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		slog.Error("ucp internal error", "error", err)
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
	}
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
