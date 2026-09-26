package main

import (
	"context"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/consents"
	"arena-portal-backend/internal/modules/generation"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/modules/settings"
	"arena-portal-backend/internal/platform/httpx"
)

// api реализует gen.StrictServerInterface: явный адаптер, один метод на
// операцию контракта (D-17). Каждый метод — одна строка, которая вызывает
// метод модуля с тем же именем; операции, чей модуль ещё не собран,
// отвечают общей ошибкой «ещё не реализовано» → 501 problem+json.
//
// Нарочно без встраивания (gen.Unimplemented или похожего): при неверной
// сигнатуре метода модуля embedding молча подставил бы заглушку вместо
// ошибки компиляции — тогда расхождение с контрактом всплыло бы 501 в
// работе, а не на сборке (D-17).
//
// Секции идут в порядке arena-portal-backend-architecture.md 13.2 (адрес →
// модуль). У модуля, для которого этап ещё не наступил, поля в структуре
// нет — оно появляется вместе с его этапом плана.
type api struct {
	auth       *auth.Transport
	people     *people.Transport
	audit      *audit.Transport
	settings   *settings.Transport
	scenarios  *scenarios.Transport
	generation *generation.Transport
	profiles   *profiles.Transport
	consents   *consents.Transport
}

var _ gen.StrictServerInterface = (*api)(nil)

func notImplemented() error {
	return httpx.NotImplemented()
}

// --- auth (этап 02) ---

func (a *api) PortalLogin(ctx context.Context, request gen.PortalLoginRequestObject) (gen.PortalLoginResponseObject, error) {
	return a.auth.PortalLogin(ctx, request)
}

func (a *api) PortalLogout(ctx context.Context, request gen.PortalLogoutRequestObject) (gen.PortalLogoutResponseObject, error) {
	return a.auth.PortalLogout(ctx, request)
}

func (a *api) PortalMe(ctx context.Context, request gen.PortalMeRequestObject) (gen.PortalMeResponseObject, error) {
	return a.auth.PortalMe(ctx, request)
}

func (a *api) ListUsers(ctx context.Context, request gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	return a.auth.ListUsers(ctx, request)
}

func (a *api) CreateUser(ctx context.Context, request gen.CreateUserRequestObject) (gen.CreateUserResponseObject, error) {
	return a.auth.CreateUser(ctx, request)
}

func (a *api) UpdateUser(ctx context.Context, request gen.UpdateUserRequestObject) (gen.UpdateUserResponseObject, error) {
	return a.auth.UpdateUser(ctx, request)
}

func (a *api) SetUserGroupAccess(ctx context.Context, request gen.SetUserGroupAccessRequestObject) (gen.SetUserGroupAccessResponseObject, error) {
	return a.auth.SetUserGroupAccess(ctx, request)
}

// --- people (этап 02) ---

func (a *api) ListGroups(ctx context.Context, request gen.ListGroupsRequestObject) (gen.ListGroupsResponseObject, error) {
	return a.people.ListGroups(ctx, request)
}

func (a *api) CreateGroup(ctx context.Context, request gen.CreateGroupRequestObject) (gen.CreateGroupResponseObject, error) {
	return a.people.CreateGroup(ctx, request)
}

func (a *api) UpdateGroup(ctx context.Context, request gen.UpdateGroupRequestObject) (gen.UpdateGroupResponseObject, error) {
	return a.people.UpdateGroup(ctx, request)
}

func (a *api) ListPeople(ctx context.Context, request gen.ListPeopleRequestObject) (gen.ListPeopleResponseObject, error) {
	return a.people.ListPeople(ctx, request)
}

func (a *api) CreatePerson(ctx context.Context, request gen.CreatePersonRequestObject) (gen.CreatePersonResponseObject, error) {
	return a.people.CreatePerson(ctx, request)
}

func (a *api) GetPersonCard(ctx context.Context, request gen.GetPersonCardRequestObject) (gen.GetPersonCardResponseObject, error) {
	return a.people.GetPersonCard(ctx, request)
}

func (a *api) UpdatePerson(ctx context.Context, request gen.UpdatePersonRequestObject) (gen.UpdatePersonResponseObject, error) {
	return a.people.UpdatePerson(ctx, request)
}

func (a *api) WithdrawConsent(ctx context.Context, request gen.WithdrawConsentRequestObject) (gen.WithdrawConsentResponseObject, error) {
	return a.people.WithdrawConsent(ctx, request)
}

// --- consents (этап 06) ---

func (a *api) TrainerRecordConsents(ctx context.Context, request gen.TrainerRecordConsentsRequestObject) (gen.TrainerRecordConsentsResponseObject, error) {
	return a.consents.TrainerRecordConsents(ctx, request)
}

func (a *api) RecordWrittenConsent(ctx context.Context, request gen.RecordWrittenConsentRequestObject) (gen.RecordWrittenConsentResponseObject, error) {
	return a.consents.RecordWrittenConsent(ctx, request)
}

// --- profiles (этап 06) ---

func (a *api) ListTrainerProfiles(ctx context.Context, request gen.ListTrainerProfilesRequestObject) (gen.ListTrainerProfilesResponseObject, error) {
	return a.profiles.ListTrainerProfiles(ctx, request)
}

func (a *api) CreateTrainerProfile(ctx context.Context, request gen.CreateTrainerProfileRequestObject) (gen.CreateTrainerProfileResponseObject, error) {
	return a.profiles.CreateTrainerProfile(ctx, request)
}

func (a *api) GetTrainerProfile(ctx context.Context, request gen.GetTrainerProfileRequestObject) (gen.GetTrainerProfileResponseObject, error) {
	return a.profiles.GetTrainerProfile(ctx, request)
}

func (a *api) UpdateTrainerProfile(ctx context.Context, request gen.UpdateTrainerProfileRequestObject) (gen.UpdateTrainerProfileResponseObject, error) {
	return a.profiles.UpdateTrainerProfile(ctx, request)
}

func (a *api) ReplaceTrainerProfileKeys(ctx context.Context, request gen.ReplaceTrainerProfileKeysRequestObject) (gen.ReplaceTrainerProfileKeysResponseObject, error) {
	return a.profiles.ReplaceTrainerProfileKeys(ctx, request)
}

func (a *api) CheckTrainerProfile(ctx context.Context, request gen.CheckTrainerProfileRequestObject) (gen.CheckTrainerProfileResponseObject, error) {
	return a.profiles.CheckTrainerProfile(ctx, request)
}

func (a *api) ArchiveTrainerProfile(ctx context.Context, request gen.ArchiveTrainerProfileRequestObject) (gen.ArchiveTrainerProfileResponseObject, error) {
	return a.profiles.ArchiveTrainerProfile(ctx, request)
}

// --- scenarios (этап 04) ---

func (a *api) ListScenarios(ctx context.Context, request gen.ListScenariosRequestObject) (gen.ListScenariosResponseObject, error) {
	return a.scenarios.ListScenarios(ctx, request)
}

func (a *api) CreateScenario(ctx context.Context, request gen.CreateScenarioRequestObject) (gen.CreateScenarioResponseObject, error) {
	return a.scenarios.CreateScenario(ctx, request)
}

func (a *api) ListScenarioTemplates(ctx context.Context, request gen.ListScenarioTemplatesRequestObject) (gen.ListScenarioTemplatesResponseObject, error) {
	return a.scenarios.ListScenarioTemplates(ctx, request)
}

func (a *api) ImportScenario(ctx context.Context, request gen.ImportScenarioRequestObject) (gen.ImportScenarioResponseObject, error) {
	return a.scenarios.ImportScenario(ctx, request)
}

func (a *api) GetScenario(ctx context.Context, request gen.GetScenarioRequestObject) (gen.GetScenarioResponseObject, error) {
	return a.scenarios.GetScenario(ctx, request)
}

func (a *api) GetDraft(ctx context.Context, request gen.GetDraftRequestObject) (gen.GetDraftResponseObject, error) {
	return a.scenarios.GetDraft(ctx, request)
}

func (a *api) SaveDraft(ctx context.Context, request gen.SaveDraftRequestObject) (gen.SaveDraftResponseObject, error) {
	return a.scenarios.SaveDraft(ctx, request)
}

func (a *api) CheckDraft(ctx context.Context, request gen.CheckDraftRequestObject) (gen.CheckDraftResponseObject, error) {
	return a.scenarios.CheckDraft(ctx, request)
}

func (a *api) PublishScenario(ctx context.Context, request gen.PublishScenarioRequestObject) (gen.PublishScenarioResponseObject, error) {
	return a.scenarios.PublishScenario(ctx, request)
}

func (a *api) ListVersions(ctx context.Context, request gen.ListVersionsRequestObject) (gen.ListVersionsResponseObject, error) {
	return a.scenarios.ListVersions(ctx, request)
}

func (a *api) GetVersion(ctx context.Context, request gen.GetVersionRequestObject) (gen.GetVersionResponseObject, error) {
	return a.scenarios.GetVersion(ctx, request)
}

func (a *api) ExportScenario(ctx context.Context, request gen.ExportScenarioRequestObject) (gen.ExportScenarioResponseObject, error) {
	return a.scenarios.ExportScenario(ctx, request)
}

func (a *api) ArchiveScenario(ctx context.Context, request gen.ArchiveScenarioRequestObject) (gen.ArchiveScenarioResponseObject, error) {
	return a.scenarios.ArchiveScenario(ctx, request)
}

// --- generation (этап 05) ---
//
// GetGeneration принадлежит контракту и архитектуре 13.2 — реализована модулем
// generation, D-31 (свой тип ответа вместо устаревшего gen.GenerationState).
// ConfirmGenerationStep и RegenerateGenerationStep — из старого пути авторства
// (анкета из десяти полей, шаговое подтверждение): CLAUDE.md отменяет его,
// arena-api.yaml по авторству отстаёт (D-05) и всё ещё их генерирует. Ни один
// модуль их не реализует — это два метода, которые остаются 501 навсегда, а
// не до какого-то этапа. Остальные четыре адреса авторства в контракте нет —
// они монтируются в cmd/portal/router.go прямо на chi, в обход этого адаптера.

func (a *api) GetGeneration(ctx context.Context, request gen.GetGenerationRequestObject) (gen.GetGenerationResponseObject, error) {
	return a.generation.GetGeneration(ctx, request)
}

func (a *api) ConfirmGenerationStep(ctx context.Context, request gen.ConfirmGenerationStepRequestObject) (gen.ConfirmGenerationStepResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) RegenerateGenerationStep(ctx context.Context, request gen.RegenerateGenerationStepRequestObject) (gen.RegenerateGenerationStepResponseObject, error) {
	return nil, notImplemented()
}

// --- rehearsals (этапы 04/10) ---

func (a *api) TrainerRedeemRehearsalLink(ctx context.Context, request gen.TrainerRedeemRehearsalLinkRequestObject) (gen.TrainerRedeemRehearsalLinkResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) TrainerUploadRehearsal(ctx context.Context, request gen.TrainerUploadRehearsalRequestObject) (gen.TrainerUploadRehearsalResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) ListRehearsals(ctx context.Context, request gen.ListRehearsalsRequestObject) (gen.ListRehearsalsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) IssueRehearsalLink(ctx context.Context, request gen.IssueRehearsalLinkRequestObject) (gen.IssueRehearsalLinkResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) GetRehearsal(ctx context.Context, request gen.GetRehearsalRequestObject) (gen.GetRehearsalResponseObject, error) {
	return nil, notImplemented()
}

// --- assignments (этап 07) ---

func (a *api) ListAssignments(ctx context.Context, request gen.ListAssignmentsRequestObject) (gen.ListAssignmentsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) CreateAssignments(ctx context.Context, request gen.CreateAssignmentsRequestObject) (gen.CreateAssignmentsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) ReissueCodes(ctx context.Context, request gen.ReissueCodesRequestObject) (gen.ReissueCodesResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) ExtendAssignment(ctx context.Context, request gen.ExtendAssignmentRequestObject) (gen.ExtendAssignmentResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) CancelAssignment(ctx context.Context, request gen.CancelAssignmentRequestObject) (gen.CancelAssignmentResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) UnblockCode(ctx context.Context, request gen.UnblockCodeRequestObject) (gen.UnblockCodeResponseObject, error) {
	return nil, notImplemented()
}

// --- sessions (этапы 07/08) ---

func (a *api) TrainerEnter(ctx context.Context, request gen.TrainerEnterRequestObject) (gen.TrainerEnterResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) TrainerStartSession(ctx context.Context, request gen.TrainerStartSessionRequestObject) (gen.TrainerStartSessionResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) TrainerGetPrivatePart(ctx context.Context, request gen.TrainerGetPrivatePartRequestObject) (gen.TrainerGetPrivatePartResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) TrainerPostEvents(ctx context.Context, request gen.TrainerPostEventsRequestObject) (gen.TrainerPostEventsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) TrainerFinishSession(ctx context.Context, request gen.TrainerFinishSessionRequestObject) (gen.TrainerFinishSessionResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) TrainerGetJudgeRetryPack(ctx context.Context, request gen.TrainerGetJudgeRetryPackRequestObject) (gen.TrainerGetJudgeRetryPackResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) TrainerPostJudgeAnswer(ctx context.Context, request gen.TrainerPostJudgeAnswerRequestObject) (gen.TrainerPostJudgeAnswerResponseObject, error) {
	return nil, notImplemented()
}

// --- results (этап 09) ---

func (a *api) TrainerListResults(ctx context.Context, request gen.TrainerListResultsRequestObject) (gen.TrainerListResultsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) TrainerGetResult(ctx context.Context, request gen.TrainerGetResultRequestObject) (gen.TrainerGetResultResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) GetReducedSession(ctx context.Context, request gen.GetReducedSessionRequestObject) (gen.GetReducedSessionResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) ListSessions(ctx context.Context, request gen.ListSessionsRequestObject) (gen.ListSessionsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) GetSessionResult(ctx context.Context, request gen.GetSessionResultRequestObject) (gen.GetSessionResultResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) GetSessionRecord(ctx context.Context, request gen.GetSessionRecordRequestObject) (gen.GetSessionRecordResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) ReviewSession(ctx context.Context, request gen.ReviewSessionRequestObject) (gen.ReviewSessionResponseObject, error) {
	return nil, notImplemented()
}

// --- analytics (этап 09) ---

func (a *api) GetScenarioHealth(ctx context.Context, request gen.GetScenarioHealthRequestObject) (gen.GetScenarioHealthResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) ListHealthSessions(ctx context.Context, request gen.ListHealthSessionsRequestObject) (gen.ListHealthSessionsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) GetPersonProgress(ctx context.Context, request gen.GetPersonProgressRequestObject) (gen.GetPersonProgressResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) CompareEmployees(ctx context.Context, request gen.CompareEmployeesRequestObject) (gen.CompareEmployeesResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) GetGroupAnalytics(ctx context.Context, request gen.GetGroupAnalyticsRequestObject) (gen.GetGroupAnalyticsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) ListCriterionSessions(ctx context.Context, request gen.ListCriterionSessionsRequestObject) (gen.ListCriterionSessionsResponseObject, error) {
	return nil, notImplemented()
}

// --- exports (этап 10) ---

func (a *api) CreateExport(ctx context.Context, request gen.CreateExportRequestObject) (gen.CreateExportResponseObject, error) {
	return nil, notImplemented()
}

// --- decisions (этап 10) ---

func (a *api) ListDecisionCandidates(ctx context.Context, request gen.ListDecisionCandidatesRequestObject) (gen.ListDecisionCandidatesResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) CreateDecision(ctx context.Context, request gen.CreateDecisionRequestObject) (gen.CreateDecisionResponseObject, error) {
	return nil, notImplemented()
}

// --- objections (этап 10) ---

func (a *api) TrainerCreateObjection(ctx context.Context, request gen.TrainerCreateObjectionRequestObject) (gen.TrainerCreateObjectionResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) ListObjections(ctx context.Context, request gen.ListObjectionsRequestObject) (gen.ListObjectionsResponseObject, error) {
	return nil, notImplemented()
}

func (a *api) AnswerObjection(ctx context.Context, request gen.AnswerObjectionRequestObject) (gen.AnswerObjectionResponseObject, error) {
	return nil, notImplemented()
}

// --- audit (этап 02) ---

func (a *api) ListAuditLog(ctx context.Context, request gen.ListAuditLogRequestObject) (gen.ListAuditLogResponseObject, error) {
	return a.audit.ListAuditLog(ctx, request)
}

func (a *api) ExportAuditLog(ctx context.Context, request gen.ExportAuditLogRequestObject) (gen.ExportAuditLogResponseObject, error) {
	return a.audit.ExportAuditLog(ctx, request)
}

// --- settings (этап 01 — первая настоящая секция) ---

func (a *api) GetPortalSettings(ctx context.Context, request gen.GetPortalSettingsRequestObject) (gen.GetPortalSettingsResponseObject, error) {
	return a.settings.GetPortalSettings(ctx, request)
}

func (a *api) UpdatePortalSettings(ctx context.Context, request gen.UpdatePortalSettingsRequestObject) (gen.UpdatePortalSettingsResponseObject, error) {
	return a.settings.UpdatePortalSettings(ctx, request)
}

// --- demo (этап 11) ---

func (a *api) TrainerDemoEnter(ctx context.Context, request gen.TrainerDemoEnterRequestObject) (gen.TrainerDemoEnterResponseObject, error) {
	return nil, notImplemented()
}
