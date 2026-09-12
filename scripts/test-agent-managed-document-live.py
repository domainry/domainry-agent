#!/usr/bin/env python3
"""Run product private upload with a synthetic file; retain SQLite recovery state."""
import argparse
import json
import os
from pathlib import Path
import sys
import tempfile

from agent_private_configuration import load_service_credentials


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--live", required=True, action="store_true", help="upload and delete one synthetic document through the real Agent application")
    parser.add_argument("--datasources", action="store_true", help="exercise UI/API bindings and synthetic uploads in the three previously verified API-push KBs")
    parser.add_argument("--transfers", action="store_true", help="copy and move synthetic originals between the three approved private KBs with two isolated Identity users")
    parser.add_argument("--extract-formats", action="store_true", help="upload synthetic PDF/Word files and verify Connector-content extraction (no local original parsing)")
    parser.add_argument("--formats", help="comma-separated subset pdf,docx for a targeted --extract-formats retry")
    args = parser.parse_args()
    if args.formats and (not args.extract_formats or any(f not in {"pdf", "docx"} for f in args.formats.split(",")) or len(set(args.formats.split(","))) != len(args.formats.split(","))):
        parser.error("--formats requires --extract-formats and unique supported file formats")
    if sum((args.datasources, args.extract_formats, args.transfers)) > 1:
        parser.error("select one acceptance mode")
    directory = Path.home() / ("Library/Application Support/domainry-agent" if sys.platform == "darwin" else ".config/domainry-agent")
    config_path = Path(os.environ.get("AGENT_WEB_SERVICES_CONFIG", directory / "web-services.json")).expanduser()
    settings = json.loads(config_path.read_text()) if config_path.exists() else {}
    env = dict(os.environ)
    load_service_credentials(env)
    for name in ("AGENT_KNOWLEDGE_BASE_URL", "AGENT_KNOWLEDGE_TEAM_ID", "AGENT_KNOWLEDGE_KB_ID", "AGENT_CONVERSATION_PROVIDER", "AGENT_CONVERSATION_PROTOCOL", "AGENT_CONVERSATION_BASE_URL", "AGENT_CONVERSATION_MODEL_URL", "AGENT_CONVERSATION_MODEL"):
        if name in settings:
            if not isinstance(settings[name], str):
                raise ValueError("Service settings must be strings")
            env.setdefault(name, settings[name])
    if env.get("AGENT_KNOWLEDGE_BASE_URL") != "https://api.verdent.ai" or not env.get("AGENT_KNOWLEDGE_TEAM_ID") or not env.get("AGENT_KNOWLEDGE_KB_ID"):
        raise ValueError("Configure a verified Verdent team and API push KB")
    if not (env.get("AGENT_KNOWLEDGE_API_KEY") or env.get("AGENT_PROVIDER_API_KEY")):
        raise ValueError("Configure the existing knowledge credential locally")
    env["AGENT_MANAGED_DOCUMENT_LIVE"] = "1"
    if args.datasources:
        env["AGENT_DATASOURCE_DOCUMENT_LIVE"] = "1"
    if args.transfers:
        env["AGENT_DOCUMENT_TRANSFERS_LIVE"] = "1"
    if args.extract_formats:
        env["AGENT_EXTRACTION_FORMAT_LIVE"] = "1"
        if args.formats:
            env["AGENT_EXTRACTION_FORMATS"] = args.formats
    env["AGENT_LIVE_EVIDENCE_DIR"] = tempfile.mkdtemp(prefix="domainry-transfer-live-" if args.transfers else "domainry-managed-private-live-")
    print("Managed private document evidence: " + env["AGENT_LIVE_EVIDENCE_DIR"], flush=True)
    model_note = "configured real model" if args.extract_formats else "deterministic model"
    print(f"Real Identity/HTTP/SQLite/worker/Verdent; {model_note}. Original files and write ownership remain in the recovery directory until cleanup is confirmed.", flush=True)
    os.chdir(Path(__file__).resolve().parents[1])
    test_name = "^TestLiveDynamicKnowledgeDatasourcesIdentityHTTP$" if args.datasources else "^TestLiveManagedPrivateDocumentIdentityHTTP$"
    if args.transfers:
        test_name = "^TestLiveKnowledgeDocumentTransfersIdentityHTTP$"
    if args.extract_formats:
        test_name = "^TestLiveKnowledgeExtractionFileFormatsIdentityHTTP$"
    os.execvpe("go", ["go", "test", "./internal/assembly/web", "-run", test_name, "-count=1", "-v", "-timeout=20m"], env)


if __name__ == "__main__":
    main()
