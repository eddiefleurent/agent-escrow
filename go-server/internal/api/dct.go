package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/dct"
)

type mintDCTRequest struct {
	EscrowID   int64    `json:"escrow_id"`
	Subject    string   `json:"subject"`
	Issuer     string   `json:"issuer,omitempty"`
	Operations []string `json:"operations"`
	Resources  []string `json:"resources"`
	ExpiresAt  int64    `json:"expires_at"`
}

type delegateDCTRequest struct {
	ParentToken string   `json:"parent_token"`
	Subject     string   `json:"subject"`
	Issuer      string   `json:"issuer,omitempty"`
	Operations  []string `json:"operations"`
	Resources   []string `json:"resources"`
	ExpiresAt   int64    `json:"expires_at"`
}

type introspectDCTRequest struct {
	Token string `json:"token"`
}

type revokeDCTRequest struct {
	TokenID string `json:"token_id"`
	Reason  string `json:"reason,omitempty"`
	By      string `json:"by,omitempty"`
}

func (h *Handlers) dctService() *dct.Service { return &dct.Service{DB: h.db} }

func (h *Handlers) MintDCT(w http.ResponseWriter, r *http.Request) {
	var req mintDCTRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	rec, token, err := h.dctService().Mint(r.Context(), dct.MintParams{
		EscrowID: req.EscrowID, Subject: req.Subject, Issuer: req.Issuer,
		Operations: req.Operations, Resources: req.Resources, ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "record": rec})
}

func (h *Handlers) DelegateDCT(w http.ResponseWriter, r *http.Request) {
	var req delegateDCTRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	rec, token, err := h.dctService().Delegate(r.Context(), dct.DelegateParams{
		ParentToken: req.ParentToken, Subject: req.Subject, Issuer: req.Issuer,
		Operations: req.Operations, Resources: req.Resources, ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "record": rec})
}

func (h *Handlers) IntrospectDCT(w http.ResponseWriter, r *http.Request) {
	var req introspectDCTRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	rec, active, reasons, err := h.dctService().Introspect(r.Context(), req.Token)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, dct.Introspection{Token: rec, Active: active, Reasons: reasons})
}

func (h *Handlers) RevokeDCT(w http.ResponseWriter, r *http.Request) {
	var req revokeDCTRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if err := h.dctService().Revoke(r.Context(), dct.RevokeParams(req)); err != nil {
		code := http.StatusBadRequest
		if err == sql.ErrNoRows {
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked", "token_id": req.TokenID})
}

func (h *Handlers) ListEscrowDCTs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if _, err := h.db.GetEscrow(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	tokens, err := h.db.ListDCTTokensByEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, tok := range tokens {
		tok.TokenHash = ""
		tok.Subject = strings.ToLower(tok.Subject)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens, "count": len(tokens)})
}
