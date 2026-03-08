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
        "--async",
        "--private-key", private_key,
        "--rpc-url", RPC_URL,
        "--json",
    ]
    for attempt in range(1, 6):
        try:
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        except subprocess.TimeoutExpired:
            print("  cast_tx timed out before returning a transaction hash", file=sys.stderr)
            sys.exit(1)
        stdout = result.stdout.strip()
        stderr = result.stderr.strip()
        if stdout:
            try:
                tx_hash = json.loads(stdout).get("transactionHash")
            except json.JSONDecodeError:
                tx_hash = None
            if tx_hash:
                return tx_hash
            if result.returncode == 0 and tx_hash is None:
                print(
                    "  cast_tx succeeded but response was ambiguous; refusing to rebroadcast",
                    file=sys.stderr,
                )
                if stdout:
                    print(f"  stdout: {stdout[:240]}", file=sys.stderr)
                if stderr:
                    print(f"  stderr: {stderr[:240]}", file=sys.stderr)
                sys.exit(1)
        print(f"  cast_tx retry {attempt}/5 (rc={result.returncode}): {(stderr or stdout)[:120]}")
        time.sleep(4)
    print("  cast_tx FAILED", file=sys.stderr)
    sys.exit(1)


def wait_for_receipt(tx_hash: str, *, timeout_seconds: int = 60) -> dict:
    """Wait for cast receipt to return a mined transaction receipt."""
    try:
        result = subprocess.run(
            ["cast", "receipt", tx_hash, "--rpc-url", RPC_URL, "--json"],
            capture_output=True,
            text=True,
            timeout=timeout_seconds,
        )
    except subprocess.TimeoutExpired:
        print(f"Timed out waiting for receipt: {tx_hash}", file=sys.stderr)
        sys.exit(1)

    stdout = result.stdout.strip()
    stderr = result.stderr.strip()
    if stderr:
        print(
            f"cast receipt stderr for {tx_hash}: returncode={result.returncode}",
            file=sys.stderr,
        )
        if stderr:
            print(f"stderr: {stderr}", file=sys.stderr)
        if stdout:
            print(f"stdout: {stdout}", file=sys.stderr)
    if result.returncode != 0:
        print(
            f"cast receipt failed for {tx_hash}: returncode={result.returncode}",
            file=sys.stderr,
        )
        if stderr:
            print(f"stderr: {stderr}", file=sys.stderr)
        if stdout:
            print(f"stdout: {stdout}", file=sys.stderr)
        sys.exit(1)
    if not stdout:
        print(f"Receipt for {tx_hash} was empty", file=sys.stderr)
        sys.exit(1)
    try:
        receipt = json.loads(stdout)
    except json.JSONDecodeError as exc:
        print(f"Invalid JSON receipt for {tx_hash}: {exc}", file=sys.stderr)
        print(f"stdout: {stdout}", file=sys.stderr)
        sys.exit(1)
    if receipt.get("status") is None:
        print(f"Receipt for {tx_hash} is missing required field: status", file=sys.stderr)
        print(f"stdout: {stdout}", file=sys.stderr)
        sys.exit(1)
    return receipt


def receipt_status_int(receipt: dict) -> int:
    status = receipt.get("status")
    if status is None:
        raise ValueError("Missing 'status' field in receipt")
    return int(status, 0) if isinstance(status, str) else int(status)


def require_env(name: str) -> str:
    value = os.getenv(name, "").strip()
    if not value:
        print(f"ERROR: missing required environment variable: {name}", file=sys.stderr)
        print("Hint: set -a && source .env && set +a", file=sys.stderr)
        sys.exit(1)
    return value


def require_address_env(name: str) -> str:
    value = require_env(name).strip()
    if not value.startswith("0x") or len(value) != 42:
        print(f"ERROR: invalid Ethereum address for {name}: {value}", file=sys.stderr)
        sys.exit(1)
    try:
        int(value[2:], 16)
    except ValueError:
        print(f"ERROR: invalid Ethereum address for {name}: {value}", file=sys.stderr)
        sys.exit(1)
    return "0x" + value[2:].lower()


def main():
    load_dotenv()
    global BASE_URL
    BASE_URL = os.getenv("BASE_URL", DEFAULT_BASE_URL).strip() or DEFAULT_BASE_URL

    buyer_key = require_env("PRIVATE_KEY")
    worker_key = require_env("WORKER_KEY")
    verifier_addr = require_address_env("VERIFIER_ADDR")
    arbitrator_addr = require_address_env("ARBITRATOR_ADDR")
    buyer_addr = Account.from_key(buyer_key).address
    derived_worker_addr = Account.from_key(worker_key).address
    configured_worker_addr = (os.environ.get("WORKER_ADDRESS") or "").strip() or None
    if configured_worker_addr:
        configured_worker_addr = require_address_env("WORKER_ADDRESS")
    if (
        configured_worker_addr
        and configured_worker_addr.lower() != derived_worker_addr.lower()
    ):
        print(
            "ERROR: WORKER_ADDRESS does not match WORKER_KEY-derived address: "
            f"{configured_worker_addr} != {derived_worker_addr}",
            file=sys.stderr,
        )
        sys.exit(1)
    # Default worker address is derived from WORKER_KEY to avoid key/address drift.
    worker_addr = derived_worker_addr

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
        "verifier": verifier_addr,
        "arbitrator": arbitrator_addr,
        "verifier_panel": [verifier_addr],
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
    create_receipt = wait_for_receipt(tx_create, timeout_seconds=40)
    if receipt_status_int(create_receipt) != 1:
        print(f"  Create tx failed: {create_receipt}", file=sys.stderr)
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

    fund_receipt = wait_for_receipt(tx_fund, timeout_seconds=90)
    if receipt_status_int(fund_receipt) != 1:
        print(f"    Fund tx failed: {fund_receipt}", file=sys.stderr)
        sys.exit(1)

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
    if receipt_status_int(submit_receipt) != 1:
        print(f"    Submit tx failed: {submit_receipt}", file=sys.stderr)
        sys.exit(1)

    # Step 7: Buyer approves
    print("  → Step 7: Buyer approves")
    approve_resp = api_post(f"/api/v1/escrows/{escrow_id}/approve", {"role": "buyer"})
    tx_approve = approve_resp["tx_hash"]
    print(f"    Approve tx: {tx_approve}")
    approve_receipt = wait_for_receipt(tx_approve, timeout_seconds=90)
    if receipt_status_int(approve_receipt) != 1:
        print(f"    Approve tx failed: {approve_receipt}", file=sys.stderr)
        sys.exit(1)

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
