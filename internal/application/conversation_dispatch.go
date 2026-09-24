package application

import (
	"context"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Shared transport dispatch; identity is established by the owning transport.
func InvokeConversation(ctx context.Context, s agentsdk.ConversationService, op string, r agentsdk.ConversationRPCRequest) (any, error) {
	if op == "scheduled_task_start" {
		tasks, ok := s.(agentsdk.ScheduledConversationTaskService)
		if !ok {
			return nil, conversationFailure("unavailable", "scheduled_tasks_unavailable")
		}
		return tasks.StartScheduledConversationTask(ctx, r.ScheduledTask)
	}
	if op == "business_event_task_accept" {
		tasks, ok := s.(agentsdk.BusinessEventConversationTaskService)
		if !ok {
			return nil, conversationFailure("unavailable", "business_event_tasks_unavailable")
		}
		return tasks.AcceptBusinessEventConversationTask(ctx, r.BusinessEventTask)
	}
	a := r.Authority
	if op == "sources_verify" {
		verifier, ok := s.(agentsdk.ConversationSourceVerifier)
		if !ok {
			return nil, conversationFailure("unavailable", "source_verification_unavailable")
		}
		request := r.SourceVerification
		request.Reader = a
		return verifier.VerifyConversationSources(ctx, request)
	}
	if strings.HasPrefix(op, "external_agent_") {
		external, ok := s.(agentsdk.ConversationExternalAgentService)
		if !ok {
			return nil, conversationFailure("unavailable", "external_agent_unavailable")
		}
		switch op {
		case "external_agent_assignments":
			return external.ConversationExternalAgentAssignments(ctx, r.ExternalAgentQuery, a)
		case "external_agent_claim":
			return external.ClaimConversationExternalAgentTask(ctx, r.TaskID, r.ExternalAgentClaim, a)
		case "external_agent_report":
			return external.ReportConversationExternalAgentTask(ctx, r.TaskID, r.ExternalAgentReport, a)
		}
	}
	if strings.HasPrefix(op, "skills_") || strings.HasPrefix(op, "improvements_") || op == "feedback_create" {
		skills, ok := s.(agentsdk.ConversationSkillService)
		if !ok {
			return nil, conversationFailure("unavailable", "skills_unavailable")
		}
		switch op {
		case "skills_list":
			return skills.ConversationSkills(ctx, a)
		case "skills_get":
			return skills.ConversationSkill(ctx, r.SkillKey, r.SkillVersion, a)
		case "skills_resource_get":
			return skills.ConversationSkillResource(ctx, r.SkillKey, r.SkillVersion, r.SkillResourceKey, a)
		case "feedback_create":
			return skills.CreateConversationCapabilityFeedback(ctx, r.CapabilityFeedback, a)
		case "improvements_list":
			return skills.ConversationImprovementCandidates(ctx, a)
		case "improvements_create":
			return skills.CreateConversationImprovementCandidate(ctx, r.ImprovementCandidate, a)
		case "improvements_evaluate":
			return skills.EvaluateConversationImprovementCandidate(ctx, r.CandidateID, r.ImprovementEvaluation, a)
		case "improvements_publish":
			return skills.PublishConversationImprovementCandidate(ctx, r.CandidateID, r.ImprovementPublish, a)
		case "improvements_rollback":
			return skills.RollbackConversationImprovement(ctx, r.CandidateID, r.ImprovementRollback, a)
		}
	}
	if strings.HasPrefix(op, "agents_") || strings.HasPrefix(op, "delegations_") {
		peers, ok := s.(agentsdk.ConversationCollaborationService)
		if !ok {
			return nil, conversationFailure("unavailable", "collaboration_unavailable")
		}
		switch op {
		case "agents_access":
			return peers.ConversationCollaborationAccess(ctx, a)
		case "agents_match":
			return peers.MatchConversationAgents(ctx, r.AgentMatch, a)
		case "agents_list":
			return peers.ConversationAgents(ctx, a)
		case "agents_create":
			return peers.WriteConversationAgent(ctx, "", r.AgentWrite, a)
		case "agents_update":
			return peers.WriteConversationAgent(ctx, r.AgentID, r.AgentWrite, a)
		case "delegations_list":
			return peers.ConversationDelegations(ctx, r.TaskQuery.SourceConversationID, a)
		case "delegations_create":
			return peers.CreateConversationDelegation(ctx, r.DelegationCreate, a)
		case "delegations_history":
			return peers.ConversationAgreementHistory(ctx, r.DelegationID, r.AgreementBefore, a)
		case "delegations_disagreement":
			return peers.ConversationDisagreementHistory(ctx, r.DelegationID, r.DisagreementID, r.AgreementBefore, a)
		case "delegations_deliveries":
			return peers.ConversationDeliveryHistory(ctx, r.DelegationID, r.AgreementBefore, a)
		case "delegations_result":
			reader, ok := s.(agentsdk.ConversationDeliveryResultReader)
			if !ok {
				return nil, conversationFailure("unavailable", "result_read_unavailable")
			}
			return reader.ReadConversationDeliveryResult(ctx, r.DelegationID, r.DeliveryResultRead, a)
		case "delegations_execution_publications":
			owner, ok := s.(agentsdk.ConversationExecutionPublicationOwnerService)
			if !ok {
				return nil, conversationFailure("unavailable", "execution_publication_controls_unavailable")
			}
			return owner.ConversationDelegationExecutionPublications(ctx, r.DelegationID, a)
		case "delegations_execution_share", "delegations_executions", "delegations_execution", "delegations_execution_result":
			sharing, ok := s.(agentsdk.ConversationExecutionSharingService)
			if !ok {
				return nil, conversationFailure("unavailable", "execution_sharing_unavailable")
			}
			switch op {
			case "delegations_execution_share":
				return sharing.PublishConversationDelegationExecution(ctx, r.DelegationID, r.ExecutionShare, a)
			case "delegations_executions":
				return sharing.ConversationDelegationExecutions(ctx, r.DelegationID, a)
			case "delegations_execution":
				return sharing.ReadConversationDelegationExecution(ctx, r.DelegationID, r.ExecutionReference, a)
			default:
				return sharing.ReadConversationDelegationExecutionResult(ctx, r.DelegationID, r.ResultRead, a)
			}
		case "delegations_publication":
			reader, ok := s.(agentsdk.ConversationDeliveryPublicationReader)
			if !ok {
				return nil, conversationFailure("unavailable", "delivery_publication_unavailable")
			}
			return reader.PreviewConversationDeliveryPublication(ctx, r.DelegationID, r.DeliveryPublication, a)
		case "delegations_publications":
			reader, ok := s.(agentsdk.ConversationDeliveryPublicationReader)
			if !ok {
				return nil, conversationFailure("unavailable", "delivery_publication_unavailable")
			}
			return reader.ConversationDeliveryPublicationCandidates(ctx, r.DelegationID, r.AgreementBefore, a)
		case "delegations_contract_publication", "delegations_contract_candidates", "delegations_contract_publications":
			reader, ok := s.(agentsdk.ConversationContractPublicationReader)
			if !ok {
				return nil, conversationFailure("unavailable", "contract_publication_unavailable")
			}
			if op == "delegations_contract_publication" {
				return reader.PreviewConversationContractPublication(ctx, r.DelegationID, r.ContractPublication, a)
			}
			if op == "delegations_contract_candidates" {
				return reader.ConversationContractPublicationCandidates(ctx, r.DelegationID, r.AgreementBefore, a)
			}
			return reader.ConversationContractPublicationHistory(ctx, r.DelegationID, r.AgreementBefore, a)
		case "delegations_artifact", "delegations_export":
			reader, ok := s.(agentsdk.ConversationDeliveryArtifactReader)
			if !ok {
				return nil, conversationFailure("unavailable", "artifacts_unavailable")
			}
			if op == "delegations_export" {
				return reader.DownloadConversationDeliveryArtifact(ctx, r.DelegationID, r.DeliveryArtifactRead, a)
			}
			return reader.ReadConversationDeliveryArtifact(ctx, r.DelegationID, r.DeliveryArtifactRead, a)
		case "delegations_get":
			return peers.ConversationDelegation(ctx, r.DelegationID, a)
		case "delegations_update":
			return peers.UpdateConversationDelegation(ctx, r.DelegationID, r.DelegationUpdate, a)
		case "delegations_message":
			return peers.SendConversationAgentMessage(ctx, r.DelegationID, r.AgentMessage, a)
		}
	}
	if op == "result_read" {
		reader, ok := s.(agentsdk.ConversationResultReader)
		if !ok {
			return nil, conversationFailure("unavailable", "result_read_unavailable")
		}
		return reader.ReadResult(ctx, r.ConversationID, r.RunID, r.ResultRead, a)
	}
	if op == "libraries_sources" || op == "libraries_bind_source" {
		sources, ok := s.(agentsdk.KnowledgeDatasourceService)
		if !ok {
			return nil, conversationFailure("unavailable", "datasources_unavailable")
		}
		if op == "libraries_sources" {
			return sources.KnowledgeLibrarySources(ctx, r.LibraryID, r.LibraryAfter, r.Limit, a)
		}
		return sources.BindKnowledgeLibrarySource(ctx, r.LibraryID, r.LibrarySourceWrite, a)
	}
	if op == "documents_transfer" {
		transfer, ok := s.(agentsdk.KnowledgeDocumentTransferService)
		if !ok {
			return nil, conversationFailure("unavailable", "document_transfer_unavailable")
		}
		return transfer.TransferKnowledgeDocument(ctx, r.LibraryID, r.DocumentTransfer, a)
	}
	if op == "documents_import_attachment" {
		importer, ok := s.(agentsdk.KnowledgeAttachmentImportService)
		if !ok {
			return nil, conversationFailure("unavailable", "document_import_unavailable")
		}
		return importer.ImportConversationAttachment(ctx, r.LibraryID, r.DocumentImport, a)
	}
	if strings.HasPrefix(op, "libraries_") {
		libraries, ok := s.(agentsdk.KnowledgeLibraryService)
		if !ok {
			return nil, conversationFailure("unavailable", "libraries_unavailable")
		}
		switch op {
		case "libraries_create":
			return libraries.CreateKnowledgeLibrary(ctx, r.LibraryCreate, a)
		case "libraries_list":
			return libraries.KnowledgeLibraries(ctx, r.LibraryAfter, r.Limit, a)
		case "libraries_get":
			return libraries.KnowledgeLibrary(ctx, r.LibraryID, a)
		case "libraries_update":
			return libraries.UpdateKnowledgeLibrary(ctx, r.LibraryID, r.LibraryUpdate, a)
		case "libraries_members":
			return libraries.KnowledgeLibraryMembers(ctx, r.LibraryID, r.LibraryAfter, r.Limit, a)
		case "libraries_set_member":
			return libraries.SetKnowledgeLibraryMember(ctx, r.LibraryID, r.LibraryUserID, r.LibraryMemberWrite, a)
		case "libraries_remove_member":
			return libraries.RemoveKnowledgeLibraryMember(ctx, r.LibraryID, r.LibraryUserID, r.Revision, a)
		}
	}
	if strings.HasPrefix(op, "documents_") {
		documents, ok := s.(agentsdk.KnowledgeDocumentService)
		if !ok {
			return nil, conversationFailure("unavailable", "documents_unavailable")
		}
		switch op {
		case "documents_upload":
			return documents.UploadKnowledgeDocument(ctx, r.LibraryID, r.DocumentUpload, a)
		case "documents_list":
			return documents.KnowledgeDocuments(ctx, r.LibraryID, r.DocumentAfter, r.Limit, a)
		case "documents_get":
			return documents.KnowledgeDocument(ctx, r.LibraryID, r.DocumentID, a)
		case "documents_download":
			return documents.DownloadKnowledgeDocument(ctx, r.LibraryID, r.DocumentID, a)
		case "documents_delete":
			return documents.DeleteKnowledgeDocument(ctx, r.LibraryID, r.DocumentID, r.Revision, a)
		}
	}
	if strings.HasPrefix(op, "attachments_") {
		attachments, ok := s.(agentsdk.ConversationAttachmentService)
		if !ok {
			return nil, conversationFailure("unavailable", "attachments_unavailable")
		}
		switch op {
		case "attachments_upload":
			return attachments.UploadAttachment(ctx, r.ConversationID, r.AttachmentUpload, a)
		case "attachments_index":
			indexing, ok := s.(agentsdk.ConversationAttachmentIndexService)
			if !ok {
				return nil, conversationFailure("unavailable", "attachment_index_unavailable")
			}
			return indexing.IndexAttachment(ctx, r.ConversationID, r.AttachmentID, r.Revision, a)
		case "attachments_check_index":
			checking, ok := s.(agentsdk.ConversationAttachmentIndexCheckService)
			if !ok {
				return nil, conversationFailure("unavailable", "attachment_index_unavailable")
			}
			return checking.CheckAttachmentIndex(ctx, r.ConversationID, r.AttachmentID, r.Revision, a)
		case "attachments_list":
			return attachments.Attachments(ctx, r.ConversationID, r.AttachmentAfter, r.Limit, a)
		case "attachments_get":
			return attachments.Attachment(ctx, r.ConversationID, r.AttachmentID, a)
		case "attachments_download":
			return attachments.DownloadAttachment(ctx, r.ConversationID, r.AttachmentID, a)
		case "attachments_delete":
			return attachments.DeleteAttachment(ctx, r.ConversationID, r.AttachmentID, r.Revision, a)
		}
	}
	if strings.HasPrefix(op, "artifacts_") {
		artifacts, ok := s.(agentsdk.ConversationArtifactService)
		if !ok {
			return nil, conversationFailure("unavailable", "artifacts_unavailable")
		}
		switch op {
		case "artifacts_list":
			return artifacts.Artifacts(ctx, r.ArtifactQuery, a)
		case "artifacts_get":
			return artifacts.Artifact(ctx, r.ArtifactID, r.ArtifactVersion, a)
		case "artifacts_versions":
			return artifacts.ArtifactVersions(ctx, r.ArtifactID, r.ArtifactBefore, r.Limit, a)
		case "artifacts_create":
			return artifacts.CreateArtifact(ctx, r.ArtifactCreate, a)
		case "artifacts_edit":
			return artifacts.EditArtifact(ctx, r.ArtifactID, r.ArtifactEdit, a)
		case "artifacts_export":
			return artifacts.ExportArtifact(ctx, r.ArtifactID, r.ArtifactExport, a)
		case "artifacts_download":
			return artifacts.DownloadArtifact(ctx, r.ArtifactExportID, a)
		}
	}
	if strings.HasPrefix(op, "todos_") {
		todos, ok := s.(agentsdk.ConversationTodoService)
		if !ok {
			return nil, conversationFailure("unavailable", "todos_unavailable")
		}
		switch op {
		case "todos_list":
			return todos.Todos(ctx, r.TodoQuery, a)
		case "todos_get":
			return todos.Todo(ctx, r.TodoID, a)
		case "todos_create":
			return todos.CreateTodos(ctx, r.TodoCreate, a)
		case "todos_update":
			return todos.UpdateTodo(ctx, r.TodoID, r.TodoUpdate, a)
		case "todos_delete":
			err := todos.DeleteTodo(ctx, r.TodoID, r.TodoDelete, a)
			return map[string]bool{"deleted": err == nil}, err
		}
	}
	if strings.HasPrefix(op, "tasks_") {
		tasks, ok := s.(agentsdk.ConversationTaskService)
		if !ok {
			return nil, conversationFailure("unavailable", "tasks_unavailable")
		}
		switch op {
		case "tasks_list":
			return tasks.ConversationTasks(ctx, r.TaskQuery, a)
		case "tasks_get":
			return tasks.ConversationTask(ctx, r.TaskID, a)
		case "tasks_plans":
			plans, ok := s.(agentsdk.ConversationTaskPlanService)
			if !ok {
				return nil, conversationFailure("unavailable", "plans_unavailable")
			}
			return plans.ConversationTaskPlans(ctx, r.TaskID, r.PlanBefore, a)
		case "tasks_completions", "tasks_completion_review":
			completion, ok := s.(agentsdk.ConversationTaskCompletionService)
			if !ok {
				return nil, conversationFailure("unavailable", "task_completion_unavailable")
			}
			if op == "tasks_completions" {
				return completion.ConversationTaskCompletionHistory(ctx, r.TaskID, r.CompletionBefore, a)
			}
			return completion.ReviewConversationTaskCompletion(ctx, r.TaskID, r.TaskCompletionReview, a)
		case "tasks_update":
			agreements, ok := s.(agentsdk.ConversationTaskAgreementService)
			if !ok {
				return nil, conversationFailure("unavailable", "task_agreement_unavailable")
			}
			return agreements.UpdateConversationTaskAgreement(ctx, r.TaskID, r.TaskAgreementUpdate, a)
		case "tasks_cancel", "tasks_resume":
			controls, ok := s.(agentsdk.ConversationTaskControlService)
			if !ok {
				return nil, conversationFailure("unavailable", "task_control_unavailable")
			}
			if op == "tasks_cancel" {
				return controls.CancelConversationTask(ctx, r.TaskID, a)
			}
			return controls.ResumeConversationTask(ctx, r.TaskID, a)
		}
	}
	switch op {
	case "create":
		return s.Create(ctx, r.Create, a)
	case "list":
		return s.List(ctx, r.Query, a)
	case "get":
		return s.Get(ctx, r.ConversationID, a)
	case "update":
		return s.Update(ctx, r.ConversationID, r.Update, a)
	case "delete":
		err := s.Delete(ctx, r.ConversationID, r.Revision, a)
		return map[string]bool{"deleted": err == nil}, err
	case "send":
		return s.Send(ctx, r.ConversationID, r.Send, a)
	case "messages":
		return s.Messages(ctx, r.ConversationID, r.Messages, a)
	case "run":
		return s.Run(ctx, r.ConversationID, r.RunID, a)
	case "conversation_fork", "trajectory_get", "trajectory_export", "trajectory_replay", "trajectory_compare":
		trajectories, ok := s.(agentsdk.ConversationTrajectoryService)
		if !ok {
			return nil, conversationFailure("unavailable", "trajectory_unavailable")
		}
		switch op {
		case "conversation_fork":
			return trajectories.ForkConversation(ctx, r.ConversationID, r.RunID, r.Fork, a)
		case "trajectory_get":
			return trajectories.ConversationTrajectory(ctx, r.ConversationID, r.RunID, a)
		case "trajectory_export":
			return trajectories.ExportConversationTrajectory(ctx, r.ConversationID, r.RunID, a)
		case "trajectory_replay":
			return trajectories.ReplayConversationTrajectory(ctx, r.ConversationID, r.RunID, r.TrajectoryReplay, a)
		default:
			return trajectories.CompareConversationTrajectories(ctx, r.ConversationID, r.RunID, r.TrajectoryCompare, a)
		}
	case "events":
		return s.Events(ctx, r.ConversationID, r.RunID, r.AfterSeq, r.Limit, a)
	case "cancel":
		return s.Cancel(ctx, r.ConversationID, r.RunID, a)
	case "resume":
		return s.Resume(ctx, r.ConversationID, r.RunID, a)
	case "respond":
		if interactions, ok := s.(agentsdk.ConversationInteractionService); ok {
			return interactions.Respond(ctx, r.ConversationID, r.RunID, r.Response, a)
		}
		return nil, conversationFailure("unavailable", "interaction_unavailable")
	case "memories_list":
		return s.Memories(ctx, a)
	case "memories_write":
		return s.WriteMemory(ctx, r.Memory, a)
	case "memories_delete":
		err := s.DeleteMemory(ctx, r.MemoryID, r.Revision, a)
		return map[string]bool{"deleted": err == nil}, err
	default:
		return nil, conversationFailure("not_found", "operation_not_found")
	}
}
