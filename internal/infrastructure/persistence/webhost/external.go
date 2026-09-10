package webhost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-orm/schema"
)

// ExternalWorkspaces belongs to the standalone Agent host. A Runtime host uses
// its own Workspace bootstrap implementation instead. Both share the SDK's
// transaction boundary with the bridge's ownership and application records.
type ExternalWorkspaces struct{ *Registrar }

func (h *ExternalWorkspaces) Prepare(ctx context.Context) error {
	statement, _, err := schema.NewTable(h.Renderer, "agent_web_workspaces").Columns(
		schema.Column("id", schema.TextKey(191)).NotNull(),
		schema.Column("owner_user_id", schema.TextKey(191)).NotNull(),
		schema.Column("name", schema.Text()).NotNull(),
		schema.Column("active", schema.Boolean()).NotNull(),
	).PrimaryKey("id").Build()
	if err != nil {
		return err
	}
	return h.ApplyOwnedMigrations(ctx, "agent_web", []modulehost.SchemaMigration{{Version: 1, Name: "personal_workspaces", Statements: []string{statement}}})
}

func (h *ExternalWorkspaces) RunExternalWorkspaceTransaction(ctx context.Context, apply func(context.Context, identity.EmbeddedTransaction) error) error {
	if apply == nil {
		return fmt.Errorf("Workspace transaction callback is required")
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = apply(ctx, identity.EmbeddedTransaction{Executor: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *ExternalWorkspaces) CreateExternalWorkspace(ctx context.Context, request identity.ExternalWorkspaceCreate, tx identity.EmbeddedTransaction) error {
	if request.WorkspaceID == "" || request.UserID == "" || tx.Executor == nil {
		return fmt.Errorf("verified Workspace owner and transaction are required")
	}
	statement, args, err := query.NewInsertBuilder(h.Renderer, "agent_web_workspaces").Columns("id", "owner_user_id", "name", "active").Values(request.WorkspaceID, request.UserID, request.Name, true).Build()
	if err != nil {
		return err
	}
	_, err = tx.Executor.ExecContext(ctx, statement, args...)
	return err
}

func (*ExternalWorkspaces) InitializeExternalWorkspaceApplication(_ context.Context, request identity.ExternalWorkspaceCreate, _ identity.EmbeddedTransaction) error {
	// Agent's personal conversations are created on demand after authentication.
	// Arbitrary Runtime business bootstrap data cannot silently be ignored here.
	if len(request.ApplicationBootstrap) != 0 {
		return fmt.Errorf("standalone Agent does not support business Workspace bootstrap data")
	}
	return nil
}

func (h *ExternalWorkspaces) ExternalWorkspaceActive(ctx context.Context, id string) (bool, error) {
	statement, args, err := query.NewSelectBuilder(h.Renderer, "agent_web_workspaces").Columns("active").Where(query.Equal("id", id)).Build()
	if err != nil {
		return false, err
	}
	var active bool
	err = h.DB.QueryRowContext(ctx, statement, args...).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active, err
}

var _ identity.ExternalWorkspaceHost = (*ExternalWorkspaces)(nil)
