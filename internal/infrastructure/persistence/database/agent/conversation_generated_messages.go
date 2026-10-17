package agent

import (
	"context"
	"database/sql"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func conversationMessageGeneration(run agentsdk.ConversationRun, step int) agentsdk.ConversationMessageGeneration {
	modelAttempt := 1
	for _, attempt := range run.ModelAttempts {
		if attempt.RunAttempt == run.Attempt && attempt.Step == step {
			modelAttempt = attempt.Number
		}
	}
	return agentsdk.ConversationMessageGeneration{Step: step, RunAttempt: run.Attempt, ModelAttempt: modelAttempt, Status: "generating"}
}

func (s *ConversationStore) generatedMessagesOwnTail(ctx context.Context, tx *sql.Tx, row conversationRunRow) (bool, error) {
	predicate := query.And(conversationItemScope(row.Authority, row.Run.ConversationID, conversationItemMessage), query.GreaterThan("seq", max(row.Run.UserSeq, row.Run.LastInputSeq)))
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(predicate).Build()
	if err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var message agentsdk.ConversationMessage
		if err = rows.Scan(&raw); err == nil {
			err = unmarshalDurableJSON(raw, &message)
		}
		if err != nil || message.RunID != row.Run.ID || message.Role != "assistant" || message.Generation == nil {
			return false, err
		}
	}
	return true, rows.Err()
}

// A model generation has one stable message identity. New steps/retries allocate
// new identities; only a prefix extension of this generation may change its text.
func (s *ConversationStore) generatedMessage(ctx context.Context, tx *sql.Tx, row *conversationRunRow, generation agentsdk.ConversationMessageGeneration, content string) (agentsdk.ConversationMessage, error) {
	id := "msg_" + conversationHash([]any{row.Run.ID, generation.RunAttempt, generation.Step, generation.ModelAttempt})[:32]
	predicate := query.And(conversationItemScope(row.Authority, row.Run.ConversationID, conversationItemMessage), query.Equal("reference_id", id))
	var previous agentsdk.ConversationMessage
	found, err := s.payloadRead(ctx, tx, conversationItemTable, predicate, &previous)
	if err != nil {
		return previous, err
	}
	if content == "" {
		if !found {
			return previous, nil
		}
		content = previous.Content
	}
	if found {
		if previous.Generation == nil || !strings.HasPrefix(content, previous.Content) || previous.Generation.Status != "generating" && (content != previous.Content || generation.Status != previous.Generation.Status) {
			return previous, conversationError("conflict", "generated_message_conflict")
		}
		if previous.Content == content && previous.Generation.Status == generation.Status {
			return previous, nil
		}
		previous.Content, previous.Generation = content, &generation
		q, args, e := query.NewUpdateBuilder(s.store.Renderer(), conversationItemTable).Set("payload_json", conversationJSON(previous)).Where(predicate).Build()
		return previous, conversationCAS(ctx, tx, q, args, e)
	}
	c, err := s.get(ctx, tx, row.Run.ConversationID, row.Authority)
	if err != nil {
		return previous, err
	}
	m := agentsdk.ConversationMessage{ID: id, ConversationID: c.ID, RunID: row.Run.ID, Seq: c.LastSeq + 1, Role: "assistant", Content: content, Generation: &generation, CreatedAt: time.Now().UTC()}
	if row.Run.BackgroundTask != nil {
		m.BackgroundTaskID = row.Run.BackgroundTask.TaskID
	}
	if err = s.insertMessage(ctx, tx, m, row.Authority); err != nil {
		return m, err
	}
	c.LastSeq = m.Seq
	return m, s.save(ctx, tx, c, c.Revision, row.Authority)
}

func (s *ConversationStore) interruptGeneratedMessages(ctx context.Context, tx *sql.Tx, row *conversationRunRow) error {
	predicate := query.And(conversationItemScope(row.Authority, row.Run.ConversationID, conversationItemMessage), query.Equal("run_id", row.Run.ID))
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(predicate).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	var messages []agentsdk.ConversationMessage
	for rows.Next() {
		var raw []byte
		var message agentsdk.ConversationMessage
		if err = rows.Scan(&raw); err == nil {
			err = unmarshalDurableJSON(raw, &message)
		}
		if err != nil {
			rows.Close()
			return err
		}
		if message.Generation != nil && message.Generation.Status == "generating" {
			messages = append(messages, message)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, message := range messages {
		generation := *message.Generation
		generation.Status = "interrupted"
		if _, err = s.generatedMessage(ctx, tx, row, generation, message.Content); err != nil {
			return err
		}
	}
	return nil
}

// This runs in the same transaction as the durable event. A refresh, failed
// request, tool invocation or later generation cannot remove already seen text.
func (s *ConversationStore) recordGeneratedMessageEvent(ctx context.Context, tx *sql.Tx, row *conversationRunRow, kind string, data map[string]any) error {
	var event struct {
		Step         int `json:"step"`
		ModelAttempt int `json:"model_attempt"`
	}
	if err := unmarshalDurableJSON([]byte(conversationJSON(data)), &event); err != nil {
		return err
	}
	var generation agentsdk.ConversationMessageGeneration
	var content string
	switch kind {
	case "step.text.delta", "step.completed":
		step := conversationRunStep(&row.Run, event.Step)
		if step == nil || strings.TrimSpace(step.Text) == "" {
			return nil
		}
		generation, content = conversationMessageGeneration(row.Run, event.Step), step.Text
		if kind == "step.completed" {
			generation.Status = "completed"
		}
	case "message.delta":
		if len(row.Run.Steps) != 0 {
			return nil // the final execution response already owns its step message
		}
		generation, content = conversationMessageGeneration(row.Run, -1), row.Run.DraftText
	case "model.attempt.failed":
		if event.Step < -1 {
			return nil // internal plans and summaries are not public responses
		}
		generation = conversationMessageGeneration(row.Run, event.Step)
		generation.ModelAttempt, generation.Status = event.ModelAttempt, "interrupted"
	case "run.started", "run.failed", "run.cancelled":
		return s.interruptGeneratedMessages(ctx, tx, row)
	default:
		return nil
	}
	m, err := s.generatedMessage(ctx, tx, row, generation, content)
	if err != nil || m.ID == "" {
		return err
	}
	data["message_id"], data["message_seq"], data["model_attempt"] = m.ID, m.Seq, generation.ModelAttempt
	data["message_status"] = generation.Status
	if generation.Step >= 0 {
		step := conversationRunStep(&row.Run, generation.Step)
		step.MessageID, step.MessageSeq = m.ID, m.Seq
	} else {
		row.Run.DraftMessageID, row.Run.DraftMessageSeq = m.ID, m.Seq
	}
	return nil
}
