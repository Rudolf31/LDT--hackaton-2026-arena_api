package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
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
