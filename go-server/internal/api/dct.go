package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/authz"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/dct"
)

type mintDCTRequest struct {
	EscrowID   int64    `json:"escrow_id"`
	Subject    string   `json:"subject"`
	Issuer     string   `json:"issuer,omitempty"`
	Operations []string `json:"operations"`
	Resources  []string `json:"resources"`
	ExpiresAt  int64    `json:"expires_at"`
	Caller     string   `json:"caller"`
}

type delegateDCTRequest struct {
	ParentToken string   `json:"parent_token"`
	Subject     string   `json:"subject"`
	Issuer      string   `json:"issuer,omitempty"`
	Operations  []string `json:"operations"`
	Resources   []string `json:"resources"`
	ExpiresAt   int64    `json:"expires_at"`
	Caller      string   `json:"caller"`
}

type introspectDCTRequest struct {
	Token string `json:"token"`
}

type revokeDCTRequest struct {
	TokenID string `json:"token_id"`
	Reason  string `json:"reason,omitempty"`
	By      string `json:"by,omitempty"`
	Caller  string `json:"caller"`
}

type emergencyOverrideDCTRequest struct {
	EscrowID      int64  `json:"escrow_id"`
	Operation     string `json:"operation"`
	CallerAddress string `json:"caller_address"`
	Reason        string `json:"reason"`
	Owner         string `json:"owner"`
}

func (h *Handlers) dctService() *dct.Service {
	return &dct.Service{
		DB:           h.db,
		Audit:        &authz.SQLiteAuditStore{DB: h.db.SQLDB()},
		FactoryOwner: h.cfg.OwnerAddress,
	}
}

func mapDCTError(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	switch {
	case errors.Is(err, dct.ErrUnauthorized):
		return http.StatusForbidden, err.Error()
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

func mapEmergencyOverrideError(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	switch {
	case errors.Is(err, dct.ErrInternal):
		return http.StatusInternalServerError, "internal error"
	case errors.Is(err, dct.ErrUnauthorized):
		return http.StatusForbidden, err.Error()
	case errors.Is(err, sql.ErrNoRows),
		strings.Contains(err.Error(), "owner address is required"),
		strings.Contains(err.Error(), "override reason is required"),
		strings.Contains(err.Error(), "unsupported override operation"):
		return http.StatusBadRequest, err.Error()
	default:
		return http.StatusInternalServerError, "internal error"
	}
}

// httpCallerCtx attaches an authenticated caller principal to the request context.
func httpCallerCtx(r *http.Request, callerAddr string) *http.Request {
	callerAddr = strings.TrimSpace(callerAddr)
	p := authz.Principal{
		Address:       strings.ToLower(callerAddr),
		Authenticated: callerAddr != "",
	}
	ctx := authz.WithCaller(r.Context(), p)
	return r.WithContext(ctx)
}

func (h *Handlers) MintDCT(w http.ResponseWriter, r *http.Request) {
	var req mintDCTRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	r = httpCallerCtx(r, req.Caller)
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
	r = httpCallerCtx(r, req.Caller)
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
	r = httpCallerCtx(r, req.Caller)
	if err := h.dctService().Revoke(r.Context(), dct.RevokeParams{TokenID: req.TokenID, Reason: req.Reason, By: req.By}); err != nil {
		code, msg := mapDCTError(err)
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked", "token_id": req.TokenID})
}

func (h *Handlers) EmergencyOverrideDCT(w http.ResponseWriter, r *http.Request) {
	var req emergencyOverrideDCTRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	r = httpCallerCtx(r, req.Owner)
	if err := h.dctService().EmergencyOverride(r.Context(), dct.EmergencyOverrideParams{
		EscrowID:      req.EscrowID,
		Operation:     req.Operation,
		CallerAddress: req.CallerAddress,
		Reason:        req.Reason,
		OwnerAddress:  req.Owner,
	}); err != nil {
		code, msg := mapEmergencyOverrideError(err)
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "override_applied", "escrow_id": req.EscrowID})
}

func (h *Handlers) ListDCTAudit(w http.ResponseWriter, r *http.Request) {
	var escrowID int64
	if v := r.URL.Query().Get("escrow_id"); v != "" {
		var err error
		escrowID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid escrow_id"})
			return
		}
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	audit := &authz.SQLiteAuditStore{DB: h.db.SQLDB()}
	records, err := audit.ListAuthzAudit(r.Context(), escrowID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_entries": records, "count": len(records)})
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
