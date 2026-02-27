package authz

import (
	"context"
	"testing"
)

func TestAuthorize_Mint_ByBuyer(t *testing.T) {
	p := Principal{Address: "0xbuyer", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	r := Authorize(OpMint, p, e, nil)
	if !r.Allowed {
		t.Fatalf("expected allowed, got denied: %s", r.Reason)
	}
}

func TestAuthorize_Mint_ByWorker(t *testing.T) {
	p := Principal{Address: "0xworker", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Worker: "0xworker", Status: "funded"}
	r := Authorize(OpMint, p, e, nil)
	if r.Allowed {
		t.Fatal("expected denied for worker minting")
	}
	if r.Reason != ReasonCallerNotBuyer {
		t.Fatalf("expected caller_not_buyer, got %s", r.Reason)
	}
}

func TestAuthorize_Mint_Unauthenticated(t *testing.T) {
	p := Principal{}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	r := Authorize(OpMint, p, e, nil)
	if r.Allowed {
		t.Fatal("expected denied for unauthenticated caller")
	}
	if r.Reason != ReasonNotAuthenticated {
		t.Fatalf("expected caller_not_authenticated, got %s", r.Reason)
	}
}

func TestAuthorize_Mint_CallerFrozen(t *testing.T) {
	p := Principal{Address: "0xbuyer", Authenticated: true, Frozen: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	r := Authorize(OpMint, p, e, nil)
	if r.Allowed {
		t.Fatal("expected denied for frozen caller")
	}
	if r.Reason != ReasonCallerFrozen {
		t.Fatalf("expected caller_frozen, got %s", r.Reason)
	}
}

func TestAuthorize_Mint_EscrowFrozen(t *testing.T) {
	p := Principal{Address: "0xbuyer", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded", Frozen: true}
	r := Authorize(OpMint, p, e, nil)
	if r.Allowed {
		t.Fatal("expected denied for frozen escrow")
	}
	if r.Reason != ReasonEscrowFrozen {
		t.Fatalf("expected escrow_frozen, got %s", r.Reason)
	}
}

func TestAuthorize_Mint_EscrowTerminal(t *testing.T) {
	for _, status := range []string{"settled", "refunded", "cancelled", "resolved"} {
		p := Principal{Address: "0xbuyer", Authenticated: true}
		e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: status}
		r := Authorize(OpMint, p, e, nil)
		if r.Allowed {
			t.Fatalf("expected denied for terminal escrow status %q", status)
		}
		if r.Reason != ReasonEscrowTerminal {
			t.Fatalf("expected escrow_terminal for status %q, got %s", status, r.Reason)
		}
	}
}

func TestAuthorize_Delegate_ByTokenHolder(t *testing.T) {
	p := Principal{Address: "0xholder", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	tok := &TokenContext{TokenID: "dct_abc", Subject: "0xholder", Issuer: "0xbuyer", EscrowID: 1}
	r := Authorize(OpDelegate, p, e, tok)
	if !r.Allowed {
		t.Fatalf("expected allowed, got denied: %s", r.Reason)
	}
}

func TestAuthorize_Delegate_ByNonHolder(t *testing.T) {
	p := Principal{Address: "0xother", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	tok := &TokenContext{TokenID: "dct_abc", Subject: "0xholder", Issuer: "0xbuyer", EscrowID: 1}
	r := Authorize(OpDelegate, p, e, tok)
	if r.Allowed {
		t.Fatal("expected denied for non-holder")
	}
	if r.Reason != ReasonCallerNotTokenHolder {
		t.Fatalf("expected caller_not_token_holder, got %s", r.Reason)
	}
}

func TestAuthorize_Revoke_OwnToken(t *testing.T) {
	p := Principal{Address: "0xissuer", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	tok := &TokenContext{TokenID: "dct_abc", Subject: "0xholder", Issuer: "0xissuer", EscrowID: 1}
	r := Authorize(OpRevoke, p, e, tok)
	if !r.Allowed {
		t.Fatalf("expected allowed for issuer revoking own token, got: %s", r.Reason)
	}
}

func TestAuthorize_Revoke_EscrowWide(t *testing.T) {
	p := Principal{Address: "0xbuyer", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	tok := &TokenContext{TokenID: "dct_abc", Subject: "0xholder", Issuer: "0xissuer", EscrowID: 1}
	r := Authorize(OpRevoke, p, e, tok)
	if !r.Allowed {
		t.Fatalf("expected allowed for buyer revoking escrow token, got: %s", r.Reason)
	}
}

func TestAuthorize_Revoke_Unauthorized(t *testing.T) {
	p := Principal{Address: "0xrandom", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	tok := &TokenContext{TokenID: "dct_abc", Subject: "0xholder", Issuer: "0xissuer", EscrowID: 1}
	r := Authorize(OpRevoke, p, e, tok)
	if r.Allowed {
		t.Fatal("expected denied for unauthorized revoker")
	}
	if r.Reason != ReasonNotAuthorizedToRevoke {
		t.Fatalf("expected caller_not_authorized_to_revoke, got %s", r.Reason)
	}
}

func TestAuthorize_Introspect_Public(t *testing.T) {
	// Even unauthenticated callers cannot introspect (authentication is still required).
	// But any authenticated caller can introspect.
	p := Principal{Address: "0xanyone", Authenticated: true}
	r := Authorize(OpIntrospect, p, nil, nil)
	if !r.Allowed {
		t.Fatalf("expected introspect allowed for any authenticated caller, got: %s", r.Reason)
	}
}

func TestAuthorize_Introspect_Unauthenticated(t *testing.T) {
	p := Principal{}
	r := Authorize(OpIntrospect, p, nil, nil)
	if r.Allowed {
		t.Fatal("expected denied for unauthenticated introspect")
	}
}

func TestAuthorize_Mint_CaseInsensitive(t *testing.T) {
	p := Principal{Address: "0xBuYeR", Authenticated: true}
	e := &EscrowContext{EscrowID: 1, Buyer: "0xbuyer", Status: "funded"}
	r := Authorize(OpMint, p, e, nil)
	if !r.Allowed {
		t.Fatalf("expected case-insensitive match, got denied: %s", r.Reason)
	}
}

func TestResolveRole(t *testing.T) {
	e := &EscrowContext{Buyer: "0xb", Worker: "0xw", Verifier: "0xv"}
	tests := []struct {
		addr string
		want Role
	}{
		{"0xb", RoleBuyer},
		{"0xw", RoleWorker},
		{"0xv", RoleVerifier},
		{"0xother", RoleNone},
	}
	for _, tt := range tests {
		got := ResolveRole(tt.addr, e)
		if got != tt.want {
			t.Errorf("ResolveRole(%q) = %s, want %s", tt.addr, got, tt.want)
		}
	}
}

func TestWithCaller_RoundTrip(t *testing.T) {
	ctx := context.Background()
	p := Principal{Address: "0xtest", Authenticated: true}
	ctx = WithCaller(ctx, p)
	got := CallerFrom(ctx)
	if got.Address != "0xtest" || !got.Authenticated {
		t.Fatalf("caller round-trip failed: %+v", got)
	}
}

func TestCallerFrom_Empty(t *testing.T) {
	ctx := context.Background()
	got := CallerFrom(ctx)
	if got.Authenticated {
		t.Fatal("expected unauthenticated from empty context")
	}
}

func TestWithRequestID_RoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "req-123")
	if got := RequestIDFrom(ctx); got != "req-123" {
		t.Fatalf("expected req-123, got %s", got)
	}
}
