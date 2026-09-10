#!/usr/bin/env python3
"""Opt-in real-model daily-work acceptance with a temporary Identity database."""
import getpass
from agent_private_configuration import load_service_credentials
import json
import os
from pathlib import Path
import sys
import tempfile

root = Path(__file__).resolve().parents[1]
default_dir = (Path.home() / "Library/Application Support/domainry-agent"
               if sys.platform == "darwin" else Path.home() / ".config/domainry-agent")
path = Path(os.environ.get("AGENT_WEB_SERVICES_CONFIG", default_dir / "web-services.json")).expanduser()
env = dict(os.environ)
load_service_credentials(env)
if path.exists():
    values = json.loads(path.read_text())
    for name in ("AGENT_CONVERSATION_PROVIDER", "AGENT_CONVERSATION_PROTOCOL",
                 "AGENT_CONVERSATION_BASE_URL", "AGENT_CONVERSATION_MODEL_URL",
                 "AGENT_CONVERSATION_MODEL"):
        if name in values:
            if not isinstance(values[name], str):
                sys.exit("Model settings must be strings: " + name)
            env.setdefault(name, values[name])
env.setdefault("AGENT_CONVERSATION_PROVIDER", "gateway")
env.setdefault("AGENT_CONVERSATION_PROTOCOL", "chat_completions")
env.setdefault("AGENT_CONVERSATION_MODEL", "glm-5.3-flash-free")
if not (env.get("AGENT_CONVERSATION_MODEL_URL") or env.get("AGENT_CONVERSATION_BASE_URL")):
    sys.exit("Configure the model URL in AGENT_WEB_SERVICES_CONFIG or the process environment.")
if not (env.get("AGENT_PROVIDER_API_KEY") or env.get("AGENT_CONVERSATION_MODEL_API_KEY")):
    if not sys.stdin.isatty():
        sys.exit("Configure the model credential in the process environment.")
    env["AGENT_CONVERSATION_MODEL_API_KEY"] = getpass.getpass("Model API key (not saved): ")
if sys.argv[1:] not in ([], ["--browser"]):
    sys.exit("Usage: python3 scripts/test-agent-live.py [--browser]")
env["AGENT_DAILY_WORK_LIVE"] = "1"
if not env.get("AGENT_LIVE_EVIDENCE_DIR"):
    env["AGENT_LIVE_EVIDENCE_DIR"] = tempfile.mkdtemp(prefix="domainry-agent-live-")
else:
    Path(env["AGENT_LIVE_EVIDENCE_DIR"]).mkdir(parents=True, exist_ok=True, mode=0o700)
print("Synthetic work execution evidence: " + env["AGENT_LIVE_EVIDENCE_DIR"], flush=True)
if "--browser" in sys.argv[1:]:
    env["AGENT_TOOL_UI_ACCEPTANCE"] = "1"
os.chdir(root)
os.execvpe("go", ["go", "test", "./internal/assembly/web", "-run",
                  "^TestLiveDailyWorkThroughIdentityHTTP$", "-count=1", "-v", "-timeout=45m"], env)
