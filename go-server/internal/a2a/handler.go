package a2a

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
)

// Handler provides HTTP handlers for the A2A settlement adapter.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ServeAgentCard handles GET /.well-known/agent.json.
func (h *Handler) ServeAgentCard(w http.ResponseWriter, r *http.Request) {
	card := h.svc.BuildAgentCard()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(card); err != nil {
		slog.Error("failed to encode agent card", "error", err)
	}
}

// HandleJSONRPC handles POST /a2a, dispatching JSON-RPC 2.0 methods.
func (h *Handler) HandleJSONRPC(w http.ResponseWriter, r *http.Request) {
	mediatype, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediatype != "application/json" {
		writeJSONRPCError(w, nil, ErrCodeInvalidRequest, "Content-Type must be application/json")
		return
	}

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPCError(w, nil, ErrCodeParse, "failed to parse JSON-RPC request")
		return
	}

	if req.JSONRPC != "2.0" {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	switch req.Method {
	case "tasks/send":
		h.handleTasksSend(w, req)
	case "tasks/get":
		h.handleTasksGet(w, req)
	case "tasks/cancel":
		h.handleTasksCancel(w, req)
	default:
		writeJSONRPCError(w, req.ID, ErrCodeMethodNotFound, "method not found: "+req.Method)
	}
}

func (h *Handler) handleTasksSend(w http.ResponseWriter, req JSONRPCRequest) {
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "invalid params")
		return
	}

	var params TaskSendParams
	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "invalid params: "+err.Error())
		return
	}

	if len(params.Message.Parts) == 0 {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "message must have at least one part")
		return
	}

	task, err := h.svc.HandleTaskSend(params)
	if err != nil {
		if errors.Is(err, ErrInvalidParams) {
			writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, err.Error())
		} else {
			writeJSONRPCError(w, req.ID, ErrCodeInternal, err.Error())
		}
		return
	}

	writeJSONRPCResult(w, req.ID, task)
}

func (h *Handler) handleTasksGet(w http.ResponseWriter, req JSONRPCRequest) {
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "invalid params")
		return
	}

	var params TaskQueryParams
	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "invalid params: "+err.Error())
		return
	}

	if params.ID == "" {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "id is required")
		return
	}

	task, err := h.svc.GetTaskStatus(params.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONRPCError(w, req.ID, ErrCodeTaskNotFound, err.Error())
		} else {
			writeJSONRPCError(w, req.ID, ErrCodeInternal, err.Error())
		}
		return
	}

	writeJSONRPCResult(w, req.ID, task)
}

func (h *Handler) handleTasksCancel(w http.ResponseWriter, req JSONRPCRequest) {
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "invalid params")
		return
	}

	var params TaskQueryParams
	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "invalid params: "+err.Error())
		return
	}

	if params.ID == "" {
		writeJSONRPCError(w, req.ID, ErrCodeInvalidParams, "id is required")
		return
	}

	task, err := h.svc.CancelTask(params.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONRPCError(w, req.ID, ErrCodeTaskNotFound, err.Error())
		} else {
			writeJSONRPCError(w, req.ID, ErrCodeInternal, err.Error())
		}
		return
	}

	writeJSONRPCResult(w, req.ID, task)
}

func writeJSONRPCResult(w http.ResponseWriter, id any, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode JSON-RPC response", "error", err)
	}
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode JSON-RPC error response", "error", err)
	}
}
