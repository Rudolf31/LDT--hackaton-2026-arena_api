package assignments

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/modules/audit"
	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
	"arena-portal-backend/internal/modules/scenarios"
	"arena-portal-backend/internal/modules/settings"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/pg"
)

// codeIssueAttempts — сколько раз выпускать код заново при совпадении
// селектора с действующим (архитектура 6.3).
const codeIssueAttempts = 5

const selectorIndex = "access_codes_active_selector"

const reasonNoFullName = "не назначено — нет ФИО"

type service struct {
	pool       *pgxpool.Pool
	store      *store
	audit      audit.Writer
	access     auth.GroupAccess
	people     people.Service
	profiles   profiles.Service
	versions   scenarios.Versions
	settings   settings.Service
	sessions   SessionFacts
	hasher     codeHasher
	trainerURL string
	now        func() time.Time
	newCode    func() (string, error)
}

func errNotFound() *httpx.Error {
	return httpx.NewError(httpx.KindNotFound, "Назначение не найдено.")
}

func errCodeNotFound() *httpx.Error {
	return httpx.NewError(httpx.KindCodeNotFound, "Код не найден — проверьте, правильно ли он введён.")
}

func errForbiddenGroup() *httpx.Error {
	return httpx.NewError(httpx.KindForbiddenGroup, "У вас нет доступа к этой группе.")
}

func fieldError(path, message string) *httpx.Error {
	return httpx.NewError(httpx.KindValidationFailed, message).
		WithErrors([]gen.FieldError{{Path: path, Message: message}})
}

// requireGroup — доступ к группе назначения. Нет доступа — отказ в журнал
// под действием операции (D-59) и 403, а не пустой ответ (I-3).
func (s *service) requireGroup(ctx context.Context, a actor.Actor, groupID uuid.UUID, subjectID *uuid.UUID, action gen.AuditAction, operation string) error {
	ok, err := s.access.HasAccess(ctx, a.UserID, groupID)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if err := s.access.RecordDenial(ctx, auth.Denial{
		UserID: a.UserID, Action: action, Operation: operation,
		Reason: auth.DenialReasonGroup, GroupID: &groupID, SubjectID: subjectID,
	}); err != nil {
		return err
	}
	return errForbiddenGroup()
}

// --- создание ---

// CreateInput — тело POST /assignments.
type CreateInput struct {
	SubjectIDs []uuid.UUID
	VersionID  uuid.UUID
	Difficulty gen.Difficulty
	ProfileID  *uuid.UUID
	DueAt      time.Time
}

// PersonRefView — участник в ответе; псевдоним, чтобы transport.go не
// импортировал people (D-18).
type PersonRefView = people.PersonRef

// Issued — выпущенный код: показывается один раз и больше нигде не живёт.
type Issued struct {
	AssignmentID uuid.UUID
	Person       people.PersonRef
	Code         string
	Link         string
}

// Skipped — сотрудник, которому назначение не выдано, и почему.
type Skipped struct {
	SubjectID    uuid.UUID
	AssignmentID *uuid.UUID
	Reason       string
}

type CreateResult struct {
	BatchID *uuid.UUID
	Created []Issued
	Skipped []Skipped
}

// Create — назначение одному сотруднику или списку (UC-H-01, UC-H-02).
// Частичный успех: в оценке сотрудник без ФИО попадает в skipped
// (FR-AC-05). Каждый код — через точку сохранения: совпадение селектора
// не откатывает пачку (архитектура 6.3).
func (s *service) Create(ctx context.Context, a actor.Actor, in CreateInput) (CreateResult, error) {
	if !in.DueAt.After(s.now()) {
		return CreateResult{}, fieldError("/due_at", "Срок должен быть в будущем.")
	}
	version, err := s.versions.Version(ctx, in.VersionID)
	if errors.Is(err, scenarios.ErrVersionNotFound) {
		return CreateResult{}, httpx.NewError(httpx.KindNotFound, "Такой версии сценария нет.")
	}
	if err != nil {
		return CreateResult{}, err
	}
	if version.ScenarioArchived {
		return CreateResult{}, httpx.NewError(httpx.KindArchived, "Сценарий в архиве — назначить его нельзя.")
	}

	refs, err := s.people.PersonRefs(ctx, in.SubjectIDs)
	if err != nil {
		return CreateResult{}, err
	}
	for i, id := range in.SubjectIDs {
		if r, ok := refs[id]; !ok || !r.Present {
			return CreateResult{}, httpx.NewError(httpx.KindNotFound, "Такого сотрудника нет.").
				WithErrors([]gen.FieldError{{Path: fmt.Sprintf("/subject_ids/%d", i), Message: "Такого сотрудника нет."}})
		}
	}
	checked := map[uuid.UUID]bool{}
	for _, id := range in.SubjectIDs {
		g := refs[id].GroupID
		if checked[g] {
			continue
		}
		if err := s.requireGroup(ctx, a, g, nil, gen.AuditActionAssignmentCreated, "CreateAssignments"); err != nil {
			return CreateResult{}, err
		}
		checked[g] = true
	}

	var explicitProfile *uuid.UUID
	if in.ProfileID != nil {
		eff, err := s.profiles.Effective(ctx, *in.ProfileID)
		if errors.Is(err, profiles.ErrNotFound) {
			return CreateResult{}, fieldError("/trainer_profile_id", "Такого профиля тренажёра нет.")
		}
		if err != nil {
			return CreateResult{}, err
		}
		if eff.Archived {
			return CreateResult{}, httpx.NewError(httpx.KindArchived, "Профиль тренажёра в архиве — выберите другой.")
		}
		explicitProfile = in.ProfileID
	}

	result := CreateResult{Created: []Issued{}, Skipped: []Skipped{}}
	var todo []uuid.UUID
	for _, id := range in.SubjectIDs {
		if version.Mode == gen.ModeAssessment && !refs[id].HasFullName {
			result.Skipped = append(result.Skipped, Skipped{SubjectID: id, Reason: reasonNoFullName})
			continue
		}
		todo = append(todo, id)
	}
	if len(todo) == 0 {
		return CreateResult{}, fieldError("/subject_ids",
			"Никого не назначено: оценка проводится только поимённо, а у выбранных сотрудников нет ФИО.")
	}

	profileByGroup := map[uuid.UUID]uuid.UUID{}
	profileFor := func(groupID uuid.UUID) (uuid.UUID, error) {
		if explicitProfile != nil {
			return *explicitProfile, nil
		}
		if id, ok := profileByGroup[groupID]; ok {
			return id, nil
		}
		id, err := s.profiles.DefaultID(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		own, err := s.people.GroupProfile(ctx, groupID)
		if err != nil {
			return uuid.Nil, err
		}
		if own != nil {
			id = *own
		}
		profileByGroup[groupID] = id
		return id, nil
	}

	if len(in.SubjectIDs) > 1 {
		batch := uuid.New()
		result.BatchID = &batch
	}
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		for _, subjectID := range todo {
			ref := refs[subjectID]
			profileID, err := profileFor(ref.GroupID)
			if err != nil {
				return err
			}
			id, err := s.store.insertAssignment(ctx, tx, newAssignment{
				SubjectID: subjectID, GroupID: ref.GroupID, VersionID: version.ID,
				Difficulty: string(in.Difficulty), ProfileID: profileID, DueAt: in.DueAt,
				BatchID: result.BatchID, CreatedBy: a.UserID,
			})
			if err != nil {
				return err
			}
			code, err := s.issueCode(ctx, tx, id, &a.UserID)
			if err != nil {
				return err
			}
			if err := s.writeEntry(ctx, tx, a, gen.AuditActionAssignmentCreated, subjectID, ref.GroupID, map[string]any{
				"assignment_id": id, "batch_id": result.BatchID, "scenario_version_id": version.ID,
				"difficulty": in.Difficulty, "mode": version.Mode,
			}); err != nil {
				return err
			}
			if err := s.writeEntry(ctx, tx, a, gen.AuditActionCodeIssued, subjectID, ref.GroupID,
				map[string]any{"assignment_id": id}); err != nil {
				return err
			}
			result.Created = append(result.Created, s.issued(id, ref, code))
		}
		return nil
	})
	if err != nil {
		return CreateResult{}, err
	}
	return result, nil
}

func (s *service) issued(id uuid.UUID, ref people.PersonRef, normalized string) Issued {
	code := formatCode(normalized)
	return Issued{AssignmentID: id, Person: ref, Code: code, Link: s.trainerURL + "/t?code=" + code}
}

// issueCode — новый действующий код назначения. Селектор — 20 бит, и при
// тысячах действующих кодов совпадение вероятно: каждая попытка — своя
// точка сохранения, на совпадении код выпускается заново (архитектура 6.3).
func (s *service) issueCode(ctx context.Context, tx pgx.Tx, assignmentID uuid.UUID, issuedBy *uuid.UUID) (string, error) {
	for range codeIssueAttempts {
		code, err := s.newCode()
		if err != nil {
			return "", err
		}
		err = pg.WithSavepoint(ctx, tx, "access_code", func(ctx context.Context) error {
			_, err := s.store.insertCode(ctx, tx, assignmentID, s.hasher.selector(code), s.hasher.full(code), issuedBy)
			return err
		})
		if err == nil {
			return code, nil
		}
		if v, ok := pg.AsViolation(err); ok && v.Kind == pg.Unique &&
			(v.Constraint == selectorIndex || v.Constraint == "access_codes_code_hash_key") {
			continue
		}
		return "", fmt.Errorf("выпуск кода доступа: %w", err)
	}
	return "", fmt.Errorf("выпуск кода доступа: %d попыток подряд совпали с действующими кодами", codeIssueAttempts)
}

func (s *service) writeEntry(ctx context.Context, tx pgx.Tx, a actor.Actor, action gen.AuditAction, subjectID, groupID uuid.UUID, details map[string]any) error {
	return s.audit.Write(ctx, tx, audit.Entry{
		ActorKind: audit.ActorUser, ActorUserID: &a.UserID, Action: action, Outcome: audit.OutcomeOK,
		SubjectID: &subjectID, GroupID: &groupID, Details: details,
	})
}

// --- перевыпуск, продление, отмена, снятие блокировки ---

// Reissue — новый код вместо старого одной транзакцией на каждое
// назначение: старый перестаёт работать немедленно (FR-AC-12), новый — с
// нулём попыток, то есть снимает и блокировку. Отменённые — в skipped.
func (s *service) Reissue(ctx context.Context, a actor.Actor, ids []uuid.UUID) (CreateResult, error) {
	rows := make([]assignmentRow, 0, len(ids))
	for _, id := range ids {
		r, err := s.store.assignment(ctx, s.pool, id, false)
		if errors.Is(err, errAssignmentNotFound) {
			return CreateResult{}, errNotFound()
		}
		if err != nil {
			return CreateResult{}, err
		}
		rows = append(rows, r)
	}
	checked := map[uuid.UUID]bool{}
	for _, r := range rows {
		if checked[r.GroupID] {
			continue
		}
		subjectID := r.SubjectID
		if err := s.requireGroup(ctx, a, r.GroupID, &subjectID, gen.AuditActionCodeReissued, "ReissueCodes"); err != nil {
			return CreateResult{}, err
		}
		checked[r.GroupID] = true
	}
	subjects := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		subjects = append(subjects, r.SubjectID)
	}
	refs, err := s.people.PersonRefs(ctx, subjects)
	if err != nil {
		return CreateResult{}, err
	}

	result := CreateResult{Created: []Issued{}, Skipped: []Skipped{}}
	err = pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		for _, id := range ids {
			r, err := s.store.assignment(ctx, tx, id, true)
			if err != nil {
				return err
			}
			if r.CancelledAt != nil {
				aid := r.ID
				result.Skipped = append(result.Skipped, Skipped{SubjectID: r.SubjectID, AssignmentID: &aid, Reason: "назначение отменено"})
				continue
			}
			if _, err := s.store.revokeActive(ctx, tx, r.ID, "reissued"); err != nil {
				return err
			}
			code, err := s.issueCode(ctx, tx, r.ID, &a.UserID)
			if err != nil {
				return err
			}
			if err := s.writeEntry(ctx, tx, a, gen.AuditActionCodeReissued, r.SubjectID, r.GroupID,
				map[string]any{"assignment_id": r.ID}); err != nil {
				return err
			}
			result.Created = append(result.Created, s.issued(r.ID, refs[r.SubjectID], code))
		}
		return nil
	})
	if err != nil {
		return CreateResult{}, err
	}
	return result, nil
}

// checkedRow — назначение для правки HR с проверкой доступа к его группе
// до транзакции; operation "" — без проверки группы (только роль).
func (s *service) checkedRow(ctx context.Context, a actor.Actor, id uuid.UUID, action gen.AuditAction, operation string) (assignmentRow, error) {
	r, err := s.store.assignment(ctx, s.pool, id, false)
	if errors.Is(err, errAssignmentNotFound) {
		return assignmentRow{}, errNotFound()
	}
	if err != nil {
		return assignmentRow{}, err
	}
	if operation != "" {
		subjectID := r.SubjectID
		if err := s.requireGroup(ctx, a, r.GroupID, &subjectID, action, operation); err != nil {
			return assignmentRow{}, err
		}
	}
	return r, nil
}

// Extend — продление срока (UC-H-03): код прежний.
func (s *service) Extend(ctx context.Context, a actor.Actor, id uuid.UUID, due time.Time) (View, error) {
	if _, err := s.checkedRow(ctx, a, id, gen.AuditActionAssignmentExtended, "ExtendAssignment"); err != nil {
		return View{}, err
	}
	if !due.After(s.now()) {
		return View{}, fieldError("/due_at", "Срок должен быть в будущем.")
	}
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, err := s.store.assignment(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if r.CancelledAt != nil {
			return httpx.NewError(httpx.KindAssignmentCancelled, "Назначение отменено — продлить его нельзя.")
		}
		if err := s.store.setDue(ctx, tx, id, due); err != nil {
			return err
		}
		return s.writeEntry(ctx, tx, a, gen.AuditActionAssignmentExtended, r.SubjectID, r.GroupID,
			map[string]any{"assignment_id": id})
	})
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, id)
}

// Cancel — отмена HR (UC-H-03): код недействителен сразу, новые запуски
// запрещены, идущая сессия доигрывается. Повторная отмена — без изменений
// и без записи в журнал (D-58).
func (s *service) Cancel(ctx context.Context, a actor.Actor, id uuid.UUID) (View, error) {
	if _, err := s.checkedRow(ctx, a, id, gen.AuditActionAssignmentCancelled, "CancelAssignment"); err != nil {
		return View{}, err
	}
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, err := s.store.assignment(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if r.CancelledAt != nil {
			return nil
		}
		if err := s.store.cancel(ctx, tx, id, a.UserID); err != nil {
			return err
		}
		if _, err := s.store.revokeActive(ctx, tx, id, "assignment_cancelled"); err != nil {
			return err
		}
		return s.writeEntry(ctx, tx, a, gen.AuditActionAssignmentCancelled, r.SubjectID, r.GroupID,
			map[string]any{"assignment_id": id})
	})
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, id)
}

// Unblock — снятие блокировки кода администратором (UC-A-05): попытки и
// блокировка обнуляются. Доступ к группе не нужен — только роль.
func (s *service) Unblock(ctx context.Context, a actor.Actor, id uuid.UUID) (View, error) {
	if _, err := s.checkedRow(ctx, a, id, "", ""); err != nil {
		return View{}, err
	}
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, err := s.store.assignment(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if r.CodeID == nil || r.CodeBlockedAt == nil {
			return nil
		}
		if err := s.store.unblock(ctx, tx, *r.CodeID); err != nil {
			return err
		}
		return s.writeEntry(ctx, tx, a, gen.AuditActionCodeUnblocked, r.SubjectID, r.GroupID,
			map[string]any{"assignment_id": id})
	})
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, id)
}

// CancelForSubject — people.AssignmentCanceller: отзыв согласия в
// транзакции people (UC-A-08).
func (s *service) CancelForSubject(ctx context.Context, tx pgx.Tx, subjectID uuid.UUID) (int, error) {
	return s.store.cancelForSubject(ctx, tx, subjectID)
}

// --- список ---

// ListFilter — параметры GET /assignments.
type ListFilter struct {
	ID         *uuid.UUID
	GroupID    *uuid.UUID
	SubjectID  *uuid.UUID
	ScenarioID *uuid.UUID
	Status     *gen.AssignmentStatus
	BatchID    *uuid.UUID
	DueBefore  *time.Time
	Limit      int
	Offset     int
}

// View — назначение для ответа: строка базы, участник, версия, профиль,
// сводка сессий и выведенный статус.
type View struct {
	Row         assignmentRow
	Person      people.PersonRef
	Version     scenarios.VersionBrief
	ProfileName *string
	Sessions    SessionSummary
	Status      gen.AssignmentStatus
}

// List — назначения со статусом (UC-H-03). Фильтр по группе, назначению
// или сотруднику без доступа — 403, а не пустой список (FR-AC-08).
func (s *service) List(ctx context.Context, a actor.Actor, f ListFilter) ([]View, int, error) {
	groups, err := s.access.AccessibleGroups(ctx, a.UserID)
	if err != nil {
		return nil, 0, err
	}
	if groups == nil {
		groups = []uuid.UUID{}
	}
	deny := func(groupID uuid.UUID, subjectID *uuid.UUID) error {
		if err := s.access.RecordDenial(ctx, auth.Denial{
			UserID: a.UserID, Action: gen.AuditActionSessionOpened, Operation: "ListAssignments",
			Reason: auth.DenialReasonGroup, GroupID: &groupID, SubjectID: subjectID,
		}); err != nil {
			return err
		}
		return errForbiddenGroup()
	}
	if f.GroupID != nil {
		if !slices.Contains(groups, *f.GroupID) {
			return nil, 0, deny(*f.GroupID, nil)
		}
		groups = []uuid.UUID{*f.GroupID}
	}
	if f.ID != nil {
		r, err := s.store.assignment(ctx, s.pool, *f.ID, false)
		if err != nil && !errors.Is(err, errAssignmentNotFound) {
			return nil, 0, err
		}
		if err == nil && !slices.Contains(groups, r.GroupID) {
			subjectID := r.SubjectID
			return nil, 0, deny(r.GroupID, &subjectID)
		}
	}
	if f.SubjectID != nil {
		refs, err := s.people.PersonRefs(ctx, []uuid.UUID{*f.SubjectID})
		if err != nil {
			return nil, 0, err
		}
		if r, ok := refs[*f.SubjectID]; ok && r.Present && !slices.Contains(groups, r.GroupID) {
			return nil, 0, deny(r.GroupID, f.SubjectID)
		}
	}

	lf := listFilter{GroupIDs: groups, ID: f.ID, SubjectID: f.SubjectID, BatchID: f.BatchID, DueBefore: f.DueBefore,
		Limit: f.Limit, Offset: f.Offset}
	if f.ScenarioID != nil {
		if lf.VersionIDs, err = s.versions.VersionIDs(ctx, *f.ScenarioID); err != nil {
			return nil, 0, err
		}
		if lf.VersionIDs == nil {
			lf.VersionIDs = []uuid.UUID{}
		}
	}
	if f.Status != nil {
		lf.Limit, lf.Offset = 0, 0
	}
	rows, total, err := s.store.list(ctx, s.pool, lf)
	if err != nil {
		return nil, 0, err
	}
	views, err := s.views(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	if f.Status == nil {
		return views, total, nil
	}
	var matched []View
	for _, v := range views {
		if v.Status == *f.Status {
			matched = append(matched, v)
		}
	}
	total = len(matched)
	start := min(f.Offset, total)
	end := min(start+f.Limit, total)
	return matched[start:end], total, nil
}

func (s *service) view(ctx context.Context, id uuid.UUID) (View, error) {
	r, err := s.store.assignment(ctx, s.pool, id, false)
	if err != nil {
		return View{}, err
	}
	views, err := s.views(ctx, []assignmentRow{r})
	if err != nil {
		return View{}, err
	}
	return views[0], nil
}

// views — строки базы в ответ: соседние модули спрашиваются пачкой, по
// одному запросу на пачку, а не на строку.
func (s *service) views(ctx context.Context, rows []assignmentRow) ([]View, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	subjects := make([]uuid.UUID, 0, len(rows))
	versions := make([]uuid.UUID, 0, len(rows))
	profileIDs := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
		subjects = append(subjects, r.SubjectID)
		versions = append(versions, r.VersionID)
		profileIDs = append(profileIDs, r.ProfileID)
	}
	refs, err := s.people.PersonRefs(ctx, subjects)
	if err != nil {
		return nil, err
	}
	briefs, err := s.versions.Briefs(ctx, versions)
	if err != nil {
		return nil, err
	}
	names, err := s.profiles.Names(ctx, profileIDs)
	if err != nil {
		return nil, err
	}
	summaries, err := s.sessions.Summaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]View, 0, len(rows))
	for _, r := range rows {
		v := View{Row: r, Person: refs[r.SubjectID], Version: briefs[r.VersionID], Sessions: summaries[r.ID]}
		if name, ok := names[r.ProfileID]; ok {
			v.ProfileName = &name
		}
		v.Status = deriveStatus(statusFacts{
			Cancelled: r.CancelledAt != nil, CodeBlocked: r.CodeBlockedAt != nil,
			DueAt: r.DueAt, Sessions: v.Sessions,
		}, now)
		out = append(out, v)
	}
	return out, nil
}

// --- вход по коду (Entry) ---

// redeemOutcome — итог ввода кода, известный внутри транзакции. Ошибку
// из него делает вызывающий уже после коммита: неудачная попытка и
// блокировка должны сохраниться, хотя ответ — отказ (NFR-S-02).
type redeemOutcome struct {
	state  State
	codeID uuid.UUID
	fail   *httpx.Error
}

func (s *service) Redeem(ctx context.Context, raw string) (Redeemed, error) {
	code, ok := normalizeCode(raw)
	if !ok {
		// Опечатка, а не перебор: подбирающий пришлёт верный контрольный
		// символ, поэтому попытку не засчитываем (D-54).
		return Redeemed{}, errCodeNotFound()
	}
	var out redeemOutcome
	err := pg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.redeemTx(ctx, tx, code)
		return err
	})
	if err != nil {
		return Redeemed{}, err
	}
	if out.fail != nil {
		return Redeemed{}, out.fail
	}
	if !s.now().Before(out.state.DueAt) {
		// Просроченное назначение пускает только в идущую сессию: она
		// доигрывается (D-58).
		summaries, err := s.sessions.Summaries(ctx, []uuid.UUID{out.state.AssignmentID})
		if err != nil {
			return Redeemed{}, err
		}
		if summaries[out.state.AssignmentID].RunningID == nil {
			return Redeemed{}, errExpired()
		}
	}
	return Redeemed{State: out.state, CodeID: out.codeID}, nil
}

func (s *service) redeemTx(ctx context.Context, tx pgx.Tx, code string) (redeemOutcome, error) {
	c, found, err := s.store.codeByHash(ctx, tx, s.hasher.full(code))
	if err != nil {
		return redeemOutcome{}, err
	}
	if !found {
		return s.failedAttempt(ctx, tx, code)
	}
	if c.RevokedAt != nil {
		if c.RevokeReason != nil && *c.RevokeReason == "reissued" {
			return redeemOutcome{fail: errCodeRevoked()}, nil
		}
		return redeemOutcome{fail: errCancelled()}, nil
	}
	if c.BlockedAt != nil {
		return redeemOutcome{fail: errCodeBlocked()}, nil
	}
	r, err := s.store.assignment(ctx, tx, c.AssignmentID, false)
	if err != nil {
		return redeemOutcome{}, err
	}
	if r.CancelledAt != nil {
		return redeemOutcome{fail: errCancelled()}, nil
	}
	return redeemOutcome{state: stateOf(r), codeID: c.ID}, nil
}

// failedAttempt — код не совпал: если селектор попал в чей-то действующий
// код, попытка засчитывается ему, а на пороге из настроек код блокируется
// насовсем (архитектура 9.5).
func (s *service) failedAttempt(ctx context.Context, tx pgx.Tx, code string) (redeemOutcome, error) {
	c, found, err := s.store.activeCodeBySelector(ctx, tx, s.hasher.selector(code))
	if err != nil {
		return redeemOutcome{}, err
	}
	if !found {
		return redeemOutcome{fail: errCodeNotFound()}, nil
	}
	cfg, err := s.settings.Get(ctx)
	if err != nil {
		return redeemOutcome{}, err
	}
	block := c.BlockedAt == nil && c.FailedAttempts+1 >= cfg.CodeMaxFailedAttempts
	if err := s.store.failAttempt(ctx, tx, c.ID, block); err != nil {
		return redeemOutcome{}, err
	}
	if block {
		r, err := s.store.assignment(ctx, tx, c.AssignmentID, false)
		if err != nil {
			return redeemOutcome{}, err
		}
		if err := s.audit.Write(ctx, tx, audit.Entry{
			ActorKind: audit.ActorSystem, Action: gen.AuditActionCodeBlocked, Outcome: audit.OutcomeOK,
			SubjectID: &r.SubjectID, GroupID: &r.GroupID,
			Details: map[string]any{"assignment_id": r.ID, "failed_attempts": c.FailedAttempts + 1},
		}); err != nil {
			return redeemOutcome{}, err
		}
	}
	return redeemOutcome{fail: errCodeNotFound()}, nil
}

func (s *service) ForTrainer(ctx context.Context, assignmentID uuid.UUID) (State, error) {
	r, err := s.store.assignment(ctx, s.pool, assignmentID, false)
	if errors.Is(err, errAssignmentNotFound) {
		return State{}, errNotFound()
	}
	if err != nil {
		return State{}, err
	}
	return stateOf(r), nil
}

func stateOf(r assignmentRow) State {
	return State{
		AssignmentID: r.ID, SubjectID: r.SubjectID, GroupID: r.GroupID, VersionID: r.VersionID,
		ProfileID: r.ProfileID, Difficulty: gen.Difficulty(r.Difficulty), DueAt: r.DueAt,
		CancelledAt: r.CancelledAt, ActiveCodeID: r.CodeID, CodeBlocked: r.CodeBlockedAt != nil,
	}
}

var (
	_ Entry                      = (*service)(nil)
	_ people.AssignmentCanceller = (*service)(nil)
)
