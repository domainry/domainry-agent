#!/usr/bin/env python3
"""Run real Identity/library authorization against three private knowledge fixtures."""
import argparse
import json
import os
from pathlib import Path
import re
import sys
import tempfile

from agent_private_configuration import load_service_credentials


def prepare_fixtures(config, manifests):
    if config.get("AGENT_KNOWLEDGE_BASE_URL") != "https://api.verdent.ai" or not config.get("AGENT_KNOWLEDGE_TEAM_ID") or len(manifests) != 3:
        raise ValueError("Three private fixtures on the verified team/origin are required")
    fixtures, seen = [], set()
    for manifest in manifests:
        if not isinstance(manifest, dict) or manifest.get("origin") != config["AGENT_KNOWLEDGE_BASE_URL"] or manifest.get("team_id") != config["AGENT_KNOWLEDGE_TEAM_ID"]:
            raise ValueError("Fixture does not match the configured knowledge team")
        if manifest.get("visibility") != "private" or manifest.get("preflight_missing") is not True or manifest.get("content_readable") is not True or manifest.get("cleanup_verified") is True:
            raise ValueError("Each fixture must be a private indexed synthetic document awaiting cleanup")
        item = {name: manifest.get(name) for name in ("kb_id", "doc_id", "marker", "permission_id")}
        if any(not isinstance(value, str) or not value or value.strip() != value or len(value.encode("utf-8")) > 128 or any(ord(c) < 32 or ord(c) == 127 for c in value) for value in item.values()):
            raise ValueError("Invalid fixture identifier")
        if not re.fullmatch(r"domainry-agent-acceptance-\d{8}-[a-f0-9]{12}", item["doc_id"]) or not re.fullmatch(r"QINGHE-[A-F0-9]{12}", item["marker"]) or not re.fullmatch(r"scope:domainry-k01:[a-f0-9]{32}:library:read", item["permission_id"]):
            raise ValueError("Only dedicated synthetic permission fixtures are supported")
        if item["kb_id"] in seen or manifest.get("permission_ids") != [item["permission_id"]]:
            raise ValueError("Private libraries must use distinct KBs and their recorded permission IDs")
        seen.add(item["kb_id"])
        fixtures.append(item)
    return fixtures


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("personal_a", type=Path)
    parser.add_argument("personal_b", type=Path)
    parser.add_argument("shared", type=Path)
    args = parser.parse_args()
    directory = Path.home() / ("Library/Application Support/domainry-agent" if sys.platform == "darwin" else ".config/domainry-agent")
    config_path = Path(os.environ.get("AGENT_WEB_SERVICES_CONFIG", directory / "web-services.json")).expanduser()
    settings = json.loads(config_path.read_text()) if config_path.exists() else {}
    env = dict(os.environ)
    load_service_credentials(env)
    for name in ("AGENT_KNOWLEDGE_BASE_URL", "AGENT_KNOWLEDGE_TEAM_ID", "AGENT_KNOWLEDGE_KB_ID"):
        if name in settings:
            if not isinstance(settings[name], str):
                raise ValueError("Service settings must be strings")
            env.setdefault(name, settings[name])
    fixtures = prepare_fixtures(env, [json.loads(path.read_text()) for path in (args.personal_a, args.personal_b, args.shared)])
    if not (env.get("AGENT_KNOWLEDGE_API_KEY") or env.get("AGENT_PROVIDER_API_KEY")):
        raise ValueError("Configure the existing knowledge credential locally")
    env["AGENT_LIBRARY_PERMISSIONS_LIVE"] = "1"
    env["AGENT_LIBRARY_PERMISSIONS_FIXTURES"] = json.dumps(fixtures)
    env["AGENT_LIVE_EVIDENCE_DIR"] = tempfile.mkdtemp(prefix="domainry-library-permissions-live-")
    print("Private library evidence: " + env["AGENT_LIVE_EVIDENCE_DIR"], flush=True)
    print("Real Identity/HTTP/SQLite and Verdent; deterministic model for reproducible permission attacks.", flush=True)
    os.chdir(Path(__file__).resolve().parents[1])
    os.execvpe("go", ["go", "test", "./internal/assembly/web", "-run", "^TestLiveLibraryPrivatePermissionsIdentityHTTP$", "-count=1", "-v", "-timeout=15m"], env)


if __name__ == "__main__":
    main()
