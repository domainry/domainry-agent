package composition

import (
	"context"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordruntime "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordexecutionruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

type recordRuntimeState struct {
	recordMutations                *recordruntime.RecordMutationApplicationService
	mutationKernel                 *recordruntime.MutationKernelApplicationService
	RecordMutationExecutionRuntime *recordexecutionruntime.RecordMutationExecutionRuntime
}

func (s *runtimeAssembly) reportRecordSchemaMap() map[string]definitionmodel.ObjectSchema {
	return schemaObjectMap(recordSchemaObjects(s))
}

func initializeRecordApplications(s *runtimeAssembly) {
	dependencies := buildRecordApplicationDependencies(s)
	if s.dataExchangeProviders != nil {
		s.dataExchangeProviders.ConfigureExportNotifications(s.notificationIntentPublisher, s.workerDependencies.Clock.Now)
	}
	s.mutationKernel = dependencies.MutationKernel
	s.recordApplicationService = recordapplication.NewRecordApplicationService(dependencies)
	s.RecordDomainService = s.recordApplicationService.RecordDomainService
	auditRecordAccess := auditapplication.NewAuditRecordAccessApplicationService(s.recordApplicationService, func() []definitionmodel.ObjectSchema {
		return append([]definitionmodel.ObjectSchema(nil), s.Schema().Objects...)
	})
	s.auditApplicationService.SetEventProjector(auditRecordAccess.ProjectAuditEvents)
	s.recordMutations = recordruntime.NewRecordMutationApplicationService(s.recordApplicationService)
	reportRecords := reportadapter.NewReportRecordAdapter(s.recordApplicationService, s.recordRepo, s.reportRecordSchemaMap)
	businessPrincipals := principalapplication.NewBusinessPrincipalApplicationService(principalapplication.BusinessPrincipalDependencies{
		Records: s.recordRepo,
		Objects: func() []definitionmodel.ObjectSchema {
			return append([]definitionmodel.ObjectSchema(nil), s.Schema().Objects...)
		},
		Extensions: func() []profilebindingmodel.Binding {
			s.mu.RLock()
			defer s.mu.RUnlock()
			return append([]profilebindingmodel.Binding(nil), s.identityProfileExtensions...)
		},
	})
	crossWorkspaceAudit := reportadapter.NewReportCrossWorkspaceAuditAdapter(s.auditApplicationService)
	s.reportModuleQueryHost = reportadapter.NewReportModuleQueryHost(reportadapter.ReportModuleQueryHostDependencies{
		Access: reportRecords, ObjectSQL: s.reportObjectSQL, SnapshotSources: s.reportSnapshotSources,
		AnalysisObjectKeys: func() []string {
			objects := s.reportRecordSchemaMap()
			keys := make([]string, 0, len(objects))
			for key := range objects {
				keys = append(keys, key)
			}
			return keys
		},
		ResolveSubject: func(ctx context.Context, authority reportmodel.ReportAuthority) (principalmodel.Principal, error) {
			if authority.Subject != nil {
				return reportadapter.RuntimePrincipalFromReportSubject(*authority.Subject), nil
			}
			identity, ok := identitysdk.RequestIdentityFromContext(ctx)
			if !ok || strings.TrimSpace(identity.AccessToken) == "" || strings.TrimSpace(authority.AccessToken) != strings.TrimSpace(identity.AccessToken) {
				return principalmodel.Principal{}, &reportsdk.Error{StatusCode: 401, Code: "auth.token_required"}
			}
			principal := principalmodel.NewPrincipalFromIdentity(identity.Principal, strings.TrimSpace(authority.RequestID))
			resolved, err := businessPrincipals.ResolveBusinessPrincipal(ctx, principal, authority.BusinessProfileKey, authority.BusinessProfileID)
			if err != nil {
				return principalmodel.Principal{}, err
			}
			return resolved, nil
		},
		Audit: crossWorkspaceAudit.AppendCrossWorkspaceExecution,
	})
	s.reportModuleSnapshotHost = reportModuleSnapshotHost{compile: s.reportNotificationCompiler, committer: s.reportSnapshotNotificationCommitter}
	s.reportExportsService = reportexportapplication.NewReportExportApplicationService(reportexportapplication.ReportExportApplicationDependencies{
		ProductBrandName: s.productBrandName, Records: reportRecords, Audit: s.auditApplicationService,
		DataExchange: s.dataExchange, DataExchangeProviders: s.dataExchangeProviders, PrepareReceipts: s.reportExportPrepareReceipts,
	})
	s.reportModuleExportHost = reportModuleExportHost{service: s.reportExportsService}
}
