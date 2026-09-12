#!/usr/bin/env python3
"""Start the local Identity + Agent module host with persistent private secrets."""
import getpass
from agent_private_configuration import load_service_credentials
import json
import os
from pathlib import Path
import secrets
import sys

root = Path(__file__).resolve().parents[1]
default_dir = (
    Path.home() / "Library/Application Support/domainry-agent"
    if sys.platform == "darwin"
    else Path.home() / ".config/domainry-agent"
)
config_path = Path(os.environ.get("AGENT_WEB_IDENTITY_CONFIG", default_dir / "web-identity.json")).expanduser()
config_path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
if not config_path.exists():
    config = {
        "AUTH_JWT_SECRET": secrets.token_urlsafe(48),
        "IDENTITY_DATA_SECRET_KEY": secrets.token_urlsafe(48),
        "AUTH_DEFAULT_PASSWORD": secrets.token_urlsafe(24) + "aA1!",
    }
    with os.fdopen(os.open(config_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "w") as handle:
        json.dump(config, handle)
config = json.loads(config_path.read_text())
env = dict(os.environ)
load_service_credentials(env)
services_path = Path(os.environ.get("AGENT_WEB_SERVICES_CONFIG", default_dir / "web-services.json")).expanduser()
if services_path.exists():
    services = json.loads(services_path.read_text())
    for name in (
        "AGENT_CONVERSATION_PROVIDER", "AGENT_CONVERSATION_PROTOCOL",
        "AGENT_CONVERSATION_BASE_URL", "AGENT_CONVERSATION_MODEL_URL", "AGENT_CONVERSATION_MODEL",
        "AGENT_KNOWLEDGE_BASE_URL", "AGENT_KNOWLEDGE_TEAM_ID", "AGENT_KNOWLEDGE_KB_ID",
        "AGENT_KNOWLEDGE_WORKSPACE_ID", "AGENT_KNOWLEDGE_TOP_K",
        "AGENT_WEB_KNOWLEDGE_PERMISSIONS",
        "AGENT_KNOWLEDGE_RESPONSE_MAPPING",
        "AGENT_KNOWLEDGE_LIBRARY_BINDINGS", "AGENT_KNOWLEDGE_DATASOURCES",
        "AGENT_ATTACHMENT_KNOWLEDGE_BINDINGS",
    ):
        if name in services:
            if not isinstance(services[name], str):
                sys.exit("Service configuration values must be strings: " + name)
            env.setdefault(name, services[name])
for name in ("AUTH_JWT_SECRET", "IDENTITY_DATA_SECRET_KEY", "AUTH_DEFAULT_PASSWORD"):
    env.setdefault(name, config[name])
env.setdefault("AUTH_ACCESS_TTL", "15m")
env.setdefault("AGENT_CONVERSATION_PROVIDER", "gateway")
env.setdefault("AGENT_CONVERSATION_PROTOCOL", "chat_completions")
env.setdefault("AGENT_CONVERSATION_MODEL", "glm-5.3-flash-free")
if not (env.get("AGENT_CONVERSATION_MODEL_URL") or env.get("AGENT_CONVERSATION_BASE_URL")):
    if not sys.stdin.isatty():
        sys.exit("Configure AGENT_CONVERSATION_BASE_URL or AGENT_CONVERSATION_MODEL_URL.")
    env["AGENT_CONVERSATION_BASE_URL"] = input("Model service origin: ").strip()
if env["AGENT_CONVERSATION_PROVIDER"] == "gateway" and not (env.get("AGENT_PROVIDER_API_KEY") or env.get("AGENT_CONVERSATION_MODEL_API_KEY")):
    if not sys.stdin.isatty():
        sys.exit("Configure AGENT_PROVIDER_API_KEY in the process environment.")
    env["AGENT_PROVIDER_API_KEY"] = getpass.getpass("Gateway API key (not saved): ")
if env.get("AGENT_KNOWLEDGE_KB_ID") and not (env.get("AGENT_KNOWLEDGE_API_KEY") or env.get("AGENT_PROVIDER_API_KEY")):
    if not sys.stdin.isatty():
        sys.exit("Configure AGENT_KNOWLEDGE_API_KEY or AGENT_PROVIDER_API_KEY.")
    env["AGENT_KNOWLEDGE_API_KEY"] = getpass.getpass("Knowledge service API key (not saved): ")
login_path = config_path.with_name("web-first-login.txt")
if not login_path.exists():
    with os.fdopen(os.open(login_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "w") as handle:
        handle.write("Account: admin@example.com\nInitial password: " + env["AUTH_DEFAULT_PASSWORD"] + "\n\nFor the first login only. Set your own password in the login page.\n")
print("Initial login details: " + str(login_path), flush=True)
print("Identity settings: " + str(config_path), flush=True)
os.chdir(root)
os.execvpe("go", ["go", "run", "./cmd/domainry-agent-web"], env)
