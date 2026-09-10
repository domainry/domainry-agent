package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	httpapi "github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
)

// Opt-in: writes only a new, preflight-absent synthetic document. A persistent
// manifest is saved before every write; the existing acceptance cleanup helper
// can recover this exact ID if a process is interrupted.
func TestLiveKnowledgeDocumentLifecycle(t *testing.T) {
	if os.Getenv("AGENT_KNOWLEDGE_DOCUMENTS_LIVE") != "1" {
		t.Skip("requires explicit synthetic-document live acceptance")
	}
	config := KnowledgeConfigFromEnvironment()
	if config.BaseURL != "https://api.verdent.ai" {
		t.Fatal("live document acceptance requires the verified Verdent origin")
	}
	config.WorkspaceID = "knowledge-document-acceptance"
	config.ResponseMapping = nil
	config.mappingInvalid = false
	config.DocumentManagement = true
	source, err := NewKnowledge(config)
	if err != nil || source == nil {
		t.Fatal("live source configuration invalid", err)
	}
	dir := os.Getenv("AGENT_LIVE_EVIDENCE_DIR")
	if dir == "" || !filepath.IsAbs(dir) {
		t.Fatal("persistent evidence directory required")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 6)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(random)
	id := "domainry-agent-acceptance-" + time.Now().UTC().Format("20060102") + "-" + suffix
	marker := "LIBRARY-DOCUMENT-" + strings.ToUpper(suffix)
	content := "# 资料库文档生命周期合成验收\n\n本文件仅用于自动测试，不含真实个人或业务资料。\n\n验收标识：" + marker + "。\n\n费用规则：收到发票后 30 日付款；合成金额为 123.45 元。\n"
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "document-acceptance", WorkspaceID: config.WorkspaceID, UserID: "document-acceptance"}
	manifest := map[string]any{"origin": config.BaseURL, "team_id": config.TeamID, "kb_id": config.KBID, "doc_id": id, "marker": marker, "filename": "domainry-agent-synthetic-acceptance.md", "content": content, "preflight_missing": false, "upload_attempted": false, "cleanup_verified": false}
	path := filepath.Join(dir, "manifest.json")
	save := func() error {
		raw, e := json.MarshalIndent(manifest, "", "  ")
		if e != nil {
			return e
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if e != nil {
			return e
		}
		if _, e = f.Write(raw); e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		return closeErr
	}
	if err = save(); err != nil {
		t.Fatal(err)
	}
	t.Logf("Synthetic document manifest: %s", path)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	state, err := source.InspectKnowledgeDocument(ctx, id, a)
	if err != nil || state.Exists {
		t.Fatal("new document ID not confirmed absent", err)
	}
	manifest["preflight_missing"] = true
	if err = save(); err != nil {
		t.Fatal(err)
	}
	cleanup := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		current, e := source.InspectKnowledgeDocument(cleanupCtx, id, a)
		if e != nil {
			return e
		}
		if current.Exists {
			manifest["delete_attempted"] = true
			if e = save(); e != nil {
				return e
			}
			if e = source.DeleteKnowledgeDocument(cleanupCtx, id, a); e != nil {
				return e
			}
			manifest["delete_acknowledged"] = true
			if e = save(); e != nil {
				return e
			}
		} else if manifest["upload_acknowledged"] != true {
			return knowledgeFailure("write_outcome_unknown")
		}
		for {
			current, e = source.InspectKnowledgeDocument(cleanupCtx, id, a)
			if e != nil {
				return e
			}
			if !current.Exists {
				raw, e := source.Search(cleanupCtx, marker, a)
				if e != nil {
					return e
				}
				var out httpapi.Output
				var found struct {
					Data struct {
						Hits []struct {
							DocID string `json:"doc_id"`
						} `json:"hits"`
					} `json:"data"`
				}
				if json.Unmarshal(raw, &out) != nil || json.Unmarshal(out.Result, &found) != nil {
					return knowledgeFailure("response_invalid")
				}
				present := false
				for _, hit := range found.Data.Hits {
					present = present || hit.DocID == id
				}
				if !present {
					manifest["cleanup_verified"] = true
					return save()
				}
			}
			select {
			case <-cleanupCtx.Done():
				return cleanupCtx.Err()
			case <-time.After(3 * time.Second):
			}
		}
	}
	defer func() {
		if manifest["cleanup_verified"] != true {
			if e := cleanup(); e != nil {
				t.Errorf("synthetic document cleanup unresolved; retain manifest: %v", e)
			}
		}
	}()
	manifest["upload_attempted"] = true
	if err = save(); err != nil {
		t.Fatal(err)
	}
	if err = source.PutKnowledgeDocument(ctx, agentsdk.KnowledgeDocumentContent{DocumentID: id, Filename: manifest["filename"].(string), Data: []byte(content)}, a); err != nil {
		t.Fatal("upload outcome requires inspection; no retry attempted", err)
	}
	manifest["upload_acknowledged"] = true
	if err = save(); err != nil {
		t.Fatal(err)
	}
	t.Log("Document push acknowledged; waiting for actual index state")
	last := ""
	for {
		state, err = source.InspectKnowledgeDocument(ctx, id, a)
		if err != nil {
			t.Fatal("index inspection failed", err)
		}
		if state.IndexStatus != last {
			last = state.IndexStatus
			manifest["index_status"] = last
			if err = save(); err != nil {
				t.Fatal(err)
			}
			t.Logf("Index status: %s", last)
		}
		if state.Exists && state.IndexStatus == "INDEXED" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("document not indexed before deadline")
		case <-time.After(5 * time.Second):
		}
	}
	raw, err := source.Fetch(ctx, id, a)
	if err != nil {
		t.Fatal(err)
	}
	var fetched httpapi.Output
	var fetch struct {
		Data struct {
			Chunks []struct {
				Content string `json:"content"`
			} `json:"chunks"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &fetched) != nil || json.Unmarshal(fetched.Result, &fetch) != nil {
		t.Fatal("fetch response invalid")
	}
	matched := false
	for _, chunk := range fetch.Data.Chunks {
		matched = matched || strings.Contains(chunk.Content, marker)
	}
	if !matched {
		t.Fatal("indexed document did not return the actual synthetic content")
	}
	manifest["content_readable"] = true
	manifest["fetch_chunks"] = len(fetch.Data.Chunks)
	raw, err = source.Search(ctx, marker, a)
	if err != nil {
		t.Fatal(err)
	}
	var searched httpapi.Output
	var search struct {
		Data struct {
			Hits []struct {
				DocID string `json:"doc_id"`
			} `json:"hits"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &searched) != nil || json.Unmarshal(searched.Result, &search) != nil {
		t.Fatal("search response invalid")
	}
	matched = false
	for _, hit := range search.Data.Hits {
		matched = matched || hit.DocID == id
	}
	if !matched {
		t.Fatal("indexed document not found by unique marker")
	}
	manifest["search_matched"] = true
	if err = save(); err != nil {
		t.Fatal(err)
	}
	if err = cleanup(); err != nil {
		t.Fatal("document cleanup unresolved", err)
	}
	t.Log("Actual content fetched and searched; deletion and absence verified")
}
