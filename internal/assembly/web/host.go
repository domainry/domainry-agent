// Package web composes Identity and Agent as in-process modules for a browser
// application. It does not change either module's deployment-neutral contract.
package web

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/infrastructure/artifactstorage"
	"github.com/domainry/domainry-agent/internal/infrastructure/attachmentstorage"
	"github.com/domainry/domainry-agent/internal/infrastructure/documentstorage"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/webhost"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Options struct {
	DatabasePath, RuntimeID, WorkspaceID, ApplicationKey string
	Agent                                                agentmodule.Options
	Identity                                             identitymodule.Options
	ExternalIdentity                                     identitysdk.ExternalDatabaseFactory
	ExternalRoles                                        []identitysdk.ProjectRoleDefinition
	KnowledgePermissions                                 map[string]string // local permission name -> upstream permission ID
}

type Host struct {
	Agent                agentsdk.Binding
	Identity             identitysdk.Binding
	db                   *sql.DB
	lock                 *os.File
	runtimeID            string
	application          identitysdk.ApplicationRef
	registrar            *webhost.Registrar
	knowledgePermissions map[string]string
	artifactFiles        *artifactstorage.Files
	attachmentFiles      *attachmentstorage.Files
	documentFiles        *documentstorage.Files
	external             bool
}

func Open(ctx context.Context, options Options) (_ *Host, resultErr error) {
	if options.DatabasePath == "" || options.RuntimeID == "" || options.WorkspaceID == "" || options.ApplicationKey == "" {
		return nil, fmt.Errorf("web host database and application identity are required")
	}
	path, err := filepath.Abs(options.DatabasePath)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	h := &Host{lock: lock, runtimeID: options.RuntimeID, external: options.ExternalIdentity != nil}
	defer func() {
		if resultErr != nil {
			_ = h.Close(context.Background())
		}
	}()
	// The lock remains held for the pool's lifetime. This SQLite web host is a
	// single process; modules may recursively submit migrations during startup.
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, fmt.Errorf("web database is already in use: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	_ = file.Close()
	h.db, err = sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	h.db.SetMaxOpenConns(1)
	renderer, err := persistence.Renderer("sqlite", "")
	if err != nil {
		return nil, err
	}
	h.registrar = &webhost.Registrar{DB: h.db, Renderer: renderer}
	if err = h.registrar.Prepare(ctx); err != nil {
		return nil, err
	}
	application := identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(options.WorkspaceID), ApplicationKey: identitysdk.ApplicationKey(options.ApplicationKey)}
	h.application = application
	handle := identitysdk.DatabaseHandle{
		Pool: h.db, Driver: "sqlite", FilePath: path, Migrations: h.registrar, ModuleMigrations: h.registrar,
	}
	if h.external {
		workspaces := &webhost.ExternalWorkspaces{Registrar: h.registrar}
		if err = workspaces.Prepare(ctx); err != nil {
			return nil, err
		}
		handle.ExternalWorkspaces = workspaces
		h.Identity, err = options.ExternalIdentity.OpenExternalWithDatabase(ctx, application, handle)
	} else {
		h.Identity, err = identitymodule.NewFactory(options.Identity).OpenWithDatabase(ctx, application, handle)
	}
	if err != nil {
		return nil, fmt.Errorf("open Identity module: %w", err)
	}
	if _, err = h.Identity.Applications().Register(ctx, identitysdk.ApplicationRegistration{Application: application}); err != nil {
		return nil, err
	}
	if err = h.registerConversationToolPermissions(ctx); err != nil {
		return nil, err
	}
	if len(options.KnowledgePermissions) > 0 && options.Agent.Knowledge.PermissionIDs != nil {
		return nil, fmt.Errorf("configure only one knowledge permission resolver")
	}
	if err = h.registerKnowledgePermissions(ctx, options.KnowledgePermissions); err != nil {
		return nil, err
	}
	if h.external {
		publisher, ok := h.Identity.(identitysdk.ProjectRoleCatalogPublisher)
		if !ok || len(options.ExternalRoles) == 0 {
			return nil, fmt.Errorf("external Agent role catalog is required")
		}
		if _, err = publisher.PublishProjectRoles(ctx, identitysdk.ProjectRoleCatalog{Application: application, Roles: options.ExternalRoles}); err != nil {
			return nil, err
		}
	}
	if options.Agent.Knowledge.PermissionIDs == nil {
		options.Agent.Knowledge.PermissionIDs = h.knowledgePermissionIDs
	}
	if h.external {
		if options.Agent.Knowledge.AuthorizeWorkspace != nil {
			return nil, fmt.Errorf("external web host owns knowledge Workspace authorization")
		}
		options.Agent.Knowledge.AuthorizeWorkspace = h.authorizeKnowledgeWorkspace
	}
	if options.Agent.ConversationOptions.ArtifactStorage == nil {
		h.artifactFiles, err = artifactstorage.NewFiles(path + ".artifacts")
		if err != nil {
			return nil, fmt.Errorf("open artifact storage: %w", err)
		}
		options.Agent.ConversationOptions.ArtifactStorage = h.artifactFiles
	}
	if options.Agent.ConversationOptions.AttachmentStorage == nil {
		h.attachmentFiles, err = attachmentstorage.NewFiles(path + ".attachments")
		if err != nil {
			return nil, fmt.Errorf("open attachment storage: %w", err)
		}
		options.Agent.ConversationOptions.AttachmentStorage = h.attachmentFiles
	}
	if options.Agent.ConversationOptions.DocumentStorage == nil {
		h.documentFiles, err = documentstorage.NewFiles(path + ".documents")
		if err != nil {
			return nil, fmt.Errorf("open document storage: %w", err)
		}
		options.Agent.ConversationOptions.DocumentStorage = h.documentFiles
	}
	if options.Agent.ConversationOptions.AttachmentAuthorizer == nil {
		options.Agent.ConversationOptions.AttachmentAuthorizer = h
	}
	if options.Agent.ConversationOptions.LibraryAuthorizer == nil {
		options.Agent.ConversationOptions.LibraryAuthorizer = h
	}
	h.Agent, err = agentmodule.NewFactory(options.Agent).OpenModule(ctx, agentsdk.ApplicationRef{RuntimeID: options.RuntimeID}, h)
	if err != nil {
		return nil, fmt.Errorf("open Agent module: %w", err)
	}
	return h, nil
}

func (h *Host) RuntimeID() string                         { return h.runtimeID }
func (h *Host) Database() modulehost.Database             { return h.db }
func (h *Host) Dialect() modulehost.Dialect               { return h.registrar.Renderer }
func (h *Host) Migrations() modulehost.MigrationRegistrar { return h.registrar }
func (h *Host) Close(ctx context.Context) error {
	var result error
	if h.Agent != nil {
		result = h.Agent.Close(ctx)
		h.Agent = nil
	}
	if h.artifactFiles != nil {
		if err := h.artifactFiles.Close(); result == nil {
			result = err
		}
		h.artifactFiles = nil
	}
	if h.documentFiles != nil {
		if err := h.documentFiles.Close(); result == nil {
			result = err
		}
		h.documentFiles = nil
	}
	if h.attachmentFiles != nil {
		if err := h.attachmentFiles.Close(); result == nil {
			result = err
		}
		h.attachmentFiles = nil
	}
	if h.Identity != nil {
		if err := h.Identity.Close(ctx); result == nil {
			result = err
		}
		h.Identity = nil
	}
	if h.db != nil {
		if err := h.db.Close(); result == nil {
			result = err
		}
		h.db = nil
	}
	if h.lock != nil {
		_ = unix.Flock(int(h.lock.Fd()), unix.LOCK_UN)
		_ = h.lock.Close()
		h.lock = nil
	}
	return result
}
