package composition

import (
	"context"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
)

// newRuntimeServicesState allocates state before ordered service initialization.
func newRuntimeServicesState(ctx context.Context, manifest manifestmodel.ManifestSchema, deps RuntimeServicesDependencies) *runtimeAssembly {
	dataExchangeProviders := deps.DataExchangeProviders
	if dataExchangeProviders == nil && deps.DataExchange != nil {
		dataExchangeProviders = recordapplication.NewDataExchangeProviders(nil)
	}
	identityProjection := deps.IdentityProjection
	connectorRegistry := newRuntimeConnectorCatalog(manifest.Integrations)
	auditApplicationService := deps.AuditApplication
	if auditApplicationService == nil {
		auditApplicationService = auditapplication.NewAuditApplicationService(deps.Audit)
	}
	services := &runtimeAssembly{
		schedulerDefinitions:                cloneSchedulerDefinitionMaps(manifest.SchedulerDefinitions),
		productBrandName:                    deps.ProductBrandName,
		actionRuntimeRevision:               deps.ActionRuntimeRevision,
		actionProjectRevision:               deps.ActionProjectRevision,
		actionMetadataRevision:              deps.ActionMetadataRevision,
		recordRepo:                          deps.Records,
		dataExchange:                        deps.DataExchange,
		dataExchangeProviders:               dataExchangeProviders,
		reportObjectSQL:                     deps.ReportObjectSQL,
		reportSnapshotSources:               deps.ReportSnapshotSources,
		reportExportPrepareReceipts:         deps.ReportExportPrepareReceipts,
		auditRepo:                           deps.Audit,
		publicationRepository:               deps.IntegrationPublication,
		integrationPublicationWorkerRepo:    deps.IntegrationPublicationWorker,
		workerWakeups:                       deps.WorkerWakeups,
		integrationOwnerDelivery:            deps.IntegrationOwnerDelivery,
		integrationOwnerCatalog:             deps.IntegrationOwnerCatalog,
		integrationOwnerManagement:          deps.IntegrationOwnerManagement,
		integrationOwnerOperations:          deps.IntegrationOwnerOperations,
		workflowWorkerRepo:                  deps.WorkflowWorker,
		workflowDefinitionRepo:              deps.WorkflowDefinitions,
		workflowProcessRepo:                 deps.WorkflowProcesses,
		workflowDecisionRepo:                deps.WorkflowDecisions,
		workflowRouteRepo:                   deps.WorkflowRoutes,
		workflowNotificationCompiler:        deps.WorkflowNotificationCompiler,
		workflowTaskNotificationCommitter:   deps.WorkflowTaskNotificationCommitter,
		notificationIntentPublisher:         deps.NotificationIntentPublisher,
		recordNotificationCompiler:          deps.RecordNotificationCompiler,
		reportNotificationCompiler:          deps.ReportNotificationCompiler,
		reportSnapshotNotificationCommitter: deps.ReportSnapshotNotificationCommitter,
		automationNotificationCompiler:      deps.AutomationNotificationCompiler,
		automationNotificationCommitter:     deps.AutomationNotificationCommitter,
		notificationEventPublisher:          deps.NotificationEventPublisher,
		applicationSchemaRepo:               deps.ApplicationSchema,
		metadataDefinitions:                 deps.MetadataDefinitions,
		metadataLocalization:                deps.MetadataLocalization,
		automationWorkerRepo:                deps.AutomationWorker,
		automationExecutionRepo:             deps.AutomationExecutions,
		businessEvidenceRepo:                deps.BusinessEvidence,
		runtimeStatusRepo:                   deps.RuntimeStatus,
		actionAssuranceStore:                deps.ActionAssurance,
		connectorRegistry:                   connectorRegistry,
		verifyFileClean:                     deps.VerifyFileClean,
		openVerifiedFile:                    deps.OpenVerifiedFile,
		issueFileDownload:                   deps.IssueFileDownload,
		createDerivedFile:                   deps.CreateDerivedFile,
		workspaceAggregateCatalog:           deps.WorkspaceAggregateCatalog,
		workspaceActiveResolver:             deps.WorkspaceActiveResolver,
		workspaceUsageResolver:              deps.WorkspaceUsageResolver,
		workspaceAggregateRepository:        deps.WorkspaceAggregateRepository,
		prepareOutboxPayload:                deps.PrepareOutboxPayload,
		identityProjection:                  identityProjection,
		identityHandlerDeliveryBinder:       deps.IdentityHandlerDeliveryBinder,
		accountErasures:                     deps.AccountErasures,
		organizationUnitDeliveryBinder:      deps.OrganizationUnitDeliveryBinder,
		storeOrganizationDeliveryBinder:     deps.StoreOrganizationDeliveryBinder,
		workspaceIdentityUsageBinder:        deps.WorkspaceIdentityUsageBinder,
		workspaceIdentityUsageCursor:        deps.WorkspaceIdentityUsageCursor,
		workspaceCommercialConfiguration:    deps.WorkspaceCommercialConfiguration,
		auditApplicationService:             auditApplicationService,
		auditExportTokenKey:                 append([]byte(nil), deps.AuditExportTokenKey...),
		workerDependencies:                  workerplatform.NormalizeDependencies(deps.Worker),
	}
	services.RecordSchemaSnapshotProvider = &RecordSchemaSnapshotProvider{snapshot: func() appschemamodel.ApplicationSchemaSnapshot { return recordSchemaSnapshot(services) }}
	services.ActionExecutionRuntime = actionruntime.NewActionExecutionRuntime(deps.ActionExecutions)
	identityPrincipals := deps.IdentityPrincipals
	services.identityPrincipals = identityPrincipals
	services.agentScheduledTasks = deps.AgentScheduledTasks
	services.agentAuthorizationService = agentapplication.NewAgentAuthorizationApplicationService(agentapplication.AgentAuthorizationDependencies{Principals: identityPrincipals, Schema: services.RecordSchemaSnapshotProvider, Records: runtimeAgentRecordVisibility{records: services}})
	services.agentTaskDispatchService = agentapplication.NewAgentTaskDispatchApplicationService(services.agentAuthorizationService, services.workerDependencies.Clock, services.workerDependencies.IDs)
	services.agentTaskRunner = deps.AgentTaskRunner
	// Agent owns the asynchronous task worker. Runtime keeps only the stable SDK
	// capability used by workflow nodes and no longer claims Agent task rows.
	services.RecordMutationExecutionRuntime = recordruntime.NewRecordMutationExecutionRuntime(deps.RecordExecutions)
	return services
}
