#!/usr/bin/env python3
"""Run the actual Runtime/Identity/Agent browser composition on synthetic data."""
import argparse
import getpass
from agent_private_configuration import load_service_credentials
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("--live", action="store_true", help="use the configured external model")
capability = parser.add_mutually_exclusive_group()
capability.add_argument("--relations", action="store_true", help="accept customer/order/project relationship traversal")
capability.add_argument("--actions", action="store_true", help="accept confirmed create/update/state-transition actions")
capability.add_argument("--workflows", action="store_true", help="accept confirmed workflow start and actual approval/progress")
parser.add_argument("--browser", action="store_true", help="serve the built UI for browser acceptance")
parser.add_argument("--model", help="explicit model override for this acceptance run")
parser.add_argument("--protocol", choices=("chat_completions", "responses", "messages"))
args = parser.parse_args()
if (args.model or args.protocol) and not args.live:
    parser.error("model and protocol overrides require --live")
agent = Path(__file__).resolve().parents[1]
runtime = Path(os.environ.get("DOMAINRY_RUNTIME_SOURCE", agent.parent / "domainry-runtime")).resolve()
env = dict(os.environ)
load_service_credentials(env)
if args.live:
    config_dir = Path.home() / ("Library/Application Support/domainry-agent" if sys.platform == "darwin" else ".config/domainry-agent")
    config_path = Path(env.get("AGENT_WEB_SERVICES_CONFIG", config_dir / "web-services.json")).expanduser()
    if config_path.exists():
        values = json.loads(config_path.read_text())
        for name in ("AGENT_CONVERSATION_PROVIDER", "AGENT_CONVERSATION_PROTOCOL", "AGENT_CONVERSATION_BASE_URL", "AGENT_CONVERSATION_MODEL_URL", "AGENT_CONVERSATION_MODEL"):
            if name in values:
                if not isinstance(values[name], str):
                    sys.exit("Model settings must be strings: " + name)
                env.setdefault(name, values[name])
    if args.model:
        env["AGENT_CONVERSATION_MODEL"] = args.model
    if args.protocol:
        env["AGENT_CONVERSATION_PROTOCOL"] = args.protocol
    if not env.get("AGENT_CONVERSATION_MODEL") or not (env.get("AGENT_CONVERSATION_BASE_URL") or env.get("AGENT_CONVERSATION_MODEL_URL")):
        sys.exit("Configure the model and URL in AGENT_WEB_SERVICES_CONFIG or the environment.")
    if not (env.get("AGENT_PROVIDER_API_KEY") or env.get("AGENT_CONVERSATION_MODEL_API_KEY")):
        if not sys.stdin.isatty():
            sys.exit("Configure the model credential in the process environment.")
        env["AGENT_CONVERSATION_MODEL_API_KEY"] = getpass.getpass("Model API key (not saved): ")
    env["RUNTIME_BUSINESS_LIVE"] = "1"
if args.browser:
    frontend = agent / "frontend/dist"
    if not (frontend / "index.html").is_file():
        sys.exit("Build the Agent frontend before browser acceptance.")
    env["RUNTIME_BUSINESS_BROWSER"] = "1"
    env["RUNTIME_BUSINESS_FRONTEND"] = str(frontend)
evidence = tempfile.mkdtemp(prefix="domainry-business-agent-")
env["RUNTIME_BUSINESS_EVIDENCE_DIR"] = evidence
print("Synthetic business evidence: " + evidence, flush=True)
modules = [agent, agent.parent / "domainry-agent-sdk", agent.parent / "domainry-connectors", agent.parent / "domainry-identity", runtime]
# Runtime/Identity development can introduce SDK contracts before publishing.
# Compose the matching local SDK when present, just like the other source modules.
identity_sdk = agent.parent / "domainry-identity-sdk"
if (identity_sdk / "go.mod").is_file():
    modules.append(identity_sdk)
for module in modules:
    if not (module / "go.mod").is_file():
        sys.exit("Required local module is missing: " + str(module))
with tempfile.TemporaryDirectory(prefix="domainry-business-workspace-") as temporary:
    workspace = Path(temporary) / "go.work"
    workspace.write_text("go 1.26.0\n\nuse (\n" + "".join("\t" + json.dumps(str(module)) + "\n" for module in modules) + ")\n")
    env["GOWORK"] = str(workspace)
    test_name = "TestConversationBusinessRelationsThroughIdentityWebAndRestart" if args.relations else "TestConversationBusinessBrowserAndRestart"
    if args.actions:
        test_name = "TestLiveConversationBusinessActions" if args.live else "TestConversationBusinessActionsBrowser" if args.browser else "TestConversationBusinessActionsThroughIdentityWebAndRestart"
        if args.browser and not args.live:
            env["RUNTIME_BUSINESS_ACTIONS_BROWSER"] = "1"
    if args.workflows:
        test_name = "TestLiveConversationBusinessWorkflow" if args.live else "TestConversationBusinessWorkflowThroughIdentityWebAndRestart"
    result = subprocess.run(["go", "test", "./runtime/bootstrap/integrationtest", "-run", "^" + test_name + "$", "-count=1", "-v", "-timeout=30m"], cwd=runtime, env=env)
sys.exit(result.returncode)
