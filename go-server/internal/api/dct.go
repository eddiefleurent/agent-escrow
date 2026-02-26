package api

import (
	"database/sql"
	"encoding/json"
	"errors"
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

func mapDCTError(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "not found"
	case errors.Is(err, dct.ErrInvalidAttenuation),
		errors.Is(err, dct.ErrExpiredToken),
		errors.Is(err, dct.ErrInvalidProfile),
		errors.Is(err, dct.ErrInvalidChain),
		errors.Is(err, dct.ErrInactiveEscrow),
		strings.Contains(err.Error(), "invalid token format"),
		strings.Contains(err.Error(), "token verification failed"),
		strings.Contains(err.Error(), "subject is required"),
		strings.Contains(err.Error(), "operations/resources must be non-empty"),
		strings.Contains(err.Error(), "escrow_id must be > 0"),
		strings.Contains(err.Error(), "expires_at must be"),
		strings.Contains(err.Error(), "operations must be non-empty"),
		strings.Contains(err.Error(), "resources must be non-empty"):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, dct.ErrRevokedToken):
		return http.StatusUnauthorized, "token is inactive"
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

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
		code, msg := mapDCTError(err)
		writeJSON(w, code, map[string]string{"error": msg})
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
		code, msg := mapDCTError(err)
		writeJSON(w, code, map[string]string{"error": msg})
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
		code, msg := mapDCTError(err)
		writeJSON(w, code, map[string]string{"error": msg})
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
		code, msg := mapDCTError(err)
		writeJSON(w, code, map[string]string{"error": msg})
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
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		return
	}
	tokens, err := h.db.ListDCTTokensByEscrow(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	for i := range tokens {
		tokens[i].TokenHash = ""
		tokens[i].Subject = strings.ToLower(tokens[i].Subject)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens, "count": len(tokens)})
}
