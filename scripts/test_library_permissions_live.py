import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("library_permission_probe", Path(__file__).with_name("test-agent-library-permissions-live.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class LibraryPermissionFixtureTest(unittest.TestCase):
    def setUp(self):
        self.config = {"AGENT_KNOWLEDGE_BASE_URL": "https://api.verdent.ai", "AGENT_KNOWLEDGE_TEAM_ID": "team"}
        self.fixtures = []
        for number in range(3):
            permission = "scope:domainry-k01:" + str(number)*32 + ":library:read"
            self.fixtures.append({"origin": self.config["AGENT_KNOWLEDGE_BASE_URL"], "team_id": "team", "kb_id": "kb-"+str(number), "doc_id": "domainry-agent-acceptance-20260911-"+str(number)*12, "marker": "QINGHE-"+str(number)*12, "permission_id": permission, "permission_ids": [permission], "visibility": "private", "preflight_missing": True, "content_readable": True, "cleanup_verified": False})

    def test_only_test_scope_metadata_is_exported(self):
        self.fixtures[0]["content"] = "synthetic original file excluded from environment"
        result = probe.prepare_fixtures(self.config, self.fixtures)
        self.assertEqual(len(result), 3)
        self.assertEqual(set(result[0]), {"kb_id", "doc_id", "marker", "permission_id"})

    def test_rejects_unowned_unready_public_or_shared_scopes(self):
        for field, value in (("origin", "https://elsewhere.example"), ("team_id", "other"), ("kb_id", "kb-1"), ("permission_ids", []), ("visibility", "public"), ("preflight_missing", False), ("content_readable", False), ("cleanup_verified", True), ("permission_id", " reader "), ("doc_id", "business-document")):
            with self.subTest(field=field), self.assertRaises(ValueError):
                probe.prepare_fixtures(self.config, [{**self.fixtures[0], field:value}, *self.fixtures[1:]])


if __name__ == "__main__":
    unittest.main()
