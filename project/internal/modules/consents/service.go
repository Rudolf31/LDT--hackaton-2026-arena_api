package consents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/crypto"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

type service struct {
	pool     *pgxpool.Pool
	store    *store
	audit    audit.Writer
	access   auth.GroupAccess
	people   people.Service
	profiles profiles.Service
	now      func() time.Time
}

func newService(pool *pgxpool.Pool, auditWriter audit.Writer, access auth.GroupAccess, staff people.Service, profileService profiles.Service) *service {
	return &service{
		pool: pool, store: newStore(), audit: auditWriter, access: access,
		people: staff, profiles: profileService, now: time.Now,
	}
}

// WrittenInput — реквизиты письменного согласия на оценку (бумага или ЭДО).
type WrittenInput struct {
	DocumentRef     string
	DocumentChannel string
	SignedOn        time.Time
	ValidUntil      *time.Time
}

// moscow — календарь портала: даты документов ставятся по московскому
// времени, в каком бы поясе ни работал сервер. Пояс фиксированный, чтобы
// не зависеть от tzdata в образе.
var moscow = time.FixedZone("MSK", 3*60*60)

// dateOf — календарная дата момента t по Москве, в той же форме, в какой
// приходит дата из тела запроса: полночь UTC.
func dateOf(t time.Time) time.Time {
	y, m, d := t.In(moscow).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func fieldError(path, message string) *httpx.Error {
	return httpx.NewError(httpx.KindValidationFailed, message).
		WithErrors([]gen.FieldError{{Path: path, Message: message}})
}

func errTextChanged() *httpx.Error {
	return httpx.NewError(httpx.KindConsentRequired, "Текст согласия изменился — обновите страницу.")
}

// --- экран согласия ---

func (s *service) TextsFor(ctx context.Context, a Audience) (Screen, error) {
	return s.screen(ctx, s.pool, a)
}

func (s *service) screen(ctx context.Context, q querier, a Audience) (Screen, error) {
	eff, err := s.profiles.Effective(ctx, a.ProfileID)
	if err != nil {
		return Screen{}, fmt.Errorf("профиль тренажёра для экрана согласия: %w", err)
	}
	in := screenInput{
		Mode: a.Mode, Demo: a.Demo,
		ModelProvidersNote: eff.ModelProvidersNote,
	}
	if eff.Routes.OpenRouter {
		in.OpenRouterURL = *eff.Settings.Openrouter.BaseUrl
	}
	if eff.Routes.OwnServer {
		in.OwnServerURL = *eff.Settings.ModelServer.BaseUrl
	}
	if !a.Demo {
		p, err := s.people.Person(ctx, a.SubjectID)
		if err != nil {
			return Screen{}, s.mapPersonError(err)
		}
		in.FullName, in.Pseudonym, in.PersonnelNo = p.FullName, p.Pseudonym, p.PersonnelNo
		in.ExternalAIWithdrawn = p.ExternalAIWithdrawn
		if a.Mode == gen.ModeAssessment {
			_, ok, err := s.store.latestUsable(ctx, q, a.SubjectID, nil, gen.ConsentKindWrittenAssessment)
			if err != nil {
				return Screen{}, err
			}
			in.WrittenConsentOK = ok
		}
	}
	if in.Templates, err = s.store.currentTemplates(ctx, q); err != nil {
		return Screen{}, err
	}
	return buildScreen(in)
}

// mapPersonError — после отзыва согласия связи «номер → человек» нет, и
// назначения участника уже отменены (UC-A-08).
func (s *service) mapPersonError(err error) error {
	switch {
	case errors.Is(err, people.ErrKeyDestroyed):
		return httpx.NewError(httpx.KindAssignmentCancelled, "Согласие отозвано — назначение отменено.")
	case errors.Is(err, people.ErrSubjectNotFound):
		return httpx.NewError(httpx.KindNotFound, "Такого сотрудника нет.")
	}
	return err
}

// Record — ответы экрана одним запросом (UC-P-02). Портал пересобирает
// экран, сверяет присланный SHA-256 со своим текстом и пишет в базу не
// его, а HMAC текста на ключе данных участника: шаблон известен, и
// простой хеш перебором по списку сотрудников выдал бы человека
// (arena-db.md, consent_records). Отказ тоже записывается; повтор — ещё
// одна безвредная запись.
func (s *service) Record(ctx context.Context, a Audience, answers []Answer) (Outcome, error) {
	screen, err := s.screen(ctx, s.pool, a)
	if err != nil {
		return Outcome{}, err
	}
	if !screen.CanProceed {
		return Outcome{}, httpx.NewError(httpx.KindConsentRequired, *screen.BlockedReason)
	}

	byKind := make(map[gen.ConsentKind]TextShown, len(screen.Texts))
	for _, t := range screen.Texts {
		byKind[t.Kind] = t
	}
	seen := map[gen.ConsentKind]bool{}
	for i, ans := range answers {
		path := fmt.Sprintf("/answers/%d", i)
		t, ok := byKind[ans.Kind]
		if !ok {
			return Outcome{}, fieldError(path+"/kind", "Такой текст участнику не показывался.")
		}
		if seen[ans.Kind] {
			return Outcome{}, fieldError(path+"/kind", "На один текст — один ответ.")
		}
		seen[ans.Kind] = true
		if ans.TextID != t.TextID || !strings.EqualFold(ans.ShownSHA256, t.SHA256) {
			return Outcome{}, errTextChanged()
		}
		if !allowed(t.Answers, ans.Answer) {
			return Outcome{}, fieldError(path+"/answer", "Такой ответ на этот текст невозможен.")
		}
	}
	main := MainKind(a.Mode, a.Demo)
	if !seen[main] {
		return Outcome{}, fieldError("/answers", "Нет ответа на основной текст экрана.")
	}

	dataKey, err := s.people.DataKey(ctx, a.SubjectID)
	if err != nil {
		return Outcome{}, s.mapPersonError(err)
	}

	out := Outcome{}
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		for _, ans := range answers {
			t := byKind[ans.Kind]
			rec, err := s.store.insertScreenRecord(ctx, tx, screenRecord{
				SubjectID: a.SubjectID, AssignmentID: a.AssignmentID,
				Kind: ans.Kind, Answer: ans.Answer, TextID: t.TextID,
				ShownHMAC: crypto.HMACSHA256(dataKey, []byte(t.Body)),
			})
			if err != nil {
				return err
			}
			out.Records = append(out.Records, rec)
			id := rec.ID
			switch {
			case ans.Kind == main && ans.Answer != gen.Refused:
				out.MainConsentID = &id
			case ans.Kind == gen.ConsentKindConsentExternalAi && ans.Answer == gen.Granted:
				out.ExternalAIConsentID = &id
				out.ExternalAIAllowed = true
			}
		}
		return nil
	})
	if err != nil {
		return Outcome{}, err
	}

	switch {
	case out.MainConsentID == nil:
		msg := messageAssessmentRefused
		out.Message = &msg
	case !screen.OwnServer && !out.ExternalAIAllowed:
		msg := messageNoModelRoute
		out.Message = &msg
	default:
		out.CanStart = true
	}
	return out, nil
}

func allowed(values []gen.ConsentAnswerValue, v gen.ConsentAnswerValue) bool {
	for _, a := range values {
		if a == v {
			return true
		}
	}
	return false
}

func (s *service) Usable(ctx context.Context, subjectID uuid.UUID, assignmentID *uuid.UUID, kind gen.ConsentKind) (uuid.UUID, bool, error) {
	id, ok, err := s.store.latestUsable(ctx, s.pool, subjectID, assignmentID, kind)
	if err != nil || !ok {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

func (s *service) Records(ctx context.Context, ids []uuid.UUID) ([]RecordInfo, error) {
	return s.store.recordInfos(ctx, s.pool, ids)
}

// --- письменное согласие ---

// RecordWritten — отметка HR о письменном согласии на оценку
// (arena-consent-texts.md 9.1 — рекомендация, документа нет). Доступ — по
// текущей группе сотрудника; нет доступа — 403 и отказ в журнале.
// Реквизиты документа в журнал не идут: это свободный текст, в нём может
// оказаться имя (D-25, D-49).
func (s *service) RecordWritten(ctx context.Context, a actor.Actor, subjectID uuid.UUID, in WrittenInput) (Record, error) {
	p, err := s.people.Person(ctx, subjectID)
	if err != nil {
		if errors.Is(err, people.ErrKeyDestroyed) {
			return Record{}, httpx.NewError(httpx.KindNotFound, "Согласие этого сотрудника отозвано — данных о нём больше нет.")
		}
		return Record{}, s.mapPersonError(err)
	}
	ok, err := s.access.HasAccess(ctx, a.UserID, p.GroupID)
	if err != nil {
		return Record{}, err
	}
	if !ok {
		groupID := p.GroupID
		if err := s.access.RecordDenial(ctx, auth.Denial{
			UserID: a.UserID, Action: gen.AuditActionWrittenConsentRecorded, Operation: "RecordWrittenConsent",
			Reason: auth.DenialReasonGroup, GroupID: &groupID, SubjectID: &subjectID,
		}); err != nil {
			return Record{}, err
		}
		return Record{}, httpx.NewError(httpx.KindForbiddenGroup, "У вас нет доступа к этой группе.")
	}

	ref := strings.TrimSpace(in.DocumentRef)
	if ref == "" {
		return Record{}, fieldError("/document_ref", "Укажите номер и дату документа.")
	}
	if in.SignedOn.After(dateOf(s.now())) {
		return Record{}, fieldError("/signed_on", "Дата подписания не может быть в будущем.")
	}
	if in.ValidUntil != nil && in.ValidUntil.Before(in.SignedOn) {
		return Record{}, fieldError("/valid_until", "Срок действия не может закончиться раньше даты подписания.")
	}

	var rec Record
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		id, err := s.store.insertWritten(ctx, tx, writtenRecord{
			SubjectID: subjectID, DocumentRef: ref, DocumentChannel: in.DocumentChannel,
			SignedOn: in.SignedOn, ValidUntil: in.ValidUntil, RecordedBy: a.UserID,
		})
		if err != nil {
			return err
		}
		groupID := p.GroupID
		if err := s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionWrittenConsentRecorded, Outcome: audit.OutcomeOK,
			SubjectID: &subjectID, GroupID: &groupID,
			Details: map[string]any{"document_channel": in.DocumentChannel, "has_valid_until": in.ValidUntil != nil},
		}); err != nil {
			return err
		}
		rec, err = s.store.record(ctx, tx, id)
		return err
	})
	if err != nil {
		return Record{}, err
	}
	return rec, nil
}

// --- Provisioner ---

func (s *service) EnsureTextsTx(ctx context.Context, tx pgx.Tx, texts []NewText) (int, error) {
	current, err := s.store.currentTemplates(ctx, tx)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, t := range texts {
		if _, ok := current[t.Kind]; ok {
			continue
		}
		if err := s.store.insertText(ctx, tx, t); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

var (
	_ Service     = (*service)(nil)
	_ Provisioner = (*service)(nil)
)
