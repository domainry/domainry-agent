#!/usr/bin/env python3
"""Run real private-attachment acceptance in its provisioned synthetic-only KB."""
import argparse
import json
import os
from pathlib import Path
import tempfile

from agent_private_configuration import load_service_credentials

parser = argparse.ArgumentParser()
parser.add_argument('--live', action='store_true', required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
manifest = json.loads((root / 'docs/evidence/2026-09-11-k07-attachment-kb.json').read_text())
if not (manifest.get('creation_confirmed') is True
        and manifest.get('kb_id') == 'kb-1a07a88ed780'
        and manifest.get('name') == 'domainry-agent-attachment-acceptance-20260911'
        and manifest.get('origin') == 'https://api.verdent.ai'
        and manifest.get('team_id') == '1470194374940573696'
        and manifest.get('contains_user_documents') is False):
    raise SystemExit('Provisioned synthetic attachment knowledge base required.')
env = dict(os.environ)
load_service_credentials(env)
if not (env.get('AGENT_KNOWLEDGE_API_KEY') or env.get('AGENT_PROVIDER_API_KEY')):
    raise SystemExit('Configure the knowledge service credential locally.')
env.update(AGENT_KNOWLEDGE_BASE_URL=manifest['origin'],
           AGENT_KNOWLEDGE_TEAM_ID=manifest['team_id'],
           AGENT_KNOWLEDGE_KB_ID=manifest['kb_id'],
           AGENT_ATTACHMENT_INDEX_LIVE='1',
           AGENT_LIVE_EVIDENCE_DIR=tempfile.mkdtemp(prefix='domainry-k07-attachment-live-'))
print('Private attachment evidence: ' + env['AGENT_LIVE_EVIDENCE_DIR'], flush=True)
os.chdir(root)
os.execvpe('go', ['go','test','./internal/assembly/web','-run','^TestLivePrivateAttachmentIndexIdentityHTTP$','-count=1','-v','-timeout=7m'], env)
