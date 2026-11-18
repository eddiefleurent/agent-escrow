package ap2

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eddiefleurent/agent-escrow/go-server/internal/chain"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/config"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/indexer"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/storage"
	"github.com/eddiefleurent/agent-escrow/go-server/internal/x402"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	testFactory    = "0x1234567890123456789012345678901234567890"
	testEscrowAddr = "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD"
	testToken      = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
	testChainID    = 84532
)

// testDomainSeparator is the token DOMAIN_SEPARATOR the mock returns. Tests
// sign ReceiveWithAuthorization against this exact value.
var testDomainSeparator = crypto.Keccak256Hash([]byte("agent-escrow-test-domain-separator"))

// testNow is a fixed clock so validity-window tests are deterministic.
var testNow = time.Unix(1_800_000_000, 0)

func setupTestService(t *testing.T) (*Service, *storage.DB, *chain.MockClient) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mc := chain.NewMockClient()
	mc.DomainSeparatorHash = testDomainSeparator
	mc.Receipt = &types.Receipt{Status: types.ReceiptStatusSuccessful}
	cfg := &config.Config{
		ChainID:        testChainID,
		FactoryAddress: testFactory,
		Port:           8080,
		X402Enabled:    true,
	}
	idx := indexer.New(db, mc, testFactory)

	svc := &Service{
		DB:    db,
		Chain: mc,
		Idx:   idx,
		Cfg:   cfg,
		X402:  x402.NewClient("http://localhost:9999"),
		Now:   func() time.Time { return testNow },
	}
	return svc, db, mc
}

type testSigner struct {
	key  *ecdsa.PrivateKey
	addr common.Address
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return testSigner{key: key, addr: crypto.PubkeyToAddress(key.PublicKey)}
}

func randomBytes32Hex(t *testing.T) string {
	t.Helper()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hexutil.Encode(b[:])
}

func testAuthorization(t *testing.T, from common.Address, to, value string) EIP3009Authorization {
	t.Helper()
	return EIP3009Authorization{
		Token:       testToken,
		From:        from.Hex(),
		To:          to,
		Value:       value,
		ValidAfter:  "0",
		ValidBefore: strconv.FormatInt(testNow.Add(time.Hour).Unix(), 10),
		Nonce:       randomBytes32Hex(t),
		V:           27,
		R:           "0x0000000000000000000000000000000000000000000000000000000000000002",
		S:           "0x0000000000000000000000000000000000000000000000000000000000000003",
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// sign computes the EIP-712 digest for env under svc's domain and signs it with key,
// producing a [r || s || v] signature with v in {27,28} as wallets do.
func sign(t *testing.T, svc *Service, key *ecdsa.PrivateKey, env MandateEnvelope) MandateEnvelope {
	t.Helper()
	vm, err := svc.decodeMandate(env)
	if err != nil {
		t.Fatalf("decode mandate for signing: %v", err)
	}
	authSig, err := signReceiveAuthorization(key, vm.Auth)
	if err != nil {
		t.Fatalf("sign authorization: %v", err)
	}
	env.Authorization.V = authSig[64]
	env.Authorization.R = hexutil.Encode(authSig[:32])
	env.Authorization.S = hexutil.Encode(authSig[32:64])
	sig, err := crypto.Sign(vm.Digest.Bytes(), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig[64] += 27
	env.Signature = hexutil.Encode(sig)
	return env
}

func signReceiveAuthorization(key *ecdsa.PrivateKey, auth parsedAuthorization) ([]byte, error) {
	digest, err := receiveWithAuthorizationDigest(testDomainSeparator, auth)
	if err != nil {
		return nil, err
	}
	sig, err := crypto.Sign(digest.Bytes(), key)
	if err != nil {
		return nil, err
	}
	sig[64] += 27
	return sig, nil
}

func paymentEnvelope(t *testing.T, signer common.Address, to, value string) MandateEnvelope {
	t.Helper()
	auth := testAuthorization(t, signer, to, value)
	return MandateEnvelope{
		Type: MandateTypePayment,
		Payload: mustJSON(t, PaymentMandate{
			Signer:    signer.Hex(),
			Amount:    value,
			Currency:  "USDC",
			Recipient: to,
			Nonce:     auth.Nonce,
		}),
		SignerAddress: signer.Hex(),
		Authorization: auth,
	}
}

func signedPayment(t *testing.T, svc *Service, s testSigner, to, value string) MandateEnvelope {
	t.Helper()
	return sign(t, svc, s.key, paymentEnvelope(t, s.addr, to, value))
}

func intentEnvelope(t *testing.T, signer common.Address, budget, value string, issuedAt, ttl int64) MandateEnvelope {
	t.Helper()
	return MandateEnvelope{
		Type: MandateTypeIntent,
		Payload: mustJSON(t, IntentMandate{
			Signer:         signer.Hex(),
			BudgetAmount:   budget,
			BudgetCurrency: "USDC",
			TTLSeconds:     ttl,
			IssuedAt:       issuedAt,
			Description:    "research task budget",
		}),
		SignerAddress: signer.Hex(),
		Authorization: testAuthorization(t, signer, testEscrowAddr, value),
	}
}

func cartEnvelope(t *testing.T, signer common.Address, amount string, issuedAt, ttl int64) MandateEnvelope {
	t.Helper()
	return MandateEnvelope{
		Type: MandateTypeCart,
		Payload: mustJSON(t, CartMandate{
			Signer:     signer.Hex(),
			Merchant:   "0x5555555555555555555555555555555555555555",
			Amount:     amount,
			Currency:   "USDC",
			ItemsHash:  randomBytes32Hex(t),
			TTLSeconds: ttl,
			IssuedAt:   issuedAt,
		}),
		SignerAddress: signer.Hex(),
		Authorization: testAuthorization(t, signer, testEscrowAddr, amount),
	}
}

func createTestEscrow(t *testing.T, db *storage.DB, buyer common.Address) *storage.Escrow {
	t.Helper()
	ctx := context.Background()
	task, err := db.CreateTask(ctx, "Test Task", "Description", "0xhash")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	escrow, err := db.CreateEscrow(ctx, &storage.Escrow{
		TaskID:                   task.ID,
		ChainID:                  testChainID,
		FactoryAddress:           testFactory,
		EscrowAddress:            testEscrowAddr,
		EscrowID:                 1,
		Buyer:                    buyer.Hex(),
		Worker:                   "0x2222222222222222222222222222222222222222",
		Verifier:                 "0x3333333333333333333333333333333333333333",
		Arbitrator:               "0x4444444444444444444444444444444444444444",
		Amount:                   "1000000",
		Token:                    testToken,
		Status:                   "created",
		SubmissionDeadline:       9999999999,
		ReviewPeriodSeconds:      86400,
		DisputePeriodSeconds:     172800,
		ArbitratorTimeoutSeconds: 604800,
		MilestoneCount:           1,
		ActiveWorker:             "0x2222222222222222222222222222222222222222",
	})
	if err != nil {
		t.Fatalf("create escrow: %v", err)
	}
	return escrow
}

func expectInvalid(t *testing.T, svc *Service, env MandateEnvelope, wantSubstr string) {
	t.Helper()
	err := svc.ValidateMandate(context.Background(), env)
	if err == nil {
		t.Fatalf("expected mandate to be rejected (%s)", wantSubstr)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("expected error containing %q, got: %v", wantSubstr, err)
	}
}

func TestBindToEscrow_GetEscrowErrorPrefix(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)

	_, err := svc.FundViaMandate(context.Background(), 999, signedPayment(t, svc, buyer, testEscrowAddr, "1000000"))
	if err == nil {
		t.Fatal("expected missing escrow error")
	}
	if !strings.Contains(err.Error(), "get escrow:") {
		t.Fatalf("expected get escrow prefix, got: %v", err)
	}
}

func TestValidateMandate_Valid(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)
	issued := testNow.Add(-time.Minute).Unix()

	cases := map[string]MandateEnvelope{
		"payment": paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000"),
		"intent":  intentEnvelope(t, buyer.addr, "5000000", "1000000", issued, 3600),
		"cart":    cartEnvelope(t, buyer.addr, "1000000", issued, 600),
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if err := svc.ValidateMandate(context.Background(), sign(t, svc, buyer.key, env)); err != nil {
				t.Fatalf("expected valid, got: %v", err)
			}
		})
	}
}

// TestValidateMandate_CrossLanguageVector checks the Go EIP-712 encoding against
// signatures produced by Python eth_account. Regenerate with
// `uv run --with eth-account scripts/ap2_eip712_vectors.py`. The key in that
// script is throwaway; only its address is recorded here. The mock returns the
// token DOMAIN_SEPARATOR eth_account used for ReceiveWithAuthorization.
func TestValidateMandate_CrossLanguageVector(t *testing.T) {
	svc, _, mc := setupTestService(t)
	svc.Now = func() time.Time { return time.Unix(1_700_000_100, 0) }
	mc.DomainSeparatorHash = common.HexToHash("0x71f17a3b2ff373b803d70a5a07c046c1a2bc8e89c09ef722fcb047abe94c9818")

	const (
		signer = "0x5c26334971Fb241E574B5b3E8afc3FD9037C93bd"
		nonce  = "0x1111111111111111111111111111111111111111111111111111111111111111"
		items  = "0x2222222222222222222222222222222222222222222222222222222222222222"
		authR  = "0x7b37da95c56d9448ac28f96e19214df49e4baa3cc1d9dbf6e22fc88dfd21c35d"
		authS  = "0x7c6ca65b66810941274fff39961dd66b9f2653a9f1736a40f14117f6b7685a41"
	)
	auth := EIP3009Authorization{
		Token: testToken, From: signer, To: testEscrowAddr, Value: "1000000",
		ValidAfter: "0", ValidBefore: "2000000000", Nonce: nonce,
		V: 28, R: authR, S: authS,
	}
	cases := []struct {
		name      string
		kind      MandateType
		payload   string
		signature string
		digest    string
	}{
		{
			name:      "intent",
			kind:      MandateTypeIntent,
			payload:   `{"signer":"` + signer + `","budget_amount":"5000000","budget_currency":"USDC","ttl_seconds":3600,"issued_at":1700000000,"description":"vector intent"}`,
			signature: "0x384248eed8eb25013576808715cae24a81fa1be1c5790462e11ef681a9edb1237b92eda583c310439a54ac555b2aaed6662a8d0561a83387172e278b75f900011c",
			digest:    "0x922cc19d4da84e7eac2a94312cd51c95b14f7b30a2f77d40d50f3ba5932fbf6a",
		},
		{
			name:      "cart",
			kind:      MandateTypeCart,
			payload:   `{"signer":"` + signer + `","merchant":"0x5555555555555555555555555555555555555555","amount":"1000000","currency":"USDC","items_hash":"` + items + `","ttl_seconds":3600,"issued_at":1700000000}`,
			signature: "0x185dca13e1de3785bed237fa5c74e015c765c0bde7573c13d957e30059a3fc9710a166fea6b3ea87ceede8e15226841b71efe39dd997459893edfae950a56e8a1b",
			digest:    "0x30a0d288e0a704a78dd5b228a96ab9f10650f51e81aa14392c39d3764c0d641d",
		},
		{
			name:      "payment",
			kind:      MandateTypePayment,
			payload:   `{"signer":"` + signer + `","amount":"1000000","currency":"USDC","recipient":"` + testEscrowAddr + `","nonce":"` + nonce + `"}`,
			signature: "0x1d5d0349a6142dc0a06fe4896f26dcabc8681b6a04a696745b3e1913c16ebbef05684df4d475f3bacda7007b3ffbe93bfe05017f9d846814da9a6c22b7f9d5391c",
			digest:    "0xeebd048927009211a3bb32a78f56263cb616d75413dcb8ff90c1966bc02c6ee0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := MandateEnvelope{
				Type: tc.kind, Payload: json.RawMessage(tc.payload),
				Signature: tc.signature, SignerAddress: signer, Authorization: auth,
			}
			vm, err := svc.verifyMandate(context.Background(), env)
			if err != nil {
				t.Fatalf("expected Python-signed vector to verify, got: %v", err)
			}
			if vm.Digest.Hex() != tc.digest {
				t.Fatalf("digest mismatch: got %s, want %s", vm.Digest.Hex(), tc.digest)
			}
		})
	}
}

func TestValidateMandate_MissingType(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)

	env := signedPayment(t, svc, buyer, testEscrowAddr, "100")
	env.Type = ""
	expectInvalid(t, svc, env, "mandate type is required")
}

func TestValidateMandate_SignerMismatch(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)

	env := signedPayment(t, svc, buyer, testEscrowAddr, "100")
	env.Authorization.From = "0x2222222222222222222222222222222222222222"
	expectInvalid(t, svc, env, "signer_address and authorization.from must match")
}

func TestValidateMandate_RejectsBadSignatures(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)
	attacker := newTestSigner(t)

	valid := signedPayment(t, svc, buyer, testEscrowAddr, "1000000")
	if err := svc.ValidateMandate(context.Background(), valid); err != nil {
		t.Fatalf("baseline mandate must be valid: %v", err)
	}

	otherChain := *svc
	otherChain.Cfg = &config.Config{ChainID: 8453, FactoryAddress: testFactory}
	otherFactory := *svc
	otherFactory.Cfg = &config.Config{ChainID: testChainID, FactoryAddress: "0x9999999999999999999999999999999999999999"}

	sigBytes := common.FromHex(valid.Signature)
	// Flip s to n - s with the complementary recovery id: still a valid ECDSA
	// signature for the same key, but non-canonical (high-s).
	n := crypto.S256().Params().N
	highS := new(big.Int).Sub(n, new(big.Int).SetBytes(sigBytes[32:64]))
	malleable := make([]byte, 65)
	copy(malleable, sigBytes[:32])
	highS.FillBytes(malleable[32:64])
	malleable[64] = 27 + (1 - (sigBytes[64] - 27))

	cases := []struct {
		name string
		env  func() MandateEnvelope
		want string
	}{
		{"missing signature", func() MandateEnvelope {
			e := valid
			e.Signature = ""
			return e
		}, "signature is required"},
		{"non-hex signature", func() MandateEnvelope {
			e := valid
			e.Signature = "0xzz" + strings.Repeat("00", 64)
			return e
		}, "invalid signature"},
		{"unprefixed signature", func() MandateEnvelope {
			e := valid
			e.Signature = strings.TrimPrefix(valid.Signature, "0x")
			return e
		}, "invalid signature"},
		{"short signature", func() MandateEnvelope {
			e := valid
			e.Signature = "0xdeadbeef"
			return e
		}, "invalid signature"},
		{"bad recovery id", func() MandateEnvelope {
			e := valid
			b := common.CopyBytes(sigBytes)
			b[64] = 30
			e.Signature = hexutil.Encode(b)
			return e
		}, "invalid recovery id"},
		{"high-s signature", func() MandateEnvelope {
			e := valid
			e.Signature = hexutil.Encode(malleable)
			return e
		}, "lower half"},
		{"wrong signer", func() MandateEnvelope {
			return sign(t, svc, attacker.key, paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000"))
		}, "signature does not match signer_address"},
		{"attacker claims own address with victim signature", func() MandateEnvelope {
			e := paymentEnvelope(t, attacker.addr, testEscrowAddr, "1000000")
			e.Signature = valid.Signature
			return e
		}, "signature does not match signer_address"},
		{"tampered payload", func() MandateEnvelope {
			e := valid
			var p PaymentMandate
			if err := json.Unmarshal(valid.Payload, &p); err != nil {
				t.Fatal(err)
			}
			p.Currency = "WETH"
			e.Payload = mustJSON(t, p)
			return e
		}, "signature does not match signer_address"},
		{"tampered authorization", func() MandateEnvelope {
			e := valid
			e.Authorization.ValidBefore = strconv.FormatInt(testNow.Add(365*24*time.Hour).Unix(), 10)
			return e
		}, "signature does not match signer_address"},
		{"tampered authorization nonce", func() MandateEnvelope {
			e := valid
			e.Authorization.Nonce = randomBytes32Hex(t)
			return e
		}, "signature does not match signer_address"},
		{"mandate type swapped", func() MandateEnvelope {
			e := sign(t, svc, buyer.key, cartEnvelope(t, buyer.addr, "1000000", testNow.Unix(), 600))
			e.Type = MandateTypeIntent
			return e
		}, "invalid intent payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectInvalid(t, svc, tc.env(), tc.want)
		})
	}

	t.Run("wrong chain", func(t *testing.T) {
		expectInvalid(t, svc, sign(t, &otherChain, buyer.key, paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")),
			"signature does not match signer_address")
	})
	t.Run("wrong factory", func(t *testing.T) {
		expectInvalid(t, svc, sign(t, &otherFactory, buyer.key, paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")),
			"signature does not match signer_address")
	})
}

func TestValidateMandate_RejectsInconsistentPayload(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)

	cases := []struct {
		name string
		env  func() MandateEnvelope
		want string
	}{
		{"unknown payload field", func() MandateEnvelope {
			e := paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")
			e.Payload = json.RawMessage(strings.TrimSuffix(string(e.Payload), "}") + `,"extra":"unsigned"}`)
			return e
		}, "unknown field"},
		{"missing payload", func() MandateEnvelope {
			e := paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")
			e.Payload = nil
			return e
		}, "payload is required"},
		{"payload signer differs", func() MandateEnvelope {
			e := paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")
			var p PaymentMandate
			_ = json.Unmarshal(e.Payload, &p)
			p.Signer = "0x7777777777777777777777777777777777777777"
			e.Payload = mustJSON(t, p)
			return e
		}, "payload.signer must equal signer_address"},
		{"payment recipient differs from authorization.to", func() MandateEnvelope {
			e := paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")
			e.Authorization.To = "0x8888888888888888888888888888888888888888"
			return e
		}, "payload.recipient must equal authorization.to"},
		{"payment amount differs from authorization.value", func() MandateEnvelope {
			e := paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")
			e.Authorization.Value = "2000000"
			return e
		}, "must equal payment amount"},
		{"intent authorization exceeds budget", func() MandateEnvelope {
			return intentEnvelope(t, buyer.addr, "1000000", "1000001", testNow.Unix(), 3600)
		}, "exceeds intent budget_amount"},
		{"intent missing issued_at", func() MandateEnvelope {
			return intentEnvelope(t, buyer.addr, "1000000", "1000000", 0, 3600)
		}, "issued_at"},
		{"non-canonical amount", func() MandateEnvelope {
			return paymentEnvelope(t, buyer.addr, testEscrowAddr, "01000000")
		}, "leading zeros"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A well-formed signature over a different, valid mandate: these
			// envelopes must be rejected on their contents, not the signature.
			e := tc.env()
			e.Signature = signedPayment(t, svc, buyer, testEscrowAddr, "1").Signature
			expectInvalid(t, svc, e, tc.want)
		})
	}
}

func TestValidateMandate_RejectsOutsideValidityWindow(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)

	t.Run("authorization expired", func(t *testing.T) {
		e := paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")
		e.Authorization.ValidBefore = strconv.FormatInt(testNow.Add(-time.Second).Unix(), 10)
		expectInvalid(t, svc, sign(t, svc, buyer.key, e), "mandate expired")
	})
	t.Run("authorization not yet valid", func(t *testing.T) {
		e := paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")
		e.Authorization.ValidAfter = strconv.FormatInt(testNow.Add(time.Minute).Unix(), 10)
		expectInvalid(t, svc, sign(t, svc, buyer.key, e), "not yet valid")
	})
	t.Run("intent ttl elapsed", func(t *testing.T) {
		e := intentEnvelope(t, buyer.addr, "1000000", "1000000", testNow.Add(-2*time.Hour).Unix(), 3600)
		expectInvalid(t, svc, sign(t, svc, buyer.key, e), "mandate expired")
	})
	t.Run("cart ttl elapsed", func(t *testing.T) {
		e := cartEnvelope(t, buyer.addr, "1000000", testNow.Add(-10*time.Minute).Unix(), 300)
		expectInvalid(t, svc, sign(t, svc, buyer.key, e), "mandate expired")
	})
	t.Run("issued_at too far in the future", func(t *testing.T) {
		e := intentEnvelope(t, buyer.addr, "1000000", "1000000", testNow.Add(10*time.Minute).Unix(), 3600)
		expectInvalid(t, svc, sign(t, svc, buyer.key, e), "issued_at is in the future")
	})
	t.Run("issued_at within clock skew", func(t *testing.T) {
		e := intentEnvelope(t, buyer.addr, "1000000", "1000000", testNow.Add(2*time.Minute).Unix(), 3600)
		if err := svc.ValidateMandate(context.Background(), sign(t, svc, buyer.key, e)); err != nil {
			t.Fatalf("expected small clock skew to be tolerated: %v", err)
		}
	})
	t.Run("replay after expiry", func(t *testing.T) {
		e := sign(t, svc, buyer.key, intentEnvelope(t, buyer.addr, "1000000", "1000000", testNow.Unix(), 600))
		if err := svc.ValidateMandate(context.Background(), e); err != nil {
			t.Fatalf("expected mandate valid when fresh: %v", err)
		}
		later := *svc
		later.Now = func() time.Time { return testNow.Add(601 * time.Second) }
		expectInvalid(t, &later, e, "mandate expired")
	})
}

func TestFundViaMandate_HappyPath(t *testing.T) {
	svc, db, mc := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)

	env := signedPayment(t, svc, buyer, testEscrowAddr, "1000000")
	resp, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err != nil {
		t.Fatalf("fund via mandate: %v", err)
	}

	if resp.EscrowID != escrow.ID {
		t.Fatalf("expected escrow_id %d, got %d", escrow.ID, resp.EscrowID)
	}
	if resp.TxHash == "" {
		t.Fatal("expected non-empty tx_hash")
	}
	if resp.Status != "funded" {
		t.Fatalf("expected status 'funded', got %s", resp.Status)
	}
	if resp.MandateID == "" {
		t.Fatal("expected non-empty mandate_id")
	}
	if len(mc.SentTxs) != 1 || mc.SentTxs[0].Method != "fundWithAuthorization" {
		t.Fatalf("expected one fundWithAuthorization tx, got %+v", mc.SentTxs)
	}

	vm, err := svc.verifyMandate(context.Background(), env)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	stored, err := db.GetAP2Mandate(context.Background(), resp.MandateID)
	if err != nil {
		t.Fatalf("get stored mandate: %v", err)
	}
	if "0x"+stored.MandateHash != vm.Digest.Hex() {
		t.Fatalf("stored mandate hash %s, want EIP-712 digest %s", stored.MandateHash, vm.Digest.Hex())
	}
	if stored.SignerAddress != strings.ToLower(buyer.addr.Hex()) {
		t.Fatalf("stored signer %s, want %s", stored.SignerAddress, buyer.addr.Hex())
	}
	if stored.ExpiresAt == "" {
		t.Fatal("expected stored expires_at")
	}
}

func TestFundViaMandate_RejectsUnsignedMandateWithoutTouchingChain(t *testing.T) {
	svc, db, mc := setupTestService(t)
	buyer := newTestSigner(t)
	attacker := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)

	// The attacker knows the buyer's address and a (replayable or guessed)
	// authorization but cannot produce the buyer's mandate signature.
	forged := sign(t, svc, attacker.key, paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000"))

	_, err := svc.FundViaMandate(context.Background(), escrow.ID, forged)
	if !errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected ErrInvalidMandate, got: %v", err)
	}
	if len(mc.SentTxs) != 0 {
		t.Fatalf("expected no chain transactions, got %+v", mc.SentTxs)
	}
}

func TestFundViaMandate_BuyerMismatch(t *testing.T) {
	svc, db, _ := setupTestService(t)
	buyer := newTestSigner(t)
	other := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)

	// Validly signed by a key that is not the escrow buyer.
	env := signedPayment(t, svc, other, testEscrowAddr, "1000000")

	_, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err == nil {
		t.Fatal("expected error for buyer mismatch")
	}
	if !errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected ErrInvalidMandate, got: %v", err)
	}
}

func TestFundViaMandate_InsufficientValue(t *testing.T) {
	svc, db, _ := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)

	env := signedPayment(t, svc, buyer, testEscrowAddr, "500000")

	_, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err == nil {
		t.Fatal("expected error for insufficient value")
	}
	if !errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected ErrInvalidMandate, got: %v", err)
	}
}

func TestFundViaMandate_AuthToMismatch(t *testing.T) {
	svc, db, _ := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)

	env := signedPayment(t, svc, buyer, "0x9999999999999999999999999999999999999999", "1000000")

	_, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err == nil {
		t.Fatal("expected error for authorization.to mismatch")
	}
	if !errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected ErrInvalidMandate, got: %v", err)
	}
}

func TestFundViaMandate_BindDBErrorNotWrappedAsInvalidMandate(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)

	env := signedPayment(t, svc, buyer, testEscrowAddr, "1000000")

	_, err := svc.FundViaMandate(context.Background(), 999, env)
	if err == nil {
		t.Fatal("expected bind-time get escrow error")
	}
	if errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected non-validation error to be unwrapped, got: %v", err)
	}
	if !strings.Contains(err.Error(), "get escrow:") {
		t.Fatalf("expected get escrow prefix, got: %v", err)
	}
}

func mandateCount(t *testing.T, db *storage.DB) int {
	t.Helper()
	var n int
	if err := db.SQLDB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM ap2_mandates`).Scan(&n); err != nil {
		t.Fatalf("count mandates: %v", err)
	}
	return n
}

func TestFundViaMandate_RejectsValueAboveEscrowAmount(t *testing.T) {
	svc, db, mc := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)

	env := signedPayment(t, svc, buyer, testEscrowAddr, "2000000")
	_, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if !errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected ErrInvalidMandate, got: %v", err)
	}
	if len(mc.SentTxs) != 0 {
		t.Fatalf("expected no chain transaction, got %+v", mc.SentTxs)
	}
	if mandateCount(t, db) != 0 {
		t.Fatal("expected no mandate row")
	}
}

func TestFundViaMandate_RejectsTokenMismatch(t *testing.T) {
	svc, db, mc := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)

	env := paymentEnvelope(t, buyer.addr, testEscrowAddr, "1000000")
	env.Authorization.Token = "0x1111111111111111111111111111111111111111"
	env = sign(t, svc, buyer.key, env)

	_, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if !errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected ErrInvalidMandate, got: %v", err)
	}
	if !strings.Contains(err.Error(), "authorization.token") {
		t.Fatalf("expected token mismatch, got: %v", err)
	}
	if len(mc.SentTxs) != 0 {
		t.Fatalf("expected no chain transaction, got %+v", mc.SentTxs)
	}
	if mandateCount(t, db) != 0 {
		t.Fatal("expected no mandate row")
	}
}

func TestFundViaMandate_RejectsAuthorizationSignatureBeforeWrite(t *testing.T) {
	svc, db, mc := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)

	env := signedPayment(t, svc, buyer, testEscrowAddr, "1000000")
	env.Authorization.R = "0x0000000000000000000000000000000000000000000000000000000000000004"
	_, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if !errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected ErrInvalidMandate, got: %v", err)
	}
	if !strings.Contains(err.Error(), "authorization signature") {
		t.Fatalf("expected authorization signature error, got: %v", err)
	}
	if len(mc.SentTxs) != 0 {
		t.Fatalf("expected no chain transaction, got %+v", mc.SentTxs)
	}
	if mandateCount(t, db) != 0 {
		t.Fatal("expected no mandate row")
	}
}

func TestFundViaMandate_RetriesAfterChainError(t *testing.T) {
	svc, db, mc := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)
	env := signedPayment(t, svc, buyer, testEscrowAddr, "1000000")

	mc.FundWithAuthErr = errors.New("gas estimation failed")
	_, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err == nil || errors.Is(err, ErrInvalidMandate) {
		t.Fatalf("expected chain error, got: %v", err)
	}
	if mandateCount(t, db) != 1 {
		t.Fatalf("expected one failed row, got %d", mandateCount(t, db))
	}

	mc.FundWithAuthErr = nil
	resp, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if resp.Status != "funded" {
		t.Fatalf("expected funded, got %s", resp.Status)
	}
	if len(mc.SentTxs) != 1 {
		t.Fatalf("expected one broadcast, got %+v", mc.SentTxs)
	}
}

func TestFundViaMandate_RetriesAfterRevert(t *testing.T) {
	svc, db, mc := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)
	env := signedPayment(t, svc, buyer, testEscrowAddr, "1000000")

	mc.Receipt = &types.Receipt{Status: types.ReceiptStatusFailed}
	_, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err == nil || !strings.Contains(err.Error(), "reverted") {
		t.Fatalf("expected revert, got: %v", err)
	}

	mc.Receipt = &types.Receipt{Status: types.ReceiptStatusSuccessful}
	resp, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err != nil {
		t.Fatalf("retry after revert: %v", err)
	}
	if resp.Status != "funded" {
		t.Fatalf("expected funded, got %s", resp.Status)
	}
	if len(mc.SentTxs) != 2 {
		t.Fatalf("expected a second broadcast, got %+v", mc.SentTxs)
	}
}

func TestFundViaMandate_PendingReceiptIsNotRebroadcast(t *testing.T) {
	svc, db, mc := setupTestService(t)
	buyer := newTestSigner(t)
	escrow := createTestEscrow(t, db, buyer.addr)
	env := signedPayment(t, svc, buyer, testEscrowAddr, "1000000")

	mc.Receipt = nil
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := svc.FundViaMandate(ctx, escrow.ID, env)
	if !errors.Is(err, ErrFundingPending) {
		t.Fatalf("expected pending confirmation, got: %v", err)
	}
	if len(mc.SentTxs) != 1 {
		t.Fatalf("expected one broadcast, got %+v", mc.SentTxs)
	}

	mc.Receipt = &types.Receipt{Status: types.ReceiptStatusSuccessful}
	resp, err := svc.FundViaMandate(context.Background(), escrow.ID, env)
	if err != nil {
		t.Fatalf("retry after receipt: %v", err)
	}
	if resp.Status != "funded" {
		t.Fatalf("expected funded, got %s", resp.Status)
	}
	if len(mc.SentTxs) != 1 {
		t.Fatalf("expected no second broadcast, got %+v", mc.SentTxs)
	}
}

func TestValidateMandate_RejectsAmbiguousJSON(t *testing.T) {
	svc, _, _ := setupTestService(t)
	buyer := newTestSigner(t)
	env := signedPayment(t, svc, buyer, testEscrowAddr, "1000000")

	var payload PaymentMandate
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	raw := `{"amount":"999","signer":"` + payload.Signer +
		`","amount":"1000000","currency":"USDC","recipient":"` + payload.Recipient +
		`","nonce":"` + payload.Nonce + `"}`
	env.Payload = json.RawMessage(raw)
	expectInvalid(t, svc, env, "duplicate key")

	cased := `{"Signer":"` + payload.Signer + `","amount":"1000000","currency":"USDC","recipient":"` +
		payload.Recipient + `","nonce":"` + payload.Nonce + `"}`
	env.Payload = json.RawMessage(cased)
	expectInvalid(t, svc, env, "case-variant key")
}
