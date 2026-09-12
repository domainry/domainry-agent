#!/usr/bin/env python3
"""Read-only real Connector permission probe; private acceptance needs a fixture."""
import argparse
import json
import os
from pathlib import Path
import sys
import tempfile

from agent_private_configuration import load_service_credentials


def prepare_probe(config, public, private=None):
    scope = {"origin": "AGENT_KNOWLEDGE_BASE_URL", "team_id": "AGENT_KNOWLEDGE_TEAM_ID", "kb_id": "AGENT_KNOWLEDGE_KB_ID"}
    if config.get("AGENT_KNOWLEDGE_BASE_URL") != "https://api.verdent.ai":
        raise ValueError("The verified Verdent origin is required")
    for label, item in (("public control", public), ("private fixture", private)):
        if item is None:
            continue
        if not isinstance(item, dict) or any(not isinstance(item.get(k), str) or not item[k].strip() or item[k] != config.get(v) for k, v in scope.items()):
            raise ValueError(label + " does not match the configured source")
        for name in ("doc_id", "marker"):
            if not isinstance(item.get(name), str) or not item[name].strip() or len(item[name]) > 256 or any(c in item[name] for c in "\r\n\0"):
                raise ValueError(label + " requires an explicit test " + name)
    if public.get("preflight_missing") is not True or public.get("content_readable") is not True or public.get("cleanup_verified") is True:
        raise ValueError("The public synthetic document must be indexed and not cleaned up")
    if public.get("visibility", "public") != "public" or public.get("permission_ids"):
        raise ValueError("A private document cannot serve as the public control")
    spec = {"public_document": public["doc_id"], "public_marker": public["marker"]}
    if private is not None:
        if private.get("visibility", "private") != "private" or private.get("cleanup_verified") is True or private.get("content_readable") is False:
            raise ValueError("The private fixture must be indexed and not cleaned up")
        permission = private.get("permission_id")
        if not isinstance(permission, str) or not permission.strip() or len(permission) > 256 or any(c in permission for c in "\r\n\0"):
            raise ValueError("Private fixture requires its correct permission_id")
        if private["doc_id"] == public["doc_id"] or private["marker"] == public["marker"]:
            raise ValueError("Public and private fixtures must differ")
        spec.update(private_document=private["doc_id"], private_marker=private["marker"], allowed_permission=permission)
    return spec


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("public_manifest", type=Path, help="indexed synthetic document made by knowledge-live-document.py")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--private-fixture", type=Path, help="JSON with origin/team_id/kb_id/doc_id/marker/permission_id for a dedicated private test document")
    mode.add_argument("--public-control-only", action="store_true", help="verify public defaults only; does not complete K01/private ACL acceptance")
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
    public = json.loads(args.public_manifest.read_text())
    private = json.loads(args.private_fixture.read_text()) if args.private_fixture else None
    spec = prepare_probe(env, public, private)
    if not (env.get("AGENT_KNOWLEDGE_API_KEY") or env.get("AGENT_PROVIDER_API_KEY")):
        raise ValueError("Configure the existing knowledge credential locally")
    env["AGENT_KNOWLEDGE_PERMISSION_PROBE_LIVE"] = "1"
    env["AGENT_KNOWLEDGE_PERMISSION_PROBE_SPEC"] = json.dumps(spec)
    env["AGENT_LIVE_EVIDENCE_DIR"] = tempfile.mkdtemp(prefix="domainry-knowledge-permission-probe-")
    print("Read-only permission evidence: " + env["AGENT_LIVE_EVIDENCE_DIR"], flush=True)
    if private is None:
        print("Scope: public controls only; private-document ACL is NOT verified.", flush=True)
    os.chdir(Path(__file__).resolve().parents[1])
    os.execvpe("go", ["go", "test", "./internal/infrastructure/provider", "-run", "^TestLiveKnowledgePermissionProbe$", "-count=1", "-v", "-timeout=10m"], env)


if __name__ == "__main__":
    main()
