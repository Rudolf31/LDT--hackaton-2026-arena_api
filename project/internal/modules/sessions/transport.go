package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/httpx"
)

// Transport — адреса входа и старта клиента-тренажёра; cmd/portal/api.go
// вызывает их по имени (D-17).
type Transport struct {
	service *service
}

func (t *Transport) TrainerEnter(ctx context.Context, request gen.TrainerEnterRequestObject) (gen.TrainerEnterResponseObject, error) {
	res, err := t.service.Enter(ctx, request.Body.Code)
	if err != nil {
		return nil, err
	}
	return enterResponse{body: toEnterBody(res)}, nil
}

func (t *Transport) TrainerStartSession(ctx context.Context, request gen.TrainerStartSessionRequestObject) (gen.TrainerStartSessionResponseObject, error) {
	res, err := t.service.Start(ctx, request.Body.ConsentIds)
	if err != nil {
		return nil, err
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	return startResponse{status: status, body: toStartBody(res)}, nil
}

func (t *Transport) TrainerGetPrivatePart(ctx context.Context, request gen.TrainerGetPrivatePartRequestObject) (gen.TrainerGetPrivatePartResponseObject, error) {
	res, err := t.service.Private(ctx, request.SessionId)
	if err != nil {
		return nil, err
	}
	return startResponse{status: http.StatusOK, body: toStartBody(res)}, nil
}

// Ответы входа, старта и повторной выдачи — свои типы (D-50): поля
// контракта заполнены частями документа по разделу 16, а рядом лежит
// document — весь документ с применённым уровнем (решение 12.3: деления
// документа нет). Формы частей берутся из документа как есть, поэтому
// сгенерированные ScenarioPublicPart и ScenarioPrivatePart, отставшие от
// формата (end_stages — объект, а не список), здесь не подходят.

type enterBody struct {
	Token            string                `json:"token"`
	TokenExpiresAt   time.Time             `json:"token_expires_at"`
	IsDemo           bool                  `json:"is_demo"`
	Participant      participantBody       `json:"participant"`
	Assignment       *assignmentBody       `json:"assignment"`
	Next             gen.EnterResponseNext `json:"next"`
	RunningSessionID *uuid.UUID            `json:"running_session_id"`
	HasResults       bool                  `json:"has_results"`
	CanStartNew      bool                  `json:"can_start_new"`
	Settings         settingsBody          `json:"settings"`
}

type participantBody struct {
	Number      string `json:"number"`
	DisplayName string `json:"display_name"`
}

type assignmentBody struct {
	ID         uuid.UUID      `json:"id"`
	Mode       gen.Mode       `json:"mode"`
	Difficulty gen.Difficulty `json:"difficulty"`
	DueAt      time.Time      `json:"due_at"`
}

type settingsBody struct {
	Format        string                      `json:"format"`
	EngineVersion string                      `json:"engine_version"`
	Scenario      json.RawMessage             `json:"scenario"`
	Document      json.RawMessage             `json:"document"`
	Profile       gen.TrainerProfileForClient `json:"profile"`
	ConsentScreen gen.ConsentScreen           `json:"consent_screen"`
}

type startBody struct {
	Session     sessionBody                 `json:"session"`
	PrivatePart json.RawMessage             `json:"private_part"`
	Document    json.RawMessage             `json:"document"`
	ModelAccess gen.ModelAccess             `json:"model_access"`
	Profile     gen.TrainerProfileForClient `json:"profile"`
	Restore     gen.RestoreState            `json:"restore"`
}

type sessionBody struct {
	ID                uuid.UUID         `json:"id"`
	Status            gen.SessionStatus `json:"status"`
	StartedAt         time.Time         `json:"started_at"`
	Mode              gen.Mode          `json:"mode"`
	Difficulty        gen.Difficulty    `json:"difficulty"`
	IsDemo            bool              `json:"is_demo"`
	ProfileRevision   int               `json:"profile_revision"`
	CriteriaSet       string            `json:"criteria_set"`
	ExternalAIAllowed bool              `json:"external_ai_allowed"`
}

type enterResponse struct {
	body enterBody
}

func (r enterResponse) VisitTrainerEnterResponse(w http.ResponseWriter) error {
	return writeJSON(w, http.StatusOK, r.body)
}

type startResponse struct {
	status int
	body   startBody
}

func (r startResponse) VisitTrainerStartSessionResponse(w http.ResponseWriter) error {
	return writeJSON(w, r.status, r.body)
}

func (r startResponse) VisitTrainerGetPrivatePartResponse(w http.ResponseWriter) error {
	return writeJSON(w, r.status, r.body)
}

func writeJSON(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}

func toEnterBody(r EnterResult) enterBody {
	return enterBody{
		Token: r.Token, TokenExpiresAt: r.TokenExpiresAt,
		Participant: participantBody{Number: r.Number, DisplayName: r.DisplayName},
		Assignment:  &assignmentBody{ID: r.AssignmentID, Mode: r.Mode, Difficulty: r.Difficulty, DueAt: r.DueAt},
		Next:        r.Next, RunningSessionID: r.RunningSessionID, HasResults: r.HasResults, CanStartNew: r.CanStartNew,
		Settings: settingsBody{
			Format: r.Format, EngineVersion: r.EngineVersion, Scenario: r.Public, Document: r.Document,
			Profile: toProfile(r.Profile), ConsentScreen: toScreen(r.Screen),
		},
	}
}

func toStartBody(r StartResult) startBody {
	s := r.Session
	return startBody{
		Session: sessionBody{
			ID: s.ID, Status: gen.SessionStatus(s.Status), StartedAt: s.StartedAt, Mode: gen.Mode(s.Mode),
			Difficulty: gen.Difficulty(s.Difficulty), IsDemo: s.IsDemo, ProfileRevision: s.ProfileRevision,
			CriteriaSet: s.CriteriaSet, ExternalAIAllowed: s.ExternalAIAllowed,
		},
		PrivatePart: r.Private, Document: r.Document,
		ModelAccess: gen.ModelAccess{OpenrouterKey: r.OpenRouterKey, ModelServerToken: r.ModelServerToken},
		Profile:     toProfile(r.Profile),
		// Ходы идущей сессии для продолжения после F5 — этап 08.
		Restore: gen.RestoreState{Turns: []gen.TurnEvent{}, ElapsedMs: 0},
	}
}

func toProfile(p ProfileSnapshot) gen.TrainerProfileForClient {
	return gen.TrainerProfileForClient{Id: p.ProfileID, Revision: p.Revision, Settings: p.Settings}
}

func toScreen(s ConsentScreen) gen.ConsentScreen {
	out := gen.ConsentScreen{
		Variant: s.Variant, CanProceed: s.CanProceed, BlockedReason: s.BlockedReason,
		Texts: make([]gen.ConsentTextShown, 0, len(s.Texts)),
	}
	for _, t := range s.Texts {
		title := t.Title
		out.Texts = append(out.Texts, gen.ConsentTextShown{
			Kind: t.Kind, TextId: t.TextID, Version: t.Version, Title: &title,
			Body: t.Body, Sha256: t.SHA256, Answers: t.Answers,
		})
	}
	return out
}

// --- разговор (этап 08) ---

// rawBody — тело как пришло (D-43): сгенерированные типы не отличают
// null от отсутствия поля и теряют объекты движка. Без middleware тела
// (в тестах службы) — то же тело, собранное заново из типа.
func rawBody(ctx context.Context, typed any) ([]byte, error) {
	if raw, ok := httpx.RawBody(ctx); ok {
		return raw, nil
	}
	return json.Marshal(typed)
}

func (t *Transport) TrainerPostEvents(ctx context.Context, request gen.TrainerPostEventsRequestObject) (gen.TrainerPostEventsResponseObject, error) {
	raw, err := rawBody(ctx, request.Body)
	if err != nil {
		return nil, err
	}
	res, err := t.service.PostEvents(ctx, request.SessionId, raw)
	if err != nil {
		return nil, err
	}
	return eventsResponse{body: eventsBody{
		Accepted: res.Accepted, Duplicates: res.Duplicates, ContiguousSeq: res.ContiguousSeq,
		MissingSeqs: res.MissingSeqs, SessionStatus: gen.SessionStatus(res.Status), Reopened: res.Reopened, Note: res.Note,
	}}, nil
}

func (t *Transport) TrainerFinishSession(ctx context.Context, request gen.TrainerFinishSessionRequestObject) (gen.TrainerFinishSessionResponseObject, error) {
	raw, err := rawBody(ctx, request.Body)
	if err != nil {
		return nil, err
	}
	res, err := t.service.Finish(ctx, request.SessionId, raw)
	if err != nil {
		return nil, err
	}
	return resultResponse{body: toResultBody(res)}, nil
}

func (t *Transport) TrainerGetJudgeRetryPack(ctx context.Context, request gen.TrainerGetJudgeRetryPackRequestObject) (gen.TrainerGetJudgeRetryPackResponseObject, error) {
	res, err := t.service.JudgeRetry(ctx, request.SessionId)
	if err != nil {
		return nil, err
	}
	return judgePackResponse{body: judgePackBody{
		SessionID: res.SessionID, CriteriaSet: res.CriteriaSet, Transcript: toTranscript(res.Transcript),
		JudgeDecisions: res.JudgeDecisions, OfferLog: res.OfferLog, CodeEpisodes: res.CodeEpisodes,
		OpponentInterests: res.Interests, RevealedFacts: res.RevealedFacts, ParticipantBrief: res.Brief,
		ModelAccess: gen.ModelAccess{OpenrouterKey: res.Access.OpenRouterKey, ModelServerToken: res.Access.ModelServerToken},
		Profile:     toProfile(res.Access.Profile),
	}}, nil
}

func (t *Transport) TrainerPostJudgeAnswer(ctx context.Context, request gen.TrainerPostJudgeAnswerRequestObject) (gen.TrainerPostJudgeAnswerResponseObject, error) {
	raw, err := rawBody(ctx, request.Body)
	if err != nil {
		return nil, err
	}
	res, err := t.service.JudgeAnswer(ctx, request.SessionId, raw)
	if err != nil {
		return nil, err
	}
	return resultResponse{body: toResultBody(res)}, nil
}

// Ответы разговора — свои типы, как у старта (D-50): у сгенерированных
// nullable-полей omitempty, и null из ответа пропадал бы.

type eventsBody struct {
	Accepted      int               `json:"accepted"`
	Duplicates    int               `json:"duplicates"`
	ContiguousSeq int               `json:"contiguous_seq"`
	MissingSeqs   []int             `json:"missing_seqs"`
	SessionStatus gen.SessionStatus `json:"session_status"`
	Reopened      bool              `json:"reopened"`
	Note          *string           `json:"note"`
}

type eventsResponse struct{ body eventsBody }

func (r eventsResponse) VisitTrainerPostEventsResponse(w http.ResponseWriter) error {
	return writeJSON(w, http.StatusOK, r.body)
}

type transcriptLine struct {
	Seq     int             `json:"seq"`
	ReplyNo int             `json:"reply_no"`
	Speaker gen.Speaker     `json:"speaker"`
	Text    *string         `json:"text"`
	AtMs    int             `json:"at_ms"`
	Offer   json.RawMessage `json:"offer"`
	Terms   json.RawMessage `json:"terms"`
}

type judgePackBody struct {
	SessionID         uuid.UUID                   `json:"session_id"`
	CriteriaSet       string                      `json:"criteria_set"`
	Transcript        []transcriptLine            `json:"transcript"`
	JudgeDecisions    []json.RawMessage           `json:"judge_decisions"`
	OfferLog          []json.RawMessage           `json:"offer_log"`
	CodeEpisodes      []json.RawMessage           `json:"code_episodes"`
	OpponentInterests json.RawMessage             `json:"opponent_interests"`
	RevealedFacts     []FactText                  `json:"revealed_facts"`
	ParticipantBrief  json.RawMessage             `json:"participant_brief"`
	ModelAccess       gen.ModelAccess             `json:"model_access"`
	Profile           gen.TrainerProfileForClient `json:"profile"`
}

type judgePackResponse struct{ body judgePackBody }

func (r judgePackResponse) VisitTrainerGetJudgeRetryPackResponse(w http.ResponseWriter) error {
	return writeJSON(w, http.StatusOK, r.body)
}

type headerBody struct {
	SessionID        uuid.UUID         `json:"session_id"`
	ScenarioID       uuid.UUID         `json:"scenario_id"`
	ScenarioTitle    string            `json:"scenario_title"`
	VersionNumber    int               `json:"version_number"`
	Fingerprint      string            `json:"fingerprint"`
	Difficulty       gen.Difficulty    `json:"difficulty"`
	Mode             gen.Mode          `json:"mode"`
	StartedAt        time.Time         `json:"started_at"`
	EndedAt          *time.Time        `json:"ended_at"`
	DurationMs       *int              `json:"duration_ms"`
	ParticipantTurns int               `json:"participant_turns"`
	Status           gen.SessionStatus `json:"status"`
	ScoringProfile   *string           `json:"scoring_profile"`
	CriteriaSet      string            `json:"criteria_set"`
	IsDemo           bool              `json:"is_demo"`
}

// scoreColumnsBody — две оценки отдельными полями; общего числа нет
// и не будет (FR-RS-04).
type scoreColumnsBody struct {
	ResultRank    *string         `json:"result_rank"`
	FinalID       *string         `json:"final_id"`
	FinalTitle    *string         `json:"final_title"`
	ResultNumber  *int            `json:"result_number"`
	ResultMax     *int            `json:"result_max"`
	ProcessStatus string          `json:"process_status"`
	ProcessNumber *int            `json:"process_number"`
	CriteriaBands json.RawMessage `json:"criteria_bands"`
	Lucky         bool            `json:"lucky"`
}

type markBody struct {
	Kind    gen.MarkKind `json:"kind"`
	Text    string       `json:"text"`
	Replies []int        `json:"replies,omitempty"`
}

type resultBody struct {
	Header           headerBody        `json:"header"`
	Marks            []markBody        `json:"marks"`
	ScoreColumns     scoreColumnsBody  `json:"score_columns"`
	Issues           []json.RawMessage `json:"issues"`
	HiddenUnrevealed int               `json:"hidden_unrevealed"`
	Epilogue         *string           `json:"epilogue"`
	Scores           json.RawMessage   `json:"scores"`
	Summary          []json.RawMessage `json:"summary"`
	Transcript       []transcriptLine  `json:"transcript"`
	Objections       []json.RawMessage `json:"objections"`
	CanRetryJudge    bool              `json:"can_retry_judge"`
}

type resultResponse struct{ body resultBody }

func (r resultResponse) VisitTrainerFinishSessionResponse(w http.ResponseWriter) error {
	return writeJSON(w, http.StatusOK, r.body)
}

func (r resultResponse) VisitTrainerPostJudgeAnswerResponse(w http.ResponseWriter) error {
	return writeJSON(w, http.StatusOK, r.body)
}

func toResultBody(v ResultView) resultBody {
	s := v.Session
	marks := make([]markBody, 0, len(v.Marks))
	for _, m := range v.Marks {
		marks = append(marks, markBody{Kind: m.Kind, Text: m.Text, Replies: m.Replies})
	}
	return resultBody{
		Header: headerBody{
			SessionID: s.ID, ScenarioID: s.ScenarioID, ScenarioTitle: v.ScenarioTitle, VersionNumber: v.VersionNumber,
			Fingerprint: v.Fingerprint, Difficulty: gen.Difficulty(s.Difficulty), Mode: gen.Mode(s.Mode),
			StartedAt: s.StartedAt, EndedAt: s.EndedAt, DurationMs: durationMs(s.StartedAt, s.EndedAt),
			ParticipantTurns: s.ParticipantTurns, Status: gen.SessionStatus(s.Status), ScoringProfile: s.ScoringProfile,
			CriteriaSet: s.CriteriaSet, IsDemo: s.IsDemo,
		},
		Marks: marks,
		ScoreColumns: scoreColumnsBody{
			ResultRank: s.ResultRank, FinalID: s.FinalID, FinalTitle: s.FinalTitle, ResultNumber: s.ResultNumber,
			ResultMax: s.ResultMax, ProcessStatus: s.ProcessStatus, ProcessNumber: s.ProcessNumber,
			CriteriaBands: rawOrNull(s.CriteriaBands), Lucky: s.Lucky,
		},
		Issues: []json.RawMessage{}, Epilogue: v.Epilogue, Scores: rawOrNull(s.Scores), Summary: v.Summary,
		Transcript: toTranscript(v.Transcript), Objections: []json.RawMessage{}, CanRetryJudge: v.CanRetryJudge,
	}
}

func toTranscript(lines []TranscriptView) []transcriptLine {
	out := make([]transcriptLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, transcriptLine{
			Seq: l.Seq, ReplyNo: l.ReplyNo, Speaker: gen.Speaker(l.Speaker), Text: l.Text, AtMs: l.AtMs,
			Offer: rawOrNull(l.Offer), Terms: rawOrNull(l.Terms),
		})
	}
	return out
}

// rawOrNull — пустой json.RawMessage при кодировании дал бы ошибку; нужен null.
func rawOrNull(raw []byte) json.RawMessage {
	if raw == nil {
		return json.RawMessage("null")
	}
	return raw
}
