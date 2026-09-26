package people

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/crypto"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

// Алфавит номера участника «N-7K3Q9D»: без 0/O и 1/I, которые путают
// на слух и на бумаге. 32 знака — случайный байт делится без перекоса.
const numberAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

const numberAttempts = 5

type service struct {
	pool        *pgxpool.Pool
	store       *store
	audit       audit.Writer
	access      auth.GroupAccess
	assignments AssignmentCanceller
	masterKey   []byte
}

func newService(pool *pgxpool.Pool, auditWriter audit.Writer, access auth.GroupAccess, canceller AssignmentCanceller, masterKey []byte) *service {
	return &service{pool: pool, store: newStore(), audit: auditWriter, access: access, assignments: canceller, masterKey: masterKey}
}

func fieldError(path, message string) *httpx.Error {
	return httpx.NewError(httpx.KindValidationFailed, message).
		WithErrors([]gen.FieldError{{Path: path, Message: message}})
}

func errPersonMissing() *httpx.Error {
	return httpx.NewError(httpx.KindNotFound, "Такого сотрудника нет.")
}

func errGroupMissing() *httpx.Error {
	return httpx.NewError(httpx.KindNotFound, "Такой группы нет.")
}

func errForbiddenGroup() *httpx.Error {
	return httpx.NewError(httpx.KindForbiddenGroup, "У вас нет доступа к этой группе.")
}

func mapError(err error) error {
	switch {
	case errors.Is(err, errGroupNameTaken):
		return fieldError("/name", "Группа с таким названием уже есть.")
	case errors.Is(err, errProfileNotFound):
		return fieldError("/trainer_profile_id", "Такого профиля тренажёра нет.")
	case errors.Is(err, errPersonnelNoTaken):
		return fieldError("/personnel_no", "Сотрудник с таким табельным номером уже заведён.")
	case errors.Is(err, errPersonNeedsNameForm):
		return fieldError("/full_name", "Укажите ФИО или псевдоним сотрудника.")
	case errors.Is(err, errPersonNotFound):
		return errPersonMissing()
	case errors.Is(err, errGroupNotFound):
		return fieldError("/group_id", "Такой группы нет.")
	}
	return err
}

// clean — пустая строка в форме значит «не указано».
func clean(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// requireGroup — доступ пользователя к результатам группы. Нет доступа —
// отказ в журнал (своей транзакцией) и 403, а не пустой ответ (I-3).
func (s *service) requireGroup(ctx context.Context, a actor.Actor, groupID uuid.UUID, subjectID *uuid.UUID, operation string) error {
	ok, err := s.access.HasAccess(ctx, a.UserID, groupID)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return s.denyGroup(ctx, a, groupID, subjectID, operation)
}

// denyGroup — отказ в доступе к группе, когда вызывающий уже знает, что его
// нет (не повторяет HasAccess): пишет отказ в журнал и возвращает 403.
func (s *service) denyGroup(ctx context.Context, a actor.Actor, groupID uuid.UUID, subjectID *uuid.UUID, operation string) error {
	if err := s.access.RecordDenial(ctx, auth.Denial{
		UserID: a.UserID, Action: gen.AuditActionSessionOpened, Operation: operation,
		Reason: auth.DenialReasonGroup, GroupID: &groupID, SubjectID: subjectID,
	}); err != nil {
		return err
	}
	return errForbiddenGroup()
}

func (s *service) accessibleGroups(ctx context.Context, a actor.Actor) ([]uuid.UUID, error) {
	ids, err := s.access.AccessibleGroups(ctx, a.UserID)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return ids, nil
}

// --- группы ---

type groupView struct {
	groupRow
	HasAccess bool
}

func (s *service) ListGroups(ctx context.Context, a actor.Actor, includeArchived bool) ([]groupView, error) {
	groups, err := s.store.listGroups(ctx, s.pool, includeArchived)
	if err != nil {
		return nil, err
	}
	access, err := s.accessibleGroups(ctx, a)
	if err != nil {
		return nil, err
	}
	out := make([]groupView, 0, len(groups))
	for _, g := range groups {
		out = append(out, groupView{groupRow: g, HasAccess: containsID(access, g.ID)})
	}
	return out, nil
}

type GroupFields struct {
	Name             *string
	Department       *string
	TrainerProfileID *uuid.UUID
	Archived         *bool
}

func (s *service) CreateGroup(ctx context.Context, a actor.Actor, f GroupFields) (groupView, error) {
	name := clean(f.Name)
	if name == nil {
		return groupView{}, fieldError("/name", "Укажите название группы.")
	}
	var id uuid.UUID
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = s.createGroupTx(ctx, tx, NewGroup{Name: *name, Department: clean(f.Department)}, f.TrainerProfileID, &a.UserID)
		return err
	})
	if err != nil {
		return groupView{}, mapError(err)
	}
	return s.groupView(ctx, a, id)
}

func (s *service) UpdateGroup(ctx context.Context, a actor.Actor, id uuid.UUID, f GroupFields) (groupView, error) {
	upd := groupUpdate{Name: clean(f.Name), Department: clean(f.Department), TrainerProfileID: f.TrainerProfileID, Archived: f.Archived}
	if f.Name != nil && upd.Name == nil {
		return groupView{}, fieldError("/name", "Название группы не может быть пустым.")
	}
	fields := changedFields(map[string]bool{
		"name": f.Name != nil, "department": f.Department != nil,
		"trainer_profile_id": f.TrainerProfileID != nil, "archived": f.Archived != nil,
	})
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.store.updateGroup(ctx, tx, id, upd); err != nil {
			if errors.Is(err, errGroupNotFound) {
				return errGroupMissing()
			}
			return err
		}
		details := map[string]any{"fields": fields}
		if f.Archived != nil {
			details["archived"] = *f.Archived
		}
		return s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionGroupSaved, Outcome: audit.OutcomeOK, GroupID: &id, Details: details,
		})
	})
	if err != nil {
		return groupView{}, mapError(err)
	}
	return s.groupView(ctx, a, id)
}

func (s *service) groupView(ctx context.Context, a actor.Actor, id uuid.UUID) (groupView, error) {
	g, err := s.store.group(ctx, s.pool, id)
	if errors.Is(err, errGroupNotFound) {
		return groupView{}, errGroupMissing()
	}
	if err != nil {
		return groupView{}, err
	}
	has, err := s.access.HasAccess(ctx, a.UserID, id)
	if err != nil {
		return groupView{}, err
	}
	return groupView{groupRow: g, HasAccess: has}, nil
}

func (s *service) createGroupTx(ctx context.Context, tx pgx.Tx, g NewGroup, profileID *uuid.UUID, actorID *uuid.UUID) (uuid.UUID, error) {
	id, err := s.store.insertGroup(ctx, tx, g, profileID, actorID)
	if err != nil {
		return uuid.Nil, err
	}
	return id, s.audit.Write(ctx, tx, entryBy(actorID, audit.Entry{
		Action: gen.AuditActionGroupSaved, Outcome: audit.OutcomeOK, GroupID: &id,
		Details: map[string]any{"created": true},
	}))
}

// --- сотрудники ---

// ListPeople — администратор видит всех (он ведёт справочник), остальные —
// только группы с доступом; фильтр по группе без доступа — 403.
func (s *service) ListPeople(ctx context.Context, a actor.Actor, groupID *uuid.UUID, query *string, limit, offset int) ([]personRow, int, error) {
	f := personFilter{Query: query, Limit: limit, Offset: offset}
	switch {
	case groupID != nil:
		if !a.IsAdmin() {
			if err := s.requireGroup(ctx, a, *groupID, nil, "ListPeople"); err != nil {
				return nil, 0, err
			}
		}
		f.GroupIDs = []uuid.UUID{*groupID}
	case !a.IsAdmin():
		ids, err := s.accessibleGroups(ctx, a)
		if err != nil {
			return nil, 0, err
		}
		f.GroupIDs = ids
	}
	return s.store.listPeople(ctx, s.pool, f)
}

type PersonFields struct {
	GroupID     *uuid.UUID
	FullName    *string
	Pseudonym   *string
	PersonnelNo *string
	JobTitle    *string
}

func (s *service) CreatePerson(ctx context.Context, a actor.Actor, f PersonFields) (personRow, error) {
	if f.GroupID == nil {
		return personRow{}, fieldError("/group_id", "Укажите группу сотрудника.")
	}
	p := NewPerson{GroupID: *f.GroupID, FullName: clean(f.FullName), Pseudonym: clean(f.Pseudonym),
		PersonnelNo: clean(f.PersonnelNo), JobTitle: clean(f.JobTitle)}

	var id uuid.UUID
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = s.createPersonTx(ctx, tx, p, &a.UserID)
		return err
	})
	if err != nil {
		return personRow{}, err
	}
	return s.store.person(ctx, s.pool, id, false)
}

func (s *service) createPersonTx(ctx context.Context, tx pgx.Tx, p NewPerson, actorID *uuid.UUID) (uuid.UUID, error) {
	if p.FullName == nil && p.Pseudonym == nil {
		return uuid.Nil, fieldError("/full_name", "Укажите ФИО или псевдоним сотрудника.")
	}
	g, err := s.store.group(ctx, tx, p.GroupID)
	if errors.Is(err, errGroupNotFound) {
		return uuid.Nil, fieldError("/group_id", "Такой группы нет.")
	}
	if err != nil {
		return uuid.Nil, err
	}
	if g.ArchivedAt != nil {
		return uuid.Nil, httpx.NewError(httpx.KindArchived, "Группа в архиве — сотрудника в неё не завести.")
	}

	dataKey, err := crypto.NewDataKey()
	if err != nil {
		return uuid.Nil, fmt.Errorf("ключ данных участника: %w", err)
	}
	wrapped, err := crypto.WrapKey(s.masterKey, dataKey)
	if err != nil {
		return uuid.Nil, fmt.Errorf("обёртка ключа данных: %w", err)
	}

	subjectID, err := s.insertSubject(ctx, tx, wrapped)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.store.insertPerson(ctx, tx, subjectID, p, actorID); err != nil {
		return uuid.Nil, mapError(err)
	}
	return subjectID, s.audit.Write(ctx, tx, entryBy(actorID, audit.Entry{
		Action: gen.AuditActionPersonSaved, Outcome: audit.OutcomeOK,
		SubjectID: &subjectID, GroupID: &p.GroupID, Details: map[string]any{"created": true},
	}))
}

// insertSubject заводит номер участника; совпадение номера (32⁶ вариантов)
// повторяется под точкой сохранения, чтобы не терять всю транзакцию.
func (s *service) insertSubject(ctx context.Context, tx pgx.Tx, wrappedKey []byte) (uuid.UUID, error) {
	for range numberAttempts {
		number, err := newSubjectNumber()
		if err != nil {
			return uuid.Nil, err
		}
		var id uuid.UUID
		err = pg.WithSavepoint(ctx, tx, "subject_number", func(ctx context.Context) error {
			var err error
			id, err = s.store.insertSubject(ctx, tx, number, wrappedKey)
			return err
		})
		if errors.Is(err, errSubjectNumberTaken) {
			continue
		}
		return id, err
	}
	return uuid.Nil, fmt.Errorf("не удалось подобрать свободный номер участника за %d попыток", numberAttempts)
}

func newSubjectNumber() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("генерация номера участника: %w", err)
	}
	out := []byte("N-")
	for _, v := range b {
		out = append(out, numberAlphabet[int(v)%len(numberAlphabet)])
	}
	return string(out), nil
}

type personCard struct {
	personRow
	ResultsVisible bool
}

// PersonCard — данные сотрудника видит администратор и тот, у кого есть
// доступ к его группе; результаты — только по доступу, администратор тоже.
func (s *service) PersonCard(ctx context.Context, a actor.Actor, subjectID uuid.UUID) (personCard, error) {
	p, err := s.store.person(ctx, s.pool, subjectID, false)
	if err != nil {
		return personCard{}, mapError(err)
	}
	has, err := s.access.HasAccess(ctx, a.UserID, p.GroupID)
	if err != nil {
		return personCard{}, err
	}
	if !has && !a.IsAdmin() {
		if err := s.denyGroup(ctx, a, p.GroupID, &subjectID, "GetPersonCard"); err != nil {
			return personCard{}, err
		}
	}
	return personCard{personRow: p, ResultsVisible: has}, nil
}

// UpdatePerson — правка данных или перевод в другую группу. История
// попыток остаётся при номере; доступ к прошлым сессиям — по группе
// назначения, а не по новой группе сотрудника.
func (s *service) UpdatePerson(ctx context.Context, a actor.Actor, subjectID uuid.UUID, f PersonFields) (personRow, error) {
	upd := personUpdate{GroupID: f.GroupID, FullName: clean(f.FullName), Pseudonym: clean(f.Pseudonym),
		PersonnelNo: clean(f.PersonnelNo), JobTitle: clean(f.JobTitle)}
	fields := changedFields(map[string]bool{
		"group_id": upd.GroupID != nil, "full_name": upd.FullName != nil, "pseudonym": upd.Pseudonym != nil,
		"personnel_no": upd.PersonnelNo != nil, "job_title": upd.JobTitle != nil,
	})

	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.store.person(ctx, tx, subjectID, true)
		if err != nil {
			return mapError(err)
		}
		if upd.GroupID != nil && *upd.GroupID != before.GroupID {
			g, err := s.store.group(ctx, tx, *upd.GroupID)
			if errors.Is(err, errGroupNotFound) {
				return fieldError("/group_id", "Такой группы нет.")
			}
			if err != nil {
				return err
			}
			if g.ArchivedAt != nil {
				return httpx.NewError(httpx.KindArchived, "Группа в архиве — сотрудника в неё не перевести.")
			}
		}
		if err := s.store.updatePerson(ctx, tx, subjectID, upd); err != nil {
			return mapError(err)
		}
		groupID := before.GroupID
		details := map[string]any{"fields": fields}
		if upd.GroupID != nil && *upd.GroupID != before.GroupID {
			details["from_group_id"] = before.GroupID.String()
			groupID = *upd.GroupID
		}
		return s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
			Action: gen.AuditActionPersonSaved, Outcome: audit.OutcomeOK,
			SubjectID: &subjectID, GroupID: &groupID, Details: details,
		})
	})
	if err != nil {
		return personRow{}, err
	}
	return s.store.person(ctx, s.pool, subjectID, false)
}

type WithdrawalResult struct {
	Number               string
	Scope                gen.ConsentWithdrawalScope
	CancelledAssignments int
}

// WithdrawConsent — отзыв согласия. Полный отзыв — одна транзакция в
// порядке архитектуры 9.3: отмена назначений и кодов → удаление строки
// «номер → человек» → уничтожение ключа данных → запись в журнал.
// Необратимо. Реквизиты письменного отзыва (document_ref) — свободный
// текст, в нём может оказаться имя, поэтому в журнал он не пишется.
func (s *service) WithdrawConsent(ctx context.Context, a actor.Actor, subjectID uuid.UUID, scope gen.ConsentWithdrawalScope, confirmName *string) (WithdrawalResult, error) {
	result := WithdrawalResult{Scope: scope}
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		p, err := s.store.person(ctx, tx, subjectID, true)
		if err != nil {
			return mapError(err)
		}
		result.Number = p.Number

		switch scope {
		case gen.ConsentWithdrawalScopeExternalAi:
			if p.ExternalAIWithdrawnAt != nil {
				return httpx.NewError(httpx.KindNothingChanged, "Отзыв согласия на внешнюю нейросеть уже отмечен.")
			}
			if err := s.store.markExternalAIWithdrawn(ctx, tx, subjectID); err != nil {
				return err
			}
			return s.audit.Write(ctx, tx, audit.Entry{
				ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
				Action: gen.AuditActionExternalAiWithdrawn, Outcome: audit.OutcomeOK,
				SubjectID: &subjectID, GroupID: &p.GroupID,
			})

		case gen.ConsentWithdrawalScopeAll:
			expected := p.FullName
			if expected == nil {
				expected = p.Pseudonym
			}
			if confirmName == nil || expected == nil || strings.TrimSpace(*confirmName) != *expected {
				return fieldError("/confirm_name", "Имя для подтверждения не совпадает с карточкой сотрудника.")
			}
			n, err := s.assignments.CancelForSubject(ctx, tx, subjectID)
			if err != nil {
				return err
			}
			result.CancelledAssignments = n
			if err := s.store.deletePerson(ctx, tx, subjectID); err != nil {
				return err
			}
			if err := s.store.destroyKey(ctx, tx, subjectID); err != nil {
				return err
			}
			return s.audit.Write(ctx, tx, audit.Entry{
				ActorKind: audit.ActorUser, ActorUserID: &a.UserID,
				Action: gen.AuditActionConsentWithdrawn, Outcome: audit.OutcomeOK,
				SubjectID: &subjectID, GroupID: &p.GroupID,
				Details: map[string]any{"cancelled_assignments": n},
			})
		}
		return fieldError("/scope", "Неизвестный вид отзыва согласия.")
	})
	if err != nil {
		return WithdrawalResult{}, err
	}
	return result, nil
}

// --- Service ---

func (s *service) DataKey(ctx context.Context, subjectID uuid.UUID) ([]byte, error) {
	wrapped, err := s.store.wrappedKey(ctx, s.pool, subjectID)
	if err != nil {
		return nil, err
	}
	if wrapped == nil {
		return nil, ErrKeyDestroyed
	}
	key, err := crypto.UnwrapKey(s.masterKey, wrapped)
	if err != nil {
		return nil, fmt.Errorf("распаковка ключа данных участника: %w", err)
	}
	return key, nil
}

func (s *service) DisplayName(ctx context.Context, subjectID uuid.UUID) (string, bool, bool, error) {
	p, err := s.store.person(ctx, s.pool, subjectID, false)
	if errors.Is(err, errPersonNotFound) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	if p.FullName != nil {
		return *p.FullName, false, true, nil
	}
	return *p.Pseudonym, true, true, nil
}

func (s *service) Person(ctx context.Context, subjectID uuid.UUID) (PersonFacts, error) {
	p, err := s.store.person(ctx, s.pool, subjectID, false)
	if errors.Is(err, errPersonNotFound) {
		wrapped, keyErr := s.store.wrappedKey(ctx, s.pool, subjectID)
		if keyErr != nil {
			return PersonFacts{}, keyErr
		}
		if wrapped == nil {
			return PersonFacts{}, ErrKeyDestroyed
		}
		return PersonFacts{}, ErrSubjectNotFound
	}
	if err != nil {
		return PersonFacts{}, err
	}
	return PersonFacts{
		GroupID: p.GroupID, FullName: p.FullName, Pseudonym: p.Pseudonym,
		PersonnelNo: p.PersonnelNo, ExternalAIWithdrawn: p.ExternalAIWithdrawnAt != nil,
	}, nil
}

// --- Provisioner ---

func (s *service) PersonRefs(ctx context.Context, subjectIDs []uuid.UUID) (map[uuid.UUID]PersonRef, error) {
	return s.store.personRefs(ctx, s.pool, subjectIDs)
}

func (s *service) GroupProfile(ctx context.Context, groupID uuid.UUID) (*uuid.UUID, error) {
	g, err := s.store.group(ctx, s.pool, groupID)
	if err != nil {
		return nil, mapError(err)
	}
	return g.TrainerProfileID, nil
}

func (s *service) CreateGroupTx(ctx context.Context, tx pgx.Tx, g NewGroup, actorID *uuid.UUID) (uuid.UUID, error) {
	id, err := s.createGroupTx(ctx, tx, g, nil, actorID)
	return id, mapError(err)
}

func (s *service) CreatePersonTx(ctx context.Context, tx pgx.Tx, p NewPerson, actorID *uuid.UUID) (uuid.UUID, error) {
	return s.createPersonTx(ctx, tx, p, actorID)
}

// --- Directory ---

func (s *service) GroupNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return s.store.groupNames(ctx, s.pool, ids)
}

func (s *service) SubjectNumbers(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return s.store.subjectNumbers(ctx, s.pool, ids)
}

func (s *service) SubjectIDByNumber(ctx context.Context, number string) (uuid.UUID, bool, error) {
	return s.store.subjectIDByNumber(ctx, s.pool, number)
}

// --- мелочи ---

func entryBy(actorID *uuid.UUID, e audit.Entry) audit.Entry {
	e.ActorKind = audit.ActorSystem
	if actorID != nil {
		e.ActorKind, e.ActorUserID = audit.ActorUser, actorID
	}
	return e
}

func changedFields(set map[string]bool) []string {
	out := []string{}
	for _, name := range []string{"name", "department", "trainer_profile_id", "archived",
		"group_id", "full_name", "pseudonym", "personnel_no", "job_title"} {
		if set[name] {
			out = append(out, name)
		}
	}
	return out
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

var (
	_ Service     = (*service)(nil)
	_ Provisioner = (*service)(nil)
	_ Directory   = (*service)(nil)
)
