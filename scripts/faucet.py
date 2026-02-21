"""Request testnet funds from the Coinbase Developer Platform faucet.

Requires CDP API credentials in .env (see .env.example).
Install: uv venv .venv && uv pip install cdp-sdk python-dotenv

Usage:
    # Fund an address with ETH (default: DEPLOYER_ADDRESS from .env)
    uv run scripts/faucet.py

    # Fund a specific address
    uv run scripts/faucet.py --address 0x1234...

    # Request USDC instead of (or in addition to) ETH
    uv run scripts/faucet.py --token usdc
    uv run scripts/faucet.py --token eth --token usdc

    # Multiple claims to accumulate more ETH (rate-limited to ~10/burst)
    uv run scripts/faucet.py --claims 10
"""

import argparse
import asyncio
import os
import sys

from dotenv import load_dotenv

load_dotenv()


async def main():
    parser = argparse.ArgumentParser(description="Request Base Sepolia testnet funds via CDP faucet")
    parser.add_argument("--address", default=os.getenv("DEPLOYER_ADDRESS"), help="Target address (default: DEPLOYER_ADDRESS from .env)")
    parser.add_argument("--token", action="append", default=None, help="Token(s) to request: eth, usdc, cbbtc (default: eth)")
    parser.add_argument("--network", default="base-sepolia", help="Target network (default: base-sepolia)")
    parser.add_argument("--claims", type=int, default=1, help="Number of claims per token (default: 1)")
    args = parser.parse_args()

    if not args.address:
        print("Error: no address provided. Set DEPLOYER_ADDRESS in .env or pass --address.", file=sys.stderr)
        sys.exit(1)

    tokens = args.token or ["eth"]

    for var in ("CDP_API_KEY_ID", "CDP_API_KEY_SECRET", "CDP_WALLET_SECRET"):
        if not os.getenv(var):
            print(f"Error: {var} not set. Add it to .env (see .env.example).", file=sys.stderr)
            sys.exit(1)

    from cdp import CdpClient

    cdp = CdpClient()

    print(f"Target:  {args.address}")
    print(f"Network: {args.network}")
    print(f"Tokens:  {', '.join(tokens)}")
    print(f"Claims:  {args.claims} per token")
    print()

    for token in tokens:
        succeeded = 0
        for i in range(args.claims):
            try:
                tx_hash = await cdp.evm.request_faucet(
                    address=args.address,
                    network=args.network,
                    token=token,
                )
                succeeded += 1
                print(f"  {token.upper()} claim {i+1}: https://sepolia.basescan.org/tx/{tx_hash}")
            except Exception as e:
                print(f"  {token.upper()} claim {i+1} failed: {e}")
                break
        print(f"  {token.upper()}: {succeeded}/{args.claims} claims succeeded")
        print()

    await cdp.close()


if __name__ == "__main__":
    asyncio.run(main())
