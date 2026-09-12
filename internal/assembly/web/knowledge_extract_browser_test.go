package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Binary original storage is real; parsed text and model decisions are explicit
// protocol fixtures. Actual PDF/Word parsing is verified by the separate live test.
const knowledgeExtractionBrowserText = "Customer: Qinghe Fixture\nInvoice Amount: 9007199254740993.25 CNY\nSigned Date: 2026-09-11\nApproval: true\nItem | Quantity | Unit Price\nPencil | 2 | 1.20\nPaper | 3 | 12.50\n"

func TestKnowledgeExtractionBrowserAcceptance(t *testing.T) {
	if os.Getenv("AGENT_TOOL_UI_ACCEPTANCE") != "1" {
		t.Skip("explicit temporary browser fixture only")
	}
	testKnowledgeDocumentsIdentityHTTP(t, false, true)
}

type knowledgeExtractionWebModel struct{ libraryKnowledgeWebModel }

func (knowledgeExtractionWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "chat_completions", Model: "extraction-browser-fixture", Fingerprint: "extraction-browser-fixture-v1"}
}

func (m knowledgeExtractionWebModel) StreamConversationStep(ctx context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	last := in.Messages[len(in.Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "read" && last.ToolCallID != "extract" {
		return m.libraryKnowledgeWebModel.StreamConversationStep(ctx, in, emit)
	}
	var result agentsdk.ConversationToolResult
	if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("extraction fixture tool failed")
	}
	if last.ToolCallID == "read" {
		var source agentsdk.ConversationKnowledgeResult
		if json.Unmarshal(result.Content, &source) != nil {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("invalid fixture source")
		}
		field := func(key, kind, pattern string, required bool) agentsdk.KnowledgeExtractionField {
			if source.Provider == "agent_parsed_document" || source.Provider == "agent_attachment_document" {
				pattern = strings.Replace(pattern, ": ", " [|] ", 1)
			}
			return agentsdk.KnowledgeExtractionField{KnowledgeExtractionColumn: agentsdk.KnowledgeExtractionColumn{Key: key, Type: kind, Required: required}, Pattern: pattern}
		}
		plan := agentsdk.KnowledgeExtractionArguments{LibraryID: source.LibraryID, DocumentID: source.DocumentID, KnowledgeExtractionPlan: agentsdk.KnowledgeExtractionPlan{
			Fields: []agentsdk.KnowledgeExtractionField{
				field("customer", "text", `Customer: ([^\n]+)`, true),
				field("amount", "decimal", `Invoice Amount: ([0-9.]+)`, true),
				field("signed_date", "date", `Signed Date: ([0-9-]+)`, true),
				field("approval", "boolean", `Approval: (true|false)`, true),
				field("email", "text", `Email: ([^\n]+)`, true),
			},
			Tables: []agentsdk.KnowledgeExtractionTable{{Key: "items", Limit: 50, Pattern: `(?m)^([A-Za-z]+) [|] ([0-9]+) [|] ([0-9.]+)$`, Columns: []agentsdk.KnowledgeExtractionColumn{{Key: "item", Type: "text"}, {Key: "quantity", Type: "integer"}, {Key: "price", Type: "decimal"}}}},
		}}
		if source.Provider == "agent_attachment_document" {
			plan.AttachmentID, plan.DocumentID = source.DocumentID, ""
		}
		raw, _ := json.Marshal(plan)
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{Name: "knowledge_extract", ID: "extract", Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	var extracted agentsdk.KnowledgeExtractionResult
	if json.Unmarshal(result.Content, &extracted) != nil {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("invalid fixture extraction result")
	}
	var text strings.Builder
	text.WriteString("提取结果（仅来自已返回的解析片段）：\n\n")
	labels := map[string]string{"customer": "客户", "amount": "金额", "signed_date": "签署日期", "approval": "确认状态", "email": "邮箱"}
	for _, f := range extracted.Data.Fields {
		value := "缺失"
		if f.Value != nil {
			value = *f.Value
		}
		fmt.Fprintf(&text, "- %s：%s\n", labels[f.Key], value)
	}
	text.WriteString("\n| 项目 | 数量 | 单价 |\n| --- | --- | --- |\n")
	for _, table := range extracted.Data.Tables {
		for _, row := range table.Rows {
			text.WriteString("|")
			for _, cell := range row {
				value := "缺失"
				if cell.Value != nil {
					value = *cell.Value
				}
				fmt.Fprintf(&text, " %s |", value)
			}
			text.WriteString("\n")
		}
	}
	text.WriteString("\n未计算合计；解析片段不能证明原文件完整。来源仅展示解析器实际提供的位置。\n\n")
	for _, citation := range extracted.Citations {
		fmt.Fprintf(&text, "[[cite:%s]] ", citation.ID)
	}
	if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: text.String()}); err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text.String()}, FinishReason: "stop"}, nil
}
