import os
import threading
import unittest

from oaiso import APIError, Client

BINARY = os.environ.get("OAISO_SDK_TEST_BINARY")
SECRET = "fixture-private-secret-must-never-be-serialized"


class ClientContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not BINARY:
            raise RuntimeError("run scripts/verify-local-sdks.py with its Go fixture binary")

    def client(self):
        client = Client({"ProxyAdminSecret": SECRET}, binary=BINARY)
        self.addCleanup(client.close)
        return client

    def test_lifecycle_replay_conflict_and_cleanup(self):
        c = self.client()
        self.assertEqual(c.capabilities["protocol_version"], 1)
        self.assertIn("resource_usage", c.capabilities["methods"])
        request = {"RequestID": "python/create-1", "ProjectDir": "/workspace", "Profile": "compact", "NoInternet": True}
        w = c.create(request)
        self.assertEqual(c.create(request)["ID"], w["ID"])
        self.assertEqual(len(c.list()), 1)
        with self.assertRaises(APIError) as caught:
            c.create({**request, "Image": "different"})
        self.assertEqual(caught.exception.code, "conflict")
        self.assertFalse(caught.exception.requires_reconciliation)
        c.start(w["ID"])
        self.assertEqual(c.get(w["ID"])["Status"], "running")
        self.assertEqual(c.request(request["RequestID"])["WorkerID"], w["ID"])
        self.assertGreater(c.profiles()[0]["HostMemoryBytes"], 0)
        self.assertEqual(c.resource_policies()[0]["pid_limit"], 256)
        self.assertEqual(c.resource_usage(w["ID"])["Worker"], "fixture")
        self.assertTrue(c.proxy_capabilities()["WorkerMTLS"])
        result = c.exec(w["ID"], ["echo"], stdin=b"\x00\xffpayload")
        self.assertEqual(result["exit_code"], 37)
        self.assertEqual(result["stdout"], b"\x00\xffpayload")
        self.assertEqual(result["stderr"], b"fixture-stderr")
        self.assertEqual(c.wait(w["ID"])["exit_code"], 37)
        c.stop(w["ID"], 0)
        c.quarantine(w["ID"])
        c.remove_request(request["RequestID"], True)
        c.remove_request(request["RequestID"], True)
        self.assertEqual(c.list(), [])
        with self.assertRaises(APIError) as caught:
            c.create(request)
        self.assertEqual(caught.exception.code, "request_gone")

    def test_stdin_absent_and_explicit_eof_are_distinct(self):
        c = self.client()
        self.assertEqual(c.exec("worker", ["stdin-mode"])["stdout"], b"absent")
        self.assertEqual(c.exec("worker", ["stdin-mode"], stdin=b"")["stdout"], b"empty")
        self.assertEqual(c.exec("worker", ["stdin-mode"], stdin=b"data")["stdout"], b"present")

    def test_lost_result_can_be_reconciled_without_duplicate(self):
        c = self.client()
        req = {"RequestID": "lost-result", "ProjectDir": "/workspace"}
        with self.assertRaises(APIError) as caught:
            c.launch(req)
        e = caught.exception
        self.assertEqual(e.code, "outcome_unknown")
        self.assertTrue(e.requires_reconciliation)
        self.assertEqual(e.partial_result["ID"], "lost-result")
        self.assertNotIn(SECRET, str(e) + repr(e))
        self.assertEqual(c.request("lost-result")["State"], "complete")
        self.assertEqual(c.launch(req)["ID"], "lost-result")
        self.assertEqual(len(c.list()), 1)
        c.remove_request("lost-result", True)

    def test_cancel_and_wait_semantics(self):
        c = self.client()
        call = c.submit("launch", {"request": {"RequestID": "block", "ProjectDir": "/workspace"}})
        self.assertTrue(call.cancel().result(3)["accepted"])
        with self.assertRaises(APIError) as caught:
            call.result(3)
        self.assertEqual(caught.exception.code, "canceled")
        self.assertTrue(caught.exception.requires_reconciliation)
        if caught.exception.partial_result is not None:
            self.assertEqual(caught.exception.partial_result["ID"], "partial-worker")
        waiting = c.submit("wait", {"worker_id": "block"})
        waiting.cancel().result(3)
        with self.assertRaises(APIError) as caught:
            waiting.result(3)
        self.assertFalse(caught.exception.requires_reconciliation)
        self.assertTrue(c.profiles())

    def test_deadline_output_limit_and_redaction(self):
        c = self.client()
        with self.assertRaises(APIError) as caught:
            c.exec("worker", ["block"], timeout_ms=10)
        self.assertEqual(caught.exception.code, "deadline_exceeded")
        self.assertTrue(caught.exception.requires_reconciliation)
        with self.assertRaises(APIError) as caught:
            c.exec("worker", ["overflow"])
        self.assertIn("output_limit", caught.exception.codes)
        self.assertTrue(caught.exception.requires_reconciliation)
        with self.assertRaises(APIError) as caught:
            c.exec("worker", ["private-error"], env=["PRIVATE=" + SECRET])
        self.assertNotIn(SECRET, str(caught.exception) + repr(caught.exception))
        # No secret is present in the subprocess command line.
        self.assertEqual(c._process.args, [BINARY, "api", "stdio"])

    def test_version_failure_is_classified(self):
        with self.assertRaises(APIError) as caught:
            Client({"NodeSocket": "/version-mismatch", "ProxyAdminSecret": SECRET}, binary=BINARY)
        self.assertEqual(caught.exception.code, "version_mismatch")
        self.assertNotIn(SECRET, str(caught.exception))

    def test_transport_loss_during_mutation_never_retries(self):
        c = self.client()
        call = c.submit("exec", {"worker_id": "worker", "argv": ["block"]})
        c._process.kill()
        with self.assertRaises(APIError) as caught:
            call.result(3)
        self.assertEqual(caught.exception.code, "transport_lost")
        self.assertTrue(caught.exception.requires_reconciliation)

    def test_concurrent_calls_keep_correlation(self):
        c = self.client()
        failures = []
        def invoke(i):
            try:
                request = {"RequestID": f"thread-{i}", "ProjectDir": "/workspace"}
                self.assertEqual(c.create(request)["ID"], request["RequestID"])
            except BaseException as e:
                failures.append(e)
        threads = [threading.Thread(target=invoke, args=(i,)) for i in range(8)]
        for t in threads: t.start()
        for t in threads: t.join(5)
        self.assertFalse(failures)
        self.assertEqual(len(c.list()), 8)


if __name__ == "__main__":
    unittest.main()
