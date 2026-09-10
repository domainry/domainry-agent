#!/usr/bin/env python3
"""Read an indexed synthetic acceptance document with the real model and Identity."""
import argparse
import getpass
from agent_private_configuration import load_service_credentials
import json
import os
from pathlib import Path
import sys
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("manifest", type=Path, help="manifest of the synthetic document prepared for acceptance")
parser.add_argument("--browser", action="store_true")
parser.add_argument("--expect-deleted", action="store_true", help="assert document deletion after browser acceptance; cleanup is performed separately")
args = parser.parse_args()
if args.expect_deleted and not args.browser:
    parser.error("--expect-deleted requires --browser")
root = Path(__file__).resolve().parents[1]
default_dir = Path.home() / ("Library/Application Support/domainry-agent" if sys.platform == "darwin" else ".config/domainry-agent")
config_path = Path(os.environ.get("AGENT_WEB_SERVICES_CONFIG", default_dir / "web-services.json")).expanduser()
env = dict(os.environ)
load_service_credentials(env)
if config_path.exists():
    config = json.loads(config_path.read_text())
    for name in ("AGENT_CONVERSATION_PROVIDER","AGENT_CONVERSATION_PROTOCOL","AGENT_CONVERSATION_BASE_URL","AGENT_CONVERSATION_MODEL_URL","AGENT_CONVERSATION_MODEL","AGENT_KNOWLEDGE_BASE_URL","AGENT_KNOWLEDGE_TEAM_ID","AGENT_KNOWLEDGE_KB_ID"):
        if name in config:
            if not isinstance(config[name],str): sys.exit("Service settings must be strings: "+name)
            env.setdefault(name,config[name])
manifest = json.loads(args.manifest.read_text())
if not manifest.get("content_readable") or not manifest.get("preflight_missing") or manifest.get("cleanup_verified"):
    sys.exit("The synthetic document must be indexed and not yet cleaned up.")
for field,name in (("origin","AGENT_KNOWLEDGE_BASE_URL"),("team_id","AGENT_KNOWLEDGE_TEAM_ID"),("kb_id","AGENT_KNOWLEDGE_KB_ID")):
    if manifest.get(field) != env.get(name): sys.exit("Manifest does not match configured knowledge scope: "+field)
if not (env.get("AGENT_PROVIDER_API_KEY") or env.get("AGENT_CONVERSATION_MODEL_API_KEY")):
    if not sys.stdin.isatty(): sys.exit("Configure the model credential in the environment.")
    env["AGENT_CONVERSATION_MODEL_API_KEY"] = getpass.getpass("Model API key (not saved): ")
if not (env.get("AGENT_PROVIDER_API_KEY") or env.get("AGENT_KNOWLEDGE_API_KEY")):
    if not sys.stdin.isatty(): sys.exit("Configure the knowledge credential in the environment.")
    env["AGENT_KNOWLEDGE_API_KEY"] = getpass.getpass("Knowledge API key (not saved): ")
env["AGENT_KNOWLEDGE_LIVE"] = "1"
env["AGENT_KNOWLEDGE_LIVE_DOC_ID"] = manifest["doc_id"]
env["AGENT_KNOWLEDGE_LIVE_MARKER"] = manifest["marker"]
env["AGENT_KNOWLEDGE_RESPONSE_MAPPING"] = json.dumps({
    "search":{"items":"/data/hits","many":True,"doc_id":"/doc_id","title":"/title","url":"/source_url","excerpt":"/snippet"},
    "fetch":{"items":"/data/chunks","many":True,"metadata_object":"/data","doc_id":"/doc_id","title":"/title","url":"/source/source_url","excerpt":"/content"}
})
env["AGENT_LIVE_EVIDENCE_DIR"] = tempfile.mkdtemp(prefix="domainry-knowledge-agent-live-")
print("Synthetic knowledge evidence: "+env["AGENT_LIVE_EVIDENCE_DIR"],flush=True)
if args.browser: env["AGENT_TOOL_UI_ACCEPTANCE"] = "1"
if args.expect_deleted: env["AGENT_KNOWLEDGE_LIVE_EXPECT_DELETED"] = "1"
os.chdir(root)
os.execvpe("go",["go","test","./internal/assembly/web","-run","^TestLiveKnowledgeThroughIdentityHTTP$","-count=1","-v","-timeout=25m"],env)
