package a2a

// A2A protocol types following the v0.2+ specification (JSON-RPC 2.0 over HTTP).
// Paper section 6 (pages 25-27): extends A2A Task objects with verification_policy
// and escrow_trigger fields to bridge A2A task lifecycle to on-chain escrow.

// AgentCard is served at GET /.well-known/agent.json for agent discovery.
type AgentCard struct {
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	URL                string         `json:"url"`
	Version            string         `json:"version"`
	Provider           *Provider      `json:"provider,omitempty"`
	Capabilities       Capabilities   `json:"capabilities"`
	Authentication     Authentication `json:"authentication"`
	DefaultInputModes  []string       `json:"defaultInputModes"`
	DefaultOutputModes []string       `json:"defaultOutputModes"`
	Skills             []Skill        `json:"skills"`
}

type Provider struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

type Capabilities struct {
	Streaming            bool `json:"streaming"`
	PushNotifications    bool `json:"pushNotifications"`
	StateTransitionHooks bool `json:"stateTransitionHooks"`
}

type Authentication struct {
	Schemes []string `json:"schemes"`
}

type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

// VerificationPolicy defines how task completion is verified (paper §6.1, page 26).
type VerificationPolicy struct {
	Mode          string                 `json:"mode"`
	Artifacts     []VerificationArtifact `json:"artifacts,omitempty"`
	EscrowTrigger bool                   `json:"escrow_trigger"`
}

type VerificationArtifact struct {
	Type              string `json:"type"`
	Validator         string `json:"validator,omitempty"`
	SignatureRequired bool   `json:"signature_required,omitempty"`
	CircuitHash       string `json:"circuit_hash,omitempty"`
	ProofProtocol     string `json:"proof_protocol,omitempty"`
}

// JSON-RPC 2.0 envelope types for the POST /a2a endpoint.

type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// A2A Task types representing the task lifecycle.

type TaskStatus string

const (
	TaskStatusSubmitted     TaskStatus = "submitted"
	TaskStatusWorking       TaskStatus = "working"
	TaskStatusInputRequired TaskStatus = "input-required"
	TaskStatusCompleted     TaskStatus = "completed"
	TaskStatusFailed        TaskStatus = "failed"
	TaskStatusCanceled      TaskStatus = "canceled"
)

type Task struct {
	ID        string            `json:"id"`
	SessionID string            `json:"sessionId"`
	Status    TaskSummary       `json:"status"`
	Artifacts []Artifact        `json:"artifacts,omitempty"`
	History   []Message         `json:"history,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type TaskSummary struct {
	State   TaskStatus `json:"state"`
	Message *Message   `json:"message,omitempty"`
}

type Message struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     any    `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type Artifact struct {
	Name     string `json:"name,omitempty"`
	Parts    []Part `json:"parts"`
	Index    int    `json:"index"`
	Append   bool   `json:"append,omitempty"`
	LastPart bool   `json:"lastChunk,omitempty"`
}

// TaskSendParams is the params object for tasks/send.
type TaskSendParams struct {
	ID        string            `json:"id"`
	SessionID string            `json:"sessionId,omitempty"`
	Message   Message           `json:"message"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// TaskQueryParams is the params object for tasks/get and tasks/cancel.
type TaskQueryParams struct {
	ID string `json:"id"`
}

// Standard JSON-RPC error codes.
const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603
	ErrCodeTaskNotFound   = -32001
)
