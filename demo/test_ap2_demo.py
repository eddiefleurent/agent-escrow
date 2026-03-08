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
ORIGINAL_MODULES = {
    "requests": sys.modules.get("requests"),
    "dotenv": sys.modules.get("dotenv"),
    "eth_account": sys.modules.get("eth_account"),
    "eth_account.messages": sys.modules.get("eth_account.messages"),
}
sys.modules["requests"] = requests_stub
sys.modules["dotenv"] = dotenv_stub
sys.modules["eth_account"] = eth_account_stub
sys.modules["eth_account.messages"] = eth_messages_stub
SPEC = importlib.util.spec_from_file_location("ap2_demo", MODULE_PATH)
ap2_demo = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(ap2_demo)


def tearDownModule():
    for name, original in ORIGINAL_MODULES.items():
        if original is None:
            sys.modules.pop(name, None)
        else:
            sys.modules[name] = original


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

    @mock.patch.object(ap2_demo.time, "sleep")
    @mock.patch.object(ap2_demo.subprocess, "run")
    def test_cast_tx_exits_without_retry_when_hash_missing_after_success(self, run_mock, sleep_mock):
        run_mock.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout=json.dumps({"status": "ok"}),
            stderr="",
        )

        with self.assertRaises(SystemExit):
            ap2_demo.cast_tx("0xkey", "0xto", "submit()", "1")

        self.assertEqual(run_mock.call_count, 1)
        sleep_mock.assert_not_called()

    @mock.patch.object(ap2_demo.time, "sleep")
    @mock.patch.object(ap2_demo.subprocess, "run")
    def test_cast_tx_exits_without_retry_on_timeout(self, run_mock, sleep_mock):
        run_mock.side_effect = subprocess.TimeoutExpired(cmd=["cast"], timeout=30)

        with self.assertRaises(SystemExit):
            ap2_demo.cast_tx("0xkey", "0xto", "submit()", "1")

        self.assertEqual(run_mock.call_count, 1)
        sleep_mock.assert_not_called()


class AddressEnvTests(unittest.TestCase):
    @mock.patch.object(ap2_demo, "require_env", return_value="  0xABABABABABABABABABABABABABABABABABABABAB  ")
    def test_require_address_env_normalizes_hex_address(self, require_env_mock):
        addr = ap2_demo.require_address_env("VERIFIER_ADDR")

        self.assertEqual(addr, "0xabababababababababababababababababababab")
        require_env_mock.assert_called_once_with("VERIFIER_ADDR")

    @mock.patch.object(ap2_demo, "require_env", return_value="not-an-address")
    def test_require_address_env_exits_on_invalid_address(self, require_env_mock):
        with self.assertRaises(SystemExit):
            ap2_demo.require_address_env("ARBITRATOR_ADDR")

        require_env_mock.assert_called_once_with("ARBITRATOR_ADDR")


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

    @mock.patch.object(ap2_demo.subprocess, "run")
    def test_wait_for_receipt_allows_stderr_when_cast_succeeds(self, run_mock):
        run_mock.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout=json.dumps({"status": "0x1"}),
            stderr="warning: noisy rpc",
        )

        receipt = ap2_demo.wait_for_receipt("0xabc", timeout_seconds=42)

        self.assertEqual(receipt["status"], "0x1")
        self.assertEqual(run_mock.call_args.kwargs["timeout"], 42)

    @mock.patch.object(ap2_demo.subprocess, "run")
    def test_wait_for_receipt_exits_on_nonzero_returncode(self, run_mock):
        run_mock.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=1,
            stdout="{}",
            stderr="boom",
        )

        with self.assertRaises(SystemExit):
            ap2_demo.wait_for_receipt("0xdead", timeout_seconds=17)

        self.assertEqual(run_mock.call_count, 1)
        self.assertEqual(run_mock.call_args.kwargs["timeout"], 17)


if __name__ == "__main__":
    unittest.main()
