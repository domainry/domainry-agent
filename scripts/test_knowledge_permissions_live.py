import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("permission_probe", Path(__file__).with_name("test-agent-knowledge-permissions-live.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class PermissionProbeConfigurationTest(unittest.TestCase):
    def setUp(self):
        self.config = {"AGENT_KNOWLEDGE_BASE_URL": "https://api.verdent.ai", "AGENT_KNOWLEDGE_TEAM_ID": "team", "AGENT_KNOWLEDGE_KB_ID": "kb"}
        self.public = {"origin": "https://api.verdent.ai", "team_id": "team", "kb_id": "kb", "doc_id": "public", "marker": "PUBLIC", "preflight_missing": True, "content_readable": True}
        self.private = {"origin": "https://api.verdent.ai", "team_id": "team", "kb_id": "kb", "doc_id": "private", "marker": "PRIVATE", "permission_id": "reader:fixture"}

    def test_public_only_cannot_claim_private_acceptance(self):
        self.assertEqual(probe.prepare_probe(self.config, self.public), {"public_document": "public", "public_marker": "PUBLIC"})
        self.assertEqual(probe.prepare_probe(self.config, self.public, self.private)["allowed_permission"], "reader:fixture")

    def test_rejects_wrong_source_and_unusable_controls(self):
        for field, value in (("kb_id", "other"), ("team_id", "other"), ("origin", "https://other.example"), ("marker", ""), ("permission_id", ""), ("doc_id", "public")):
            with self.subTest(field=field), self.assertRaises(ValueError):
                probe.prepare_probe(self.config, self.public, dict(self.private, **{field: value}))
        for field, value in (("cleanup_verified", True), ("content_readable", False), ("preflight_missing", False), ("visibility", "private"), ("permission_ids", ["private"])):
            with self.subTest(field=field), self.assertRaises(ValueError):
                probe.prepare_probe(self.config, dict(self.public, **{field: value}), self.private)
        for field, value in (("cleanup_verified", True), ("content_readable", False), ("visibility", "public")):
            with self.subTest(private_field=field), self.assertRaises(ValueError):
                probe.prepare_probe(self.config, self.public, dict(self.private, **{field: value}))


if __name__ == "__main__":
    unittest.main()
