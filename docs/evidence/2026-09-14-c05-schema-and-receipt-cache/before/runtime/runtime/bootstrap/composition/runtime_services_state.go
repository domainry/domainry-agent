package composition

import (
	"context"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"
	"sync"

	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	deploymentbusiness "github.com/domainry/domainry-runtime/runtime/application/deployment"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workspaceaggregatecontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceaggregate/contract"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

// runtimeAssembly is the private, constructor-only composition graph.
type runtimeAssembly struct {
	templateID, templateVersion, name, timeZone string
	productBrandName                            string
	actionRuntimeRevision                       string
	actionProjectRevision                       string
	actionMetadataRevision                      string
	schema                                      map[string]definitionmodel.ObjectSchema
	actions                                     map[string]definitionmodel.ActionSchema
	workflows                                   map[string]definitionmodel.WorkflowSchema
	schedulerDefinitions                        []map[string]any
	automationRules                             map[string]automationmodel.AutomationRuleSchema
	dictionaries                                []appschemamodel.DictionarySchema
	integrations                                connectormodel.IntegrationSchema
	reports                                     []reportmodel.ReportSchema
	reportObjects                               map[string]struct{}
	skills                                      []agentsdk.SkillSchema
	agents                                      []agentsdk.AgentSchema
	agentTasks                                  []agentsdk.AgentTaskDefinition
	agentEntrypoints                            []agentsdk.AgentEntrypointAssignment
	agentServicePrincipals                      []agentsdk.AgentServicePrincipalBinding
	identityProfileExtensions                   []profilebindingmodel.Binding
	recordRepo                                  recordrepository.RecordRepository
	dataExchange                                dataexchange.Binding
	dataExchangeProviders                       *recordapplication.DataExchangeProviders
	reportObjectSQL                             reportcontract.ReportObjectSQLExecutor
	reportSnapshotSources                       reportcontract.ReportSnapshotSourceVersionReader
	reportExportPrepareReceipts                 reportcontract.ReportExportPrepareReceiptStore
	auditRepo                                   auditrepository.AuditRepository
	auditApplicationService                     *auditapplication.AuditApplicationService
	auditExportTokenKey                         []byte
	schemaService                               *appschemaapplication.ApplicationSchemaQueryApplicationService
	actionService                               *actionapplication.ActionApplicationService
	runtimeStatusService                        *deploymentbusiness.DeploymentRuntimeStatusApplicationService
	*recordservice.RecordQueryPolicyDomainService
	*RecordSchemaSnapshotProvider
	*pipelineapplication.PipelineApplicationService
	*recordservice.RecordValidationDomainService
	recordStateMachineEffects *recordapplication.RecordStateMachineEffectApplicationService
	*actionapplication.ActionPreconditionApplicationService
	recordApplicationService *recordapplication.RecordApplicationService
	*recordservice.RecordDomainService
	recordRuntimeState
	*actionruntime.ActionExecutionRuntime
	businessSystemService               *businesssystemapplication.BusinessSystemApplicationService
	publicationHandoffService           *publicationhandoff.PublicationHandoffApplicationService
	integrationPublicationWorkerRepo    publicationrepository.WorkerRepository
	workerWakeups                       *workerplatform.WakeupBroker
	publicationRepository               publicationrepository.Repository
	integrationOwnerDelivery            integrationsdk.Delivery
	integrationOwnerCatalog             integrationsdk.Catalog
	integrationOwnerManagement          integrationsdk.Management
	integrationOwnerOperations          integrationsdk.Operations
	workflowWorkerRepo                  workflowcontract.WorkflowWorkerStore
	workflowApplicationService          *workflowapplication.WorkflowApplicationService
	applicationSchemaService            *appschemaapplication.ApplicationSchemaApplicationService
	automationApplicationService        *automationapplication.AutomationApplicationService
	workflowDecisionRepo                workflowcontract.WorkflowDecisionStore
	workflowRouteRepo                   workflowcontract.WorkflowRouteStore
	workflowDefinitionRepo              workflowcontract.WorkflowDefinitionStore
	workflowProcessRepo                 workflowcontract.WorkflowProcessStore
	workflowNotificationCompiler        func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	workflowTaskNotificationCommitter   workflowapplication.WorkflowTaskNotificationCommitter
	notificationIntentPublisher         func(context.Context, notificationmodel.NotificationIntent) error
	recordNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	reportNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	reportSnapshotNotificationCommitter ReportSnapshotNotificationCommitter
	automationNotificationCompiler      func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	automationNotificationCommitter     automationapplication.AutomationExecutionNotificationCommitter
	notificationEventPublisher          func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error)
	applicationSchemaRepo               appschemarepository.ApplicationSchemaRepository
	metadataDefinitions                 metadatasdk.Definitions
	metadataLocalization                metadatasdk.Localization
	automationWorkerRepo                automationcontract.AutomationWorkerStore
	automationExecutionRepo             automationrepository.AutomationExecutionRepository
	businessEvidenceRepo                changeplanrepository.ChangePlanEvidenceRepository
	runtimeStatusRepo                   deploymentrepository.DeploymentRuntimeStatusRepository
	identityProjection                  identitysdk.Projection
	identityHandlerDeliveryBinder       identitysdk.HandlerDeliveryUnitOfWorkBinder
	accountErasures                     lifecyclecontract.AccountErasures
	organizationUnitDeliveryBinder      organizationunit.UnitOfWorkBinder
	storeOrganizationDeliveryBinder     identitysdk.StoreOrganizationDeliveryUnitOfWorkBinder
	workspaceIdentityUsageBinder        identitysdk.WorkspaceIdentityUsageUnitOfWorkBinder
	workspaceIdentityUsageCursor        actionapplication.WorkspaceIdentityUsageCursorCodec
	workspaceCommercialConfiguration    actionapplication.WorkspaceCommercialConfigurationLocker
	actionAssuranceStore                actioncontract.ActionAssuranceStore
	workflowProcesses                   *workflowapplication.WorkflowProcessEngine
	connectorRegistry                   *runtimeConnectorCatalog
	verifyFileClean                     func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)
	openVerifiedFile                    func(context.Context, string, runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error)
	issueFileDownload                   func(context.Context, string, runtimeext.Principal, runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error)
	createDerivedFile                   func(context.Context, string, runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error)
	workspaceAggregateCatalog           workspaceaggregatecontract.Catalog
	workspaceActiveResolver             workspaceaggregatecontract.ActiveResolver
	workspaceUsageResolver              workspaceaggregatecontract.UsageResolver
	workspaceAggregateRepository        workspaceaggregatecontract.Repository
	prepareOutboxPayload                publicationhandoff.PayloadPreparer
	authoringCapabilities               *capabilityapplication.CapabilityAuthoringApplicationService
	businessReferences                  *changeplanapplication.ChangePlanReferenceApplicationService
	reportExportsService                *reportexportapplication.ReportExportApplicationService
	reportApplication                   reportsdk.ApplicationBinding
	reportModuleQueryHost               *reportadapter.ReportModuleQueryHost
	reportModuleSnapshotHost            reportmodulehost.SnapshotTerminalCommitter
	reportModuleExportHost              reportmodulehost.ExportGateway
	targetExecutionService              *dispatchapplication.TargetExecutionApplicationService
	schedulerDefinitionSource           schedulerDefinitionSourceAdapter
	recordTimerService                  *recordtimerapplication.RecordTimerApplicationService
	agentAuthorizationService           *agentapplication.AgentAuthorizationApplicationService
	agentTaskDispatchService            *agentapplication.AgentTaskDispatchApplicationService
	agentTaskRunner                     agentsdk.TaskRunner
	identityPrincipals                  identitysdk.PrincipalResolver
	agentScheduledTasks                 agentsdk.ScheduledConversationTaskService
	mu                                  sync.RWMutex // guards concurrent schema reads vs. hot-reload writes
	workerDependencies                  workerplatform.Dependencies
}
