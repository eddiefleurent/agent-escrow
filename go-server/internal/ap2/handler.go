package ap2

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

const maxMandateBody = 1 << 20

// Handler provides HTTP endpoints for the AP2 mandate-to-escrow bridge.
type Handler struct {
	svc *Service
}

// NewHandler creates a new AP2 HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// FundViaMandate handles POST /api/v1/ap2/fund.
func (h *Handler) FundViaMandate(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil || !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	req, err := decodeFundRequest(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	resp, err := h.svc.FundViaMandate(r.Context(), req.EscrowID, req.MandateEnvelope)
	if err != nil {
		if errors.Is(err, ErrInvalidMandate) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid mandate or escrow binding"})
			return
		}
		if errors.Is(err, ErrFundingPending) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		slog.Error("fund via mandate failed", "escrow_id", req.EscrowID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// ValidateMandate handles POST /api/v1/ap2/validate.
func (h *Handler) ValidateMandate(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil || !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	req, err := decodeValidateRequest(body)
	if err != nil {
		writeJSON(w, http.StatusOK, ValidateMandateResponse{Valid: false, Reason: err.Error()})
		return
	}

	err = h.svc.ValidateMandate(r.Context(), req.MandateEnvelope)
	if err != nil {
		writeJSON(w, http.StatusOK, ValidateMandateResponse{Valid: false, Reason: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, ValidateMandateResponse{Valid: true})
}

// GetMandate handles GET /api/v1/ap2/mandates/{id}.
func (h *Handler) GetMandate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mandate id is required"})
		return
	}

	mandate, err := h.svc.DB.GetAP2Mandate(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "mandate not found"})
		return
	}

	writeJSON(w, http.StatusOK, mandate)
}

func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, maxMandateBody))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode failed", "status", status, "error", err)
	}
}
