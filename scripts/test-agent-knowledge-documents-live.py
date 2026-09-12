#!/usr/bin/env python3
"""Exercise the actual Connector document lifecycle using a new synthetic file."""
import argparse
import json
import os
from pathlib import Path
import sys
import tempfile

from agent_private_configuration import load_service_credentials

parser = argparse.ArgumentParser()
parser.add_argument("--live", action="store_true", required=True, help="explicitly upload, inspect and delete one new synthetic document")
parser.add_argument("--delete-recovery", action="store_true", help="use a private fixture, lose the first real DELETE response and recover the same deletion")
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
directory = Path.home() / ("Library/Application Support/domainry-agent" if sys.platform == "darwin" else ".config/domainry-agent")
config_path = Path(os.environ.get("AGENT_WEB_SERVICES_CONFIG", directory / "web-services.json")).expanduser()
env = dict(os.environ)
load_service_credentials(env)
if config_path.exists():
    config = json.loads(config_path.read_text())
    for name in ("AGENT_KNOWLEDGE_BASE_URL", "AGENT_KNOWLEDGE_TEAM_ID", "AGENT_KNOWLEDGE_KB_ID"):
        if name in config:
            if not isinstance(config[name], str):
                sys.exit("Service settings must be strings: " + name)
            env.setdefault(name, config[name])
if not (env.get("AGENT_KNOWLEDGE_API_KEY") or env.get("AGENT_PROVIDER_API_KEY")):
    sys.exit("Configure the knowledge service credential locally before running live acceptance.")
env["AGENT_KNOWLEDGE_DOCUMENTS_LIVE"] = "1"
if args.delete_recovery:
    env["AGENT_KNOWLEDGE_DELETE_RECOVERY_LIVE"] = "1"
env["AGENT_LIVE_EVIDENCE_DIR"] = tempfile.mkdtemp(prefix="domainry-knowledge-document-lifecycle-")
print("Document lifecycle evidence: " + env["AGENT_LIVE_EVIDENCE_DIR"], flush=True)
os.chdir(root)
os.execvpe("go", ["go", "test", "./internal/infrastructure/provider", "-run", "^TestLiveKnowledgeDocumentLifecycle$", "-count=1", "-v", "-timeout=7m"], env)
