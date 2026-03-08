#!/usr/bin/env python3
"""AP2 Mandate Bridge Demo — EIP-3009 gasless escrow funding on Base Sepolia.

Signs an EIP-712 ReceiveWithAuthorization off-chain and funds a USDC escrow
via POST /api/v1/ap2/fund, then completes the escrow lifecycle.
"""

import json
import os
import secrets
import subprocess
import sys
import tempfile
import time

import requests
from dotenv import load_dotenv
from eth_account import Account
from eth_account.messages import encode_typed_data

DEFAULT_BASE_URL = "http://localhost:8080"
BASE_URL = DEFAULT_BASE_URL
RPC_URL = "https://sepolia.base.org"
USDC = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
CHAIN_ID = 84532
ESCROW_AMOUNT = "100000"  # 0.10 USDC

# USDC EIP-712 domain on Base Sepolia
USDC_DOMAIN = {
    "name": "USDC",
    "version": "2",
    "chainId": CHAIN_ID,
    "verifyingContract": USDC,
}

RECEIVE_WITH_AUTH_TYPES = {
    "ReceiveWithAuthorization": [
        {"name": "from", "type": "address"},
        {"name": "to", "type": "address"},
        {"name": "value", "type": "uint256"},
        {"name": "validAfter", "type": "uint256"},
        {"name": "validBefore", "type": "uint256"},
        {"name": "nonce", "type": "bytes32"},
    ],
}


def api_get(path: str) -> dict:
    resp = requests.get(f"{BASE_URL}{path}", timeout=30)
    resp.raise_for_status()
    return resp.json()


def api_post(path: str, data: dict | None = None, retries: int = 5) -> dict:
    for attempt in range(1, retries + 1):
        resp = requests.post(
            f"{BASE_URL}{path}",
            json=data or {},
            headers={"Content-Type": "application/json"},
            timeout=60,
        )
        body = resp.json()
        if resp.ok and "error" not in body:
            return body
        if attempt < retries:
            print(f"  Retry {attempt}/{retries}: {body.get('error', resp.status_code)}")
            time.sleep(3)
        else:
            print(f"  FAILED after {retries} attempts: {body}", file=sys.stderr)
            sys.exit(1)


def cast_tx(private_key: str, to: str, sig: str, *args: str) -> str:
    """Send a transaction via cast and return the tx hash."""
    cmd = [
        "cast", "send", to, sig, *args,
        "--private-key", private_key,
        "--rpc-url", RPC_URL,
        "--json",
    ]
    for attempt in range(1, 6):
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        if result.returncode == 0:
            try:
                return json.loads(result.stdout)["transactionHash"]
            except (json.JSONDecodeError, KeyError):
                pass
        print(f"  cast_tx retry {attempt}/5: {result.stderr[:120]}")
        time.sleep(4)
    print("  cast_tx FAILED", file=sys.stderr)
    sys.exit(1)


def wait_for_receipt(tx_hash: str, *, timeout_seconds: int = 60) -> dict:
    """Poll cast receipt until the transaction is mined."""
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        result = subprocess.run(
            ["cast", "receipt", tx_hash, "--rpc-url", RPC_URL, "--json"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        if result.returncode == 0 and result.stdout.strip():
            try:
                return json.loads(result.stdout)
            except json.JSONDecodeError:
                pass
        time.sleep(2)
    print(f"Timed out waiting for receipt: {tx_hash}", file=sys.stderr)
    sys.exit(1)


def require_env(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value:
        print(f"ERROR: missing required environment variable: {name}", file=sys.stderr)
        print("Hint: set -a && source .env && set +a", file=sys.stderr)
        sys.exit(1)
    return value


def main():
    load_dotenv()
    global BASE_URL
    BASE_URL = os.getenv("BASE_URL", DEFAULT_BASE_URL).strip() or DEFAULT_BASE_URL

    buyer_key = require_env("PRIVATE_KEY")
    worker_key = require_env("WORKER_KEY")
    buyer_addr = Account.from_key(buyer_key).address
    # Default worker address is derived from WORKER_KEY to avoid key/address drift.
    worker_addr = os.environ.get("WORKER_ADDRESS") or Account.from_key(worker_key).address

    print("=" * 64)
    print("  AP2 Mandate Bridge Demo — EIP-3009 Gasless Funding")
    print("=" * 64)
    print()
    print(f"  Buyer:  {buyer_addr}")
    print(f"  Worker: {worker_addr}")
    print(f"  USDC:   {USDC}")
    print(f"  Amount: {ESCROW_AMOUNT} (0.10 USDC)")
    print()

    # Step 1: Create a USDC escrow
    print("  → Step 1: Create USDC escrow")
    deadline = int(time.time()) + 7200
    create_resp = api_post("/api/v1/escrows", {
        "title": "AP2 Demo: Gasless EIP-3009 Funding",
        "description": "Escrow funded via AP2 mandate bridge with receiveWithAuthorization",
        "buyer": buyer_addr,
        "worker": worker_addr,
        "verifier": "0xEa62Afd342704CF52A48A50BC5a7e57B45e3de7A",
        "arbitrator": "0x98586bC45A9D6B9D2C5F11292d4a9bfA4a50b097",
        "verifier_panel": ["0xEa62Afd342704CF52A48A50BC5a7e57B45e3de7A"],
        "quorum_verifier_count": 1,
        "quorum_threshold": 1,
        "amount": ESCROW_AMOUNT,
        "token": USDC,
        "submission_deadline": str(deadline),
        "review_period_seconds": "3600",
        "dispute_period_seconds": "3600",
        "arbitrator_timeout_seconds": "7200",
    })
    escrow_id = create_resp["escrow_id"]
    escrow_addr = create_resp["escrow_address"]
    tx_create = create_resp["tx_hash"]
    print(f"    Escrow ID: {escrow_id}")
    print(f"    Escrow Address: {escrow_addr}")
    print(f"    Create tx: {tx_create}")

    # Wait for create tx to be mined
    print("  → Waiting for create tx to be mined...")
    mined = False
    for _ in range(20):
        result = subprocess.run(
            ["cast", "receipt", tx_create, "--rpc-url", RPC_URL, "--json"],
            capture_output=True, text=True, timeout=10,
        )
        if result.returncode == 0 and result.stdout.strip():
            mined = True
            break
        time.sleep(2)
    if not mined:
        print(
            f"  Create tx not mined after 20 attempts: tx={tx_create} rpc={RPC_URL}",
            file=sys.stderr,
        )
        sys.exit(1)
    time.sleep(3)

    # Step 2: Generate EIP-3009 authorization signature
    print("  → Step 2: Sign EIP-3009 ReceiveWithAuthorization")
    nonce = secrets.token_bytes(32)
    nonce_hex = "0x" + nonce.hex()
    valid_after = 0
    valid_before = int(time.time()) + 3600

    message = {
        "from": buyer_addr,
        "to": escrow_addr,
        "value": int(ESCROW_AMOUNT),
        "validAfter": valid_after,
        "validBefore": valid_before,
        "nonce": nonce,
    }

    signable = encode_typed_data(
        domain_data=USDC_DOMAIN,
        message_types=RECEIVE_WITH_AUTH_TYPES,
        message_data=message,
    )

    signed = Account.sign_message(signable, private_key=buyer_key)
    v = signed.v
    r = "0x" + signed.r.to_bytes(32, "big").hex()
    s = "0x" + signed.s.to_bytes(32, "big").hex()

    print(f"    Nonce: {nonce_hex}")
    print(f"    Valid after: {valid_after}")
    print(f"    Valid before: {valid_before}")
    print(f"    v={v} r={r[:10]}... s={s[:10]}...")

    # Step 3: Construct mandate envelope and call AP2 fund
    print("  → Step 3: Fund via AP2 mandate bridge")
    mandate_payload = {
        "escrow_id": escrow_id,
        "mandate_envelope": {
            "type": "payment",
            "payload": {
                "signer": buyer_addr,
                "amount": ESCROW_AMOUNT,
                "currency": "USDC",
                "recipient": escrow_addr,
                "nonce": nonce_hex,
            },
            "signature": f"0x{signed.signature.hex()}",
            "signer_address": buyer_addr,
            "authorization": {
                "from": buyer_addr,
                "to": escrow_addr,
                "value": ESCROW_AMOUNT,
                "valid_after": str(valid_after),
                "valid_before": str(valid_before),
                "nonce": nonce_hex,
                "v": v,
                "r": r,
                "s": s,
            },
        },
    }

    print("    Mandate envelope:")
    print("      type: payment")
    print(f"      signer: {buyer_addr}")
    print(f"      authorization.to: {escrow_addr}")
    print(f"      authorization.value: {ESCROW_AMOUNT}")

    # The buyer needs to have approved USDC for the escrow first (receiveWithAuthorization
    # doesn't need approval, but the USDC contract on Base Sepolia may require it).
    # Actually, EIP-3009 receiveWithAuthorization does NOT require prior approval --
    # the signature IS the authorization. But we need to ensure the buyer has enough USDC.

    fund_resp = api_post("/api/v1/ap2/fund", mandate_payload)
    tx_fund = fund_resp["tx_hash"]
    mandate_id = fund_resp["mandate_id"]
    print(f"    Fund tx: {tx_fund}")
    print(f"    Mandate ID: {mandate_id}")
    print(f"    Status: {fund_resp['status']}")

    time.sleep(8)

    # Step 4: Verify escrow is funded
    print("  → Step 4: Verify escrow status")
    escrow_status = api_get(f"/api/v1/escrows/{escrow_id}")
    escrow_data = escrow_status.get("escrow", escrow_status)
    print(f"    Escrow status: {escrow_data.get('Status', escrow_data.get('status', '?'))}")

    # Step 5: Check mandate record
    print("  → Step 5: Check mandate record")
    mandate_resp = api_get(f"/api/v1/ap2/mandates/{mandate_id}")
    print(f"    Mandate status: {mandate_resp.get('status', '?')}")
    print(f"    Funding tx: {mandate_resp.get('funding_tx_hash', '?')}")

    # Step 6: Complete lifecycle — worker submits
    print("  → Step 6: Worker submits work")
    sub_hash = subprocess.run(
        ["cast", "keccak", "ipfs://QmAP2_demo_submission"],
        capture_output=True, text=True, timeout=10,
    ).stdout.strip()
    tx_submit = cast_tx(
        worker_key, escrow_addr,
        "submit(bytes32,string,bytes32)",
        sub_hash, "ipfs://QmAP2_demo_submission", "0x" + ("00" * 32),
    )
    print(f"    Submit tx: {tx_submit}")
    submit_receipt = wait_for_receipt(tx_submit, timeout_seconds=90)
    submit_status = submit_receipt.get("status")
    submit_status_int = int(submit_status, 0) if isinstance(submit_status, str) else int(submit_status)
    if submit_status_int != 1:
        print(f"    Submit tx failed: {submit_receipt}", file=sys.stderr)
        sys.exit(1)

    # Step 7: Buyer approves
    print("  → Step 7: Buyer approves")
    approve_resp = api_post(f"/api/v1/escrows/{escrow_id}/approve", {"role": "buyer"})
    tx_approve = approve_resp["tx_hash"]
    print(f"    Approve tx: {tx_approve}")

    time.sleep(8)

    # Step 8: Final status
    print("  → Step 8: Verify final state")
    final_status = api_get(f"/api/v1/escrows/{escrow_id}")
    final_data = final_status.get("escrow", final_status)
    print(f"    Final status: {final_data.get('Status', final_data.get('status', '?'))}")

    print()
    print("=" * 64)
    print("  AP2 Demo Complete!")
    print("=" * 64)
    print()

    results = {
        "escrow_id": escrow_id,
        "escrow_address": escrow_addr,
        "mandate_id": mandate_id,
        "tx_create": tx_create,
        "tx_fund_ap2": tx_fund,
        "tx_submit": tx_submit,
        "tx_approve": tx_approve,
        "eip3009_nonce": nonce_hex,
        "eip3009_valid_before": valid_before,
    }
    results_file = (
        os.getenv("AP2_RESULTS_FILE")
        or os.getenv("DEMO_OUTPUT_PATH")
        or os.getenv("OUTPUT_PATH")
    )
    if not results_file:
        fd, results_file = tempfile.mkstemp(prefix="ap2_demo_", suffix=".json")
        os.close(fd)
    with open(results_file, "w") as f:
        json.dump(results, f, indent=2)
    print(f"Results saved to: {results_file}")
    print(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
