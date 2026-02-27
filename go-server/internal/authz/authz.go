// Package authz implements the principal authorization layer for DCT operations
// (roadmap item 13b). It enforces who can mint, delegate, revoke, and introspect
// Delegation Capability Tokens based on escrow roles and trust context, with a
// default-deny policy and audited decisions (paper §4.7, §6.1).
package authz

import (
	"context"
	"errors"
	"strings"
)

// Operation identifies a DCT action subject to authorization.
type Operation string

const (
	OpMint       Operation = "mint"
	OpDelegate   Operation = "delegate"
	OpRevoke     Operation = "revoke"
	OpIntrospect Operation = "introspect"
)

// Sentinel errors returned by the authorization layer.
var (
	ErrUnauthorized     = errors.New("authorization denied")
	ErrNotAuthenticated = errors.New("caller not authenticated")
)

// DenyReason is a machine-readable code explaining why an operation was denied.
type DenyReason string

const (
	ReasonAllowed               DenyReason = "allowed"
	ReasonNotAuthenticated      DenyReason = "caller_not_authenticated"
	ReasonCallerFrozen          DenyReason = "caller_frozen"
	ReasonEscrowFrozen          DenyReason = "escrow_frozen"
	ReasonEscrowTerminal        DenyReason = "escrow_terminal"
	ReasonCallerNotBuyer        DenyReason = "caller_not_buyer"
	ReasonCallerNotTokenHolder  DenyReason = "caller_not_token_holder"
	ReasonNotAuthorizedToRevoke DenyReason = "caller_not_authorized_to_revoke"
	ReasonEmergencyOverride     DenyReason = "emergency_override"
)

// Role describes a principal's relationship to an escrow.
type Role string

const (
	RoleBuyer      Role = "buyer"
	RoleWorker     Role = "worker"
	RoleVerifier   Role = "verifier"
	RoleArbitrator Role = "arbitrator"
	RoleBackup     Role = "backup"
	RoleNone       Role = "none"
)

// Principal represents an authenticated caller.
type Principal struct {
	Address       string
	Authenticated bool
	Frozen        bool
}

// EscrowContext holds the escrow state relevant to authorization decisions.
type EscrowContext struct {
	EscrowID int64
	Buyer    string
	Worker   string
	Verifier string
	Status   string
	Frozen   bool
}

// TokenContext holds token state relevant to authorization decisions.
type TokenContext struct {
	TokenID  string
	Issuer   string
	Subject  string
	EscrowID int64
}

// Result captures an authorization decision with its reason.
type Result struct {
	Allowed bool
	Reason  DenyReason
}

func Allow() Result            { return Result{Allowed: true, Reason: ReasonAllowed} }
func Deny(r DenyReason) Result { return Result{Allowed: false, Reason: r} }

// callerKey is the context key for storing caller identity.
type callerKey struct{}

// WithCaller attaches a Principal to the context.
func WithCaller(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, callerKey{}, p)
}

// CallerFrom extracts the Principal from context, returning an unauthenticated
// principal if none is set.
func CallerFrom(ctx context.Context) Principal {
	if p, ok := ctx.Value(callerKey{}).(Principal); ok {
		return p
	}
	return Principal{}
}

// requestIDKey is the context key for request correlation IDs.
type requestIDKey struct{}

// WithRequestID attaches a request ID to the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFrom extracts the request ID from context.
func RequestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// Authorize evaluates whether the given principal may perform op on the
// specified escrow/token. It implements a default-deny policy: every path
// that does not explicitly allow the operation returns a deny result.
func Authorize(op Operation, principal Principal, escrow *EscrowContext, token *TokenContext) Result {
	if !principal.Authenticated {
		return Deny(ReasonNotAuthenticated)
	}

	if principal.Frozen {
		return Deny(ReasonCallerFrozen)
	}

	// Introspect is public — no further checks needed.
	if op == OpIntrospect {
		return Allow()
	}

	// Escrow state checks apply to mint, delegate, and revoke.
	if escrow != nil {
		if escrow.Frozen {
			return Deny(ReasonEscrowFrozen)
		}
		if isTerminal(escrow.Status) {
			return Deny(ReasonEscrowTerminal)
		}
	}

	switch op {
	case OpMint:
		if escrow == nil || !addressEq(principal.Address, escrow.Buyer) {
			return Deny(ReasonCallerNotBuyer)
		}
		return Allow()

	case OpDelegate:
		if token == nil || !addressEq(principal.Address, token.Subject) {
			return Deny(ReasonCallerNotTokenHolder)
		}
		return Allow()

	case OpRevoke:
		if token == nil {
			return Deny(ReasonNotAuthorizedToRevoke)
		}
		// Token issuer can revoke their own tokens.
		if addressEq(principal.Address, token.Issuer) {
			return Allow()
		}
		// Buyer can revoke any token scoped to their escrow.
		if escrow != nil && addressEq(principal.Address, escrow.Buyer) {
			return Allow()
		}
		return Deny(ReasonNotAuthorizedToRevoke)

	default:
		return Deny(ReasonNotAuthenticated)
	}
}

// ResolveRole determines the caller's role relative to an escrow.
func ResolveRole(address string, escrow *EscrowContext) Role {
	if escrow == nil {
		return RoleNone
	}
	addr := strings.ToLower(strings.TrimSpace(address))
	switch {
	case addressEq(addr, escrow.Buyer):
		return RoleBuyer
	case addressEq(addr, escrow.Worker):
		return RoleWorker
	case addressEq(addr, escrow.Verifier):
		return RoleVerifier
	default:
		return RoleNone
	}
}

func isTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "settled", "refunded", "cancelled", "resolved":
		return true
	default:
		return false
	}
}

func addressEq(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
