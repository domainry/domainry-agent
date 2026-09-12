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
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/base"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/webhost"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	"github.com/domainry/domainry-orm/driver"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type Options struct {
	CalendarTools, MailTools, WebTools                       bool
	CalendarWriteTools, MailWriteTools                       bool
	ReportTools                                              bool
	AnalysisTools                                            bool
	ScheduleTools                                            bool
	WebConnectionKey                                         string
	ToolAccountRequirements                                  map[string][]ToolAccountRequirement
	DatabaseDriver, DatabaseDSN, DatabaseSchema, StoragePath string
	ToolDefinitions                                          []agentsdk.ConversationToolDefinition
	Prepare                                                  func(context.Context, *Host) error
	DatabasePath, RuntimeID, WorkspaceID, ApplicationKey     string
	Agent                                                    agentmodule.Options
	Identity                                                 identitymodule.Options
	// IdentityBinding borrows an already scoped Identity owner. The caller keeps
	// its lifecycle and permission-publication ownership; this host only consumes it.
	IdentityBinding      identitysdk.Binding
	ExternalIdentity     identitysdk.ExternalDatabaseFactory
	ExternalRoles        []identitysdk.ProjectRoleDefinition
	Integration          integrationsdk.Binding
	ScheduledPlans       schedulersdk.ScheduledPlanService
	KnowledgePermissions map[string]string // local permission name -> upstream permission ID
}

type Host struct {
	accountToolDefinitions   []toolsdk.Definition
	accountToolAvailability  map[string]toolsdk.Availability
	reportToolAvailability   toolsdk.Availability
	analysisToolAvailability toolsdk.Availability
	scheduleToolAvailability toolsdk.Availability
	scheduleToolHost         toolsdk.Host
	toolAccountRequirements  map[string][]ToolAccountRequirement
	toolDefinitions          []agentsdk.ConversationToolDefinition
	Agent                    agentsdk.Binding
	Identity                 identitysdk.Binding
	Integration              integrationsdk.Binding
	ToolSettings             toolsdk.SettingsBinding
	db                       *sql.DB
	connection               *persistence.Connection
	storageRelease           func() error
	profile                  driver.Profile
	runtimeID                string
	application              identitysdk.ApplicationRef
	registrar                *webhost.Registrar
	knowledgePermissions     map[string]string
	artifactFiles            *knowledgemodule.ArtifactFiles
	attachmentFiles          *knowledgemodule.AttachmentFiles
	documentFiles            *knowledgemodule.DocumentFiles
	external                 bool
	identityBorrowed         bool
	businessSource           agentsdk.ConversationBusinessSource
}

func Open(ctx context.Context, options Options) (_ *Host, resultErr error) {
	if options.RuntimeID == "" || options.WorkspaceID == "" || options.ApplicationKey == "" {
		return nil, fmt.Errorf("web host application identity is required")
	}
	if options.IdentityBinding != nil && options.ExternalIdentity != nil {
		return nil, fmt.Errorf("configure only one shared or external Identity binding")
	}
	if err := configureAccountDefinitions(&options); err != nil {
		return nil, err
	}
	if err := configureReportDefinitions(&options); err != nil {
		return nil, err
	}
	if err := configureAnalysisDefinitions(&options); err != nil {
		return nil, err
	}
	if err := configureScheduleDefinitions(&options); err != nil {
		return nil, err
	}
	connection, profile, err := persistence.OpenConnection(ctx, options.DatabaseDriver, persistence.ConnectionOptions{Path: options.DatabasePath, DSN: options.DatabaseDSN, Schema: options.DatabaseSchema})
	if err != nil {
		return nil, err
	}
	h := &Host{toolAccountRequirements: cloneToolAccountRequirements(options.ToolAccountRequirements), db: connection.DB, connection: connection, profile: profile, runtimeID: options.RuntimeID, external: options.ExternalIdentity != nil, toolDefinitions: options.ToolDefinitions, Integration: options.Integration, identityBorrowed: options.IdentityBinding != nil, businessSource: options.Agent.ConversationOptions.Business}
	defer func() {
		if resultErr != nil {
			_ = h.Close(context.Background())
		}
	}()
	if options.CalendarTools || options.MailTools || options.WebTools || options.CalendarWriteTools || options.MailWriteTools {
		if err = h.bindAccountTools(&options); err != nil {
			return nil, err
		}
	}
	if options.ReportTools {
		if err = h.bindReportTools(&options); err != nil {
			return nil, err
		}
	}
	if options.AnalysisTools {
		if err = h.bindAnalysisTools(&options); err != nil {
			return nil, err
		}
	}
	if options.ScheduleTools {
		if err = h.bindScheduleTools(&options); err != nil {
			return nil, err
		}
	}
	// Keep the original SQLite sidecar paths. Network databases require an
	// explicit local storage path, independent of the DSN (which may be secret).
	path := options.StoragePath
	if path == "" {
		path = connection.FilePath
	}
	if path == "" {
		return nil, fmt.Errorf("StoragePath is required for local files with a network database")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if path != connection.FilePath {
		h.storageRelease, err = base.LockFile(path + ".lock")
		if err != nil {
			return nil, err
		}
	}
	renderer, err := persistence.Renderer(connection.Driver, connection.Schema)
	if err != nil {
		return nil, err
	}
	h.registrar = &webhost.Registrar{DB: h.db, Renderer: renderer, DatabaseDriver: connection.Driver, Namespace: connection.Schema, Profile: profile}
	if err = h.registrar.Prepare(ctx); err != nil {
		return nil, err
	}
	application := identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(options.WorkspaceID), ApplicationKey: identitysdk.ApplicationKey(options.ApplicationKey)}
	h.application = application
	handle := identitysdk.DatabaseHandle{
		Pool: h.db, Driver: connection.Driver, Schema: connection.Schema, FilePath: connection.FilePath, Migrations: h.registrar, ModuleMigrations: h.registrar,
	}
	if h.identityBorrowed {
		h.Identity, err = bindSharedIdentity(options.IdentityBinding, application)
	} else if h.external {
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
	if err = h.validateBusinessBinding(); err != nil {
		return nil, err
	}
	if !h.identityBorrowed {
		if _, err = h.Identity.Applications().Register(ctx, identitysdk.ApplicationRegistration{Application: application}); err != nil {
			return nil, err
		}
	}
	if err = h.registerConversationToolPermissions(ctx); err != nil {
		return nil, err
	}
	if err = h.registerIntegrationPermissions(ctx); err != nil {
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
		h.artifactFiles, err = knowledgemodule.NewArtifactFiles(path + ".artifacts")
		if err != nil {
			return nil, fmt.Errorf("open artifact storage: %w", err)
		}
		options.Agent.ConversationOptions.ArtifactStorage = h.artifactFiles
	}
	if options.Agent.ConversationOptions.AttachmentStorage == nil {
		h.attachmentFiles, err = knowledgemodule.NewAttachmentFiles(path + ".attachments")
		if err != nil {
			return nil, fmt.Errorf("open attachment storage: %w", err)
		}
		options.Agent.ConversationOptions.AttachmentStorage = h.attachmentFiles
	}
	if options.Agent.ConversationOptions.DocumentStorage == nil {
		h.documentFiles, err = knowledgemodule.NewDocumentFiles(path + ".documents")
		if err != nil {
			return nil, fmt.Errorf("open document storage: %w", err)
		}
		options.Agent.ConversationOptions.DocumentStorage = h.documentFiles
	}
	if err := os.RemoveAll(path + ".parses"); err != nil {
		return nil, fmt.Errorf("remove retired document cache: %w", err)
	}
	if options.Agent.ConversationOptions.AttachmentAuthorizer == nil {
		options.Agent.ConversationOptions.AttachmentAuthorizer = h
	}
	if options.Agent.ConversationOptions.LibraryAuthorizer == nil {
		options.Agent.ConversationOptions.LibraryAuthorizer = h
	}
	if options.Prepare != nil {
		if err = options.Prepare(ctx, h); err != nil {
			return nil, err
		}
	}
	previousToolPolicy := options.Agent.ConversationOptions.BindToolPolicy
	options.Agent.ConversationOptions.BindToolPolicy = func(catalog toolsdk.Catalog, previous toolsdk.Availability) (toolsdk.Availability, error) {
		if previousToolPolicy != nil {
			var err error
			previous, err = previousToolPolicy(catalog, previous)
			if err != nil {
				return nil, err
			}
		}
		return h.bindToolSettings(ctx, catalog, previous)
	}
	h.Agent, err = agentmodule.NewFactory(options.Agent).OpenModule(ctx, agentsdk.ApplicationRef{RuntimeID: options.RuntimeID}, h)
	if err != nil {
		return nil, fmt.Errorf("open Agent module: %w", err)
	}
	binder, ok := h.Agent.(modulehost.ConversationApplicationHostBinder)
	if !ok {
		return nil, fmt.Errorf("Agent module does not support conversation host binding")
	}
	if err = binder.BindConversationHost(h); err != nil {
		return nil, fmt.Errorf("bind Agent conversation host: %w", err)
	}
	// Finish every owner migration, including policies bound during conversation
	// assembly, before releasing the host startup lock.
	if connection.ReleaseStartup != nil {
		if err = connection.ReleaseStartup(); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// Identity and personal capabilities are ready before conversation recovery
// starts. This web host does not need the legacy Runtime task/application ports.
func (*Host) DeferConversationHostBinding() bool                            { return true }
func (h *Host) ConversationAuthorizer() agentsdk.ConversationToolAuthorizer { return h }
func (h *Host) ConversationBusinessSource() agentsdk.ConversationBusinessSource {
	return h.businessSource
}

var _ modulehost.DeferredConversationHost = (*Host)(nil)
var _ modulehost.ConversationApplicationHost = (*Host)(nil)

func (h *Host) RuntimeID() string                         { return h.runtimeID }
func (h *Host) DatabaseProfile() driver.Profile           { return h.profile }
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
		if !h.identityBorrowed {
			if err := h.Identity.Close(ctx); result == nil {
				result = err
			}
		}
		h.Identity = nil
	}
	if h.connection != nil {
		if err := h.connection.Close(); result == nil {
			result = err
		}
		h.connection = nil
		h.db = nil
	}
	if h.storageRelease != nil {
		if err := h.storageRelease(); result == nil {
			result = err
		}
		h.storageRelease = nil
	}
	return result
}
