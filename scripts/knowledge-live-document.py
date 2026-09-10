#!/usr/bin/env python3
"""Manage only synthetic documents for the opt-in Verdent acceptance test."""
import argparse
import getpass
from agent_private_configuration import load_service_credentials
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import time
from datetime import datetime, timezone
import urllib.request
import urllib.error
import urllib.parse
import uuid

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

parser = argparse.ArgumentParser()
operation = parser.add_mutually_exclusive_group(required=True)
operation.add_argument('--create', action='store_true', help='upload a new synthetic test document and wait for indexed content')
operation.add_argument('--cleanup', type=Path, help='delete only the document recorded in a test manifest')
operation.add_argument('--inspect', type=Path, help='read the current indexing state and search results without writing remote data')
args = parser.parse_args()
default_dir = Path.home() / ('Library/Application Support/domainry-agent' if sys.platform == 'darwin' else '.config/domainry-agent')
config_path = Path(os.environ.get('AGENT_WEB_SERVICES_CONFIG', default_dir / 'web-services.json')).expanduser()
config = json.loads(config_path.read_text()) if config_path.exists() else {}
settings = {name: os.environ.get(name, config.get(name, '')) for name in ('AGENT_KNOWLEDGE_BASE_URL', 'AGENT_KNOWLEDGE_TEAM_ID', 'AGENT_KNOWLEDGE_KB_ID')}
if any(not isinstance(value, str) or not value.strip() for value in settings.values()):
    sys.exit('Configure the knowledge origin, team and knowledge base.')
origin = settings['AGENT_KNOWLEDGE_BASE_URL'].rstrip('/')
if origin != 'https://api.verdent.ai':
    sys.exit('This acceptance helper uses the verified Verdent document API only.')
team, kb = settings['AGENT_KNOWLEDGE_TEAM_ID'], settings['AGENT_KNOWLEDGE_KB_ID']
credential_env = dict(os.environ)
load_service_credentials(credential_env)
key = credential_env.get('AGENT_KNOWLEDGE_API_KEY') or credential_env.get('AGENT_PROVIDER_API_KEY')
if not key:
    if not sys.stdin.isatty():
        sys.exit('Configure a knowledge credential in the process environment.')
    key = getpass.getpass('Knowledge API key (not saved): ')

def request(method, path, payload=None, binary=None):
    headers = {'Authorization':'Bearer '+key, 'Accept':'application/json'}
    data = binary
    if payload is not None:
        headers['Content-Type'] = 'application/json'
        data = json.dumps(payload).encode()
    elif binary is not None:
        headers['Content-Type'] = 'application/octet-stream'
    req = urllib.request.Request(origin+path, data=data, headers=headers, method=method)
    try:
        with urllib.request.build_opener(NoRedirect()).open(req, timeout=40) as response:
            status, raw = response.status, response.read(524289)
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read(4096)
    if len(raw) > 524288:
        raise RuntimeError('Response exceeds limit')
    try:
        return status, json.loads(raw)
    except ValueError:
        return status, {'non_json':True}

def save(path, data):
    with os.fdopen(os.open(path, os.O_WRONLY|os.O_CREAT|os.O_TRUNC, 0o600), 'w') as output:
        json.dump(data, output, ensure_ascii=False, indent=2)

def report(name, status, data):
    print(json.dumps({'operation':name,'http_status':status,'err_code':data.get('err_code') if isinstance(data,dict) else None}),flush=True)

def fetch(doc):
    return request('POST','/v1/kb/fetch',{'team_id':team,'kb_id':kb,'doc_id':doc})

def doc_path(doc, filename=None):
    query = {'doc_id':doc}
    if filename: query['filename'] = filename
    return '/v1/kb/kbs/'+urllib.parse.quote(kb,safe='')+'/documents?'+urllib.parse.urlencode(query)

def read_manifest(path):
    manifest = json.loads(path.read_text())
    if manifest.get('origin') != origin or manifest.get('kb_id') != kb or manifest.get('team_id') != team:
        raise RuntimeError('Manifest does not match the configured knowledge scope')
    if not re.fullmatch(r'domainry-agent-acceptance-\d{8}-[0-9a-f]{12}', manifest.get('doc_id','')) or manifest.get('preflight_missing') is not True:
        raise RuntimeError('Only a preflight-verified synthetic document is supported')
    return manifest

def inspect(path, manifest):
    doc = manifest['doc_id']
    previous = None
    ready = False
    for attempt in range(24):
        status, value = fetch(doc)
        save(path.parent/'fetch.json',value)
        data = value.get('data',{})
        state = (status,value.get('err_code'),data.get('status'),len(data.get('chunks',[])))
        if state != previous:
            report('inspect-fetch',status,value)
            print(json.dumps({'index_status':data.get('status'),'chunks':len(data.get('chunks',[]))}),flush=True)
            previous = state
        if status == 200 and value.get('err_code') == 0 and len(data.get('chunks',[])) > 0:
            ready = True
            break
        if status != 200 or value.get('err_code') != 0 or data.get('status') not in ('PENDING','PROCESSING','CHUNKED','INDEXING'):
            break
        time.sleep(5)
    status, value = request('POST','/v1/kb/search',{'team_id':team,'kb_id':kb,'query':manifest['marker']+' 付款期限','top_k':5})
    save(path.parent/'search.json',value)
    report('inspect-search',status,value)
    manifest['content_readable'] = ready
    save(path,manifest)
    return ready

if args.inspect:
    inspect(args.inspect, read_manifest(args.inspect))
elif args.cleanup:
    manifest = read_manifest(args.cleanup)
    doc = manifest['doc_id']
    status, value = fetch(doc)
    if status == 200 and value.get('err_code') == 1004:
        manifest['cleanup_verified'] = True
        save(args.cleanup,manifest)
        report('already-deleted',status,value)
        sys.exit(0)
    if status != 200 or value.get('err_code') != 0:
        raise RuntimeError('Current document state is unknown; deletion not attempted')
    status, value = request('DELETE',doc_path(doc))
    save(args.cleanup.parent/'delete.json',value)
    report('delete',status,value)
    status, value = fetch(doc)
    save(args.cleanup.parent/'fetch-after-delete.json',value)
    report('fetch-after-delete',status,value)
    manifest['cleanup_verified'] = status == 200 and value.get('err_code') == 1004
    save(args.cleanup,manifest)
    if not manifest['cleanup_verified']: raise RuntimeError('Cleanup not yet verified')
else:
    doc = 'domainry-agent-acceptance-'+datetime.now(timezone.utc).strftime('%Y%m%d')+'-'+uuid.uuid4().hex[:12]
    marker = 'QINGHE-'+uuid.uuid4().hex[:12].upper()
    out = Path(tempfile.mkdtemp(prefix='domainry-knowledge-live-doc-'))
    content = '# 青禾合成验收指南\n\n本文件仅用于 Domainry Agent 自动验收，不对应真实业务。\n\n验收标识：'+marker+'。\n\n## 付款期限\n收到验收发票后 30 日付款。\n\n## 周报要求\n周报分为本周进展、风险、下周计划三节。\n\n## 费用样例\n样例一 125.50 元，样例二 74.50 元，合计 200.00 元。\n'
    manifest = {'origin':origin,'team_id':team,'kb_id':kb,'doc_id':doc,'marker':marker,'filename':'domainry-agent-synthetic-acceptance.md','content':content,'preflight_missing':False,'upload_attempted':False,'cleanup_verified':False}
    save(out/'manifest.json',manifest)
    print('Manifest: '+str(out/'manifest.json'),flush=True)
    status, value = fetch(doc)
    save(out/'preflight.json',value)
    report('preflight',status,value)
    if status != 200 or value.get('err_code') != 1004: raise RuntimeError('New document ID not confirmed absent')
    manifest['preflight_missing'] = True
    manifest['upload_attempted'] = True
    save(out/'manifest.json',manifest)
    status, value = request('POST',doc_path(doc,manifest['filename']),binary=content.encode())
    save(out/'upload.json',value)
    report('upload',status,value)
    if status not in (200,201,202) or value.get('err_code',0) != 0 or value.get('error') or value.get('non_json'): raise RuntimeError('Upload did not report success; inspect before retrying')
    if not inspect(out/'manifest.json',manifest):
        raise RuntimeError('Document is not yet readable; retain the manifest and inspect before any new upload')
