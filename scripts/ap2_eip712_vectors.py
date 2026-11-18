#!/usr/bin/env python3
"""Print Go test vectors for AP2 intent, cart, and payment mandates.

The digests and signatures are computed with eth_account.encode_typed_data,
independent of the Go EIP-712 implementation. The key below is throwaway: it
was generated for this file and must not be funded.

    uv run --with eth-account scripts/ap2_eip712_vectors.py

The EIP-3009 signature is ReceiveWithAuthorization under a USDC-style domain.
Go does not rebuild that domain. It reads DOMAIN_SEPARATOR() and the test mock
returns the separator this script prints.
"""

from eth_account import Account
from eth_account.messages import encode_typed_data
from eth_utils import keccak

# Throwaway secp256k1 key. Not a deployment key and not used for funds.
PRIVATE_KEY = "0x123ff739c4e95ddcb9808b7c2e9e7f0caa7704dab610f98857823b768c19bcef"

CHAIN_ID = 84532
FACTORY = "0x1234567890123456789012345678901234567890"
ESCROW = "0xABCDABCDABCDABCDABCDABCDABCDABCDABCDABCD"
TOKEN = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"
VALUE = 1_000_000
VALID_AFTER = 0
VALID_BEFORE = 2_000_000_000
ISSUED_AT = 1_700_000_000
TTL = 3600
NONCE = bytes.fromhex("11" * 32)
ITEMS = bytes.fromhex("22" * 32)
MERCHANT = "0x5555555555555555555555555555555555555555"
DESCRIPTION = "vector intent"

AUTHORIZATION_TYPE = [
    {"name": "token", "type": "address"},
    {"name": "from", "type": "address"},
    {"name": "to", "type": "address"},
    {"name": "value", "type": "uint256"},
    {"name": "validAfter", "type": "uint256"},
    {"name": "validBefore", "type": "uint256"},
    {"name": "nonce", "type": "bytes32"},
]
RECEIVE_TYPES = {
    "ReceiveWithAuthorization": [
        {"name": "from", "type": "address"},
        {"name": "to", "type": "address"},
        {"name": "value", "type": "uint256"},
        {"name": "validAfter", "type": "uint256"},
        {"name": "validBefore", "type": "uint256"},
        {"name": "nonce", "type": "bytes32"},
    ],
}


def sign_typed(domain, types, message, primary):
    signable = encode_typed_data(
        full_message={
            "types": types,
            "primaryType": primary,
            "domain": domain,
            "message": message,
        }
    )
    signed = Account.sign_message(signable, private_key=PRIVATE_KEY)
    digest = keccak(b"\x19" + signable.version + signable.header + signable.body)
    assert signed.message_hash == digest
    recovered = Account.recover_message(signable, signature=signed.signature)
    assert recovered == Account.from_key(PRIVATE_KEY).address
    return "0x" + digest.hex(), "0x" + signed.signature.hex(), "0x" + signable.header.hex()


def main() -> None:
    account = Account.from_key(PRIVATE_KEY)
    signer = account.address
    auth_message = {
        "token": TOKEN,
        "from": signer,
        "to": ESCROW,
        "value": VALUE,
        "validAfter": VALID_AFTER,
        "validBefore": VALID_BEFORE,
        "nonce": NONCE,
    }
    receive_message = {
        "from": signer,
        "to": ESCROW,
        "value": VALUE,
        "validAfter": VALID_AFTER,
        "validBefore": VALID_BEFORE,
        "nonce": NONCE,
    }
    token_domain = {
        "name": "USDC",
        "version": "2",
        "chainId": CHAIN_ID,
        "verifyingContract": TOKEN,
    }
    auth_digest, auth_sig, domain_separator = sign_typed(
        token_domain, RECEIVE_TYPES, receive_message, "ReceiveWithAuthorization"
    )
    ap2_domain = {
        "name": "agent-escrow AP2",
        "version": "1",
        "chainId": CHAIN_ID,
        "verifyingContract": FACTORY,
    }
    mandates = {
        "IntentMandate": {
            "signer": signer,
            "budgetAmount": 5_000_000,
            "budgetCurrency": "USDC",
            "ttlSeconds": TTL,
            "issuedAt": ISSUED_AT,
            "description": DESCRIPTION,
            "authorization": auth_message,
        },
        "CartMandate": {
            "signer": signer,
            "merchant": MERCHANT,
            "amount": VALUE,
            "currency": "USDC",
            "itemsHash": ITEMS,
            "ttlSeconds": TTL,
            "issuedAt": ISSUED_AT,
            "authorization": auth_message,
        },
        "PaymentMandate": {
            "signer": signer,
            "amount": VALUE,
            "currency": "USDC",
            "recipient": ESCROW,
            "nonce": NONCE,
            "authorization": auth_message,
        },
    }
    print(f"signer {signer}")
    print(f"domain_separator {domain_separator}")
    print(f"auth_digest {auth_digest}")
    print(f"auth_signature {auth_sig}")
    v = auth_sig[-2:]
    print(f"auth_v {int(v, 16)}")
    print(f"auth_r {auth_sig[:66]}")
    print(f"auth_s 0x{auth_sig[66:130]}")
    for primary, message in mandates.items():
        types = {"Authorization": AUTHORIZATION_TYPE, primary: None}
        # encode_typed_data wants the primary type's fields, not a None placeholder.
        field_lists = {
            "IntentMandate": [
                {"name": "signer", "type": "address"},
                {"name": "budgetAmount", "type": "uint256"},
                {"name": "budgetCurrency", "type": "string"},
                {"name": "ttlSeconds", "type": "uint256"},
                {"name": "issuedAt", "type": "uint256"},
                {"name": "description", "type": "string"},
                {"name": "authorization", "type": "Authorization"},
            ],
            "CartMandate": [
                {"name": "signer", "type": "address"},
                {"name": "merchant", "type": "address"},
                {"name": "amount", "type": "uint256"},
                {"name": "currency", "type": "string"},
                {"name": "itemsHash", "type": "bytes32"},
                {"name": "ttlSeconds", "type": "uint256"},
                {"name": "issuedAt", "type": "uint256"},
                {"name": "authorization", "type": "Authorization"},
            ],
            "PaymentMandate": [
                {"name": "signer", "type": "address"},
                {"name": "amount", "type": "uint256"},
                {"name": "currency", "type": "string"},
                {"name": "recipient", "type": "address"},
                {"name": "nonce", "type": "bytes32"},
                {"name": "authorization", "type": "Authorization"},
            ],
        }
        types = {"Authorization": AUTHORIZATION_TYPE, primary: field_lists[primary]}
        digest, signature, _ = sign_typed(ap2_domain, types, message, primary)
        print(f"{primary} digest {digest}")
        print(f"{primary} signature {signature}")


if __name__ == "__main__":
    main()
