import importlib.util
import json
import subprocess
import sys
import types
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("ap2_demo.py")
requests_stub = types.ModuleType("requests")
requests_stub.get = mock.Mock()
requests_stub.post = mock.Mock()
dotenv_stub = types.ModuleType("dotenv")
dotenv_stub.load_dotenv = mock.Mock()
eth_account_stub = types.ModuleType("eth_account")
eth_account_stub.Account = mock.Mock()
eth_messages_stub = types.ModuleType("eth_account.messages")
eth_messages_stub.encode_typed_data = mock.Mock()
sys.modules.setdefault("requests", requests_stub)
sys.modules.setdefault("dotenv", dotenv_stub)
sys.modules.setdefault("eth_account", eth_account_stub)
sys.modules.setdefault("eth_account.messages", eth_messages_stub)
SPEC = importlib.util.spec_from_file_location("ap2_demo", MODULE_PATH)
ap2_demo = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(ap2_demo)


class CastTxTests(unittest.TestCase):
    @mock.patch.object(ap2_demo.subprocess, "run")
    def test_cast_tx_uses_async_send(self, run_mock):
        run_mock.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout=json.dumps({"transactionHash": "0xabc"}),
            stderr="",
        )

        tx_hash = ap2_demo.cast_tx("0xkey", "0xto", "submit()", "1")

        self.assertEqual(tx_hash, "0xabc")
        cmd = run_mock.call_args.args[0]
        self.assertIn("--async", cmd)


class WaitForReceiptTests(unittest.TestCase):
    @mock.patch.object(ap2_demo.subprocess, "run")
    def test_wait_for_receipt_uses_caller_timeout_for_cast(self, run_mock):
        run_mock.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout=json.dumps({"status": "0x1"}),
            stderr="",
        )

        receipt = ap2_demo.wait_for_receipt("0xabc", timeout_seconds=42)

        self.assertEqual(receipt["status"], "0x1")
        self.assertEqual(run_mock.call_count, 1)
        self.assertEqual(run_mock.call_args.kwargs["timeout"], 42)


if __name__ == "__main__":
    unittest.main()
