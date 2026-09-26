// Package scenarios — библиотека сценариев: создание из брифа, шаблона,
// копией или вручную, черновик с проверкой, публикация неизменяемых версий,
// импорт и экспорт, архив (этап 04, arena-portal-hr.md 6.1, 10.5, 11.7;
// arena-portal-backend-architecture.md 6.5, 10.1, 12.5). Формат документа,
// его проверка, отпечаток, шаблоны и каркас — в scenariodoc; этот модуль
// хранит документ в базе и решает правила версий и библиотеки.
package scenarios

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
)

// ErrVersionNotFound — такой версии сценария нет.
var ErrVersionNotFound = errors.New("версия сценария не найдена")

// VersionInfo — опубликованная версия целиком, контракт наружу для
// назначений (этап 07) и сессий (этап 08): им нужен документ версии и её
// неизменяемые поля, а не HTTP-представление.
type VersionInfo struct {
	ID              uuid.UUID
	ScenarioID      uuid.UUID
	Number          int
	Mode            gen.Mode
	Fingerprint     string
	Format          string
	EngineVersion   string
	Title           string
	Sphere          gen.Sphere
	NegotiationType gen.NegotiationType
	Document        json.RawMessage
	// ScenarioArchived — сценарий версии в архиве: новых назначений на
	// него не делают (arena-api.yaml, createAssignments: «Архивный
	// сценарий — 409»).
	ScenarioArchived bool
}

// VersionBrief — версия без документа, для списка назначений: номер,
// режим, название и есть ли у сценария версия новее (FR-AC-01: назначение
// на новую версию не переезжает, экран только показывает, что она есть).
type VersionBrief struct {
	ID          uuid.UUID
	ScenarioID  uuid.UUID
	Number      int
	Mode        gen.Mode
	Title       string
	NewerExists bool
}

// Versions — то, что другие модули видят о версиях сценария (CLAUDE.md,
// «Устройство модуля»): контракт scenarios.Version(id) для назначений и
// сессий (04-scenarios.md, задача «Версии»).
type Versions interface {
	Version(ctx context.Context, id uuid.UUID) (VersionInfo, error)
	// Briefs — версии по номерам без документа; неизвестные номера в
	// карте отсутствуют.
	Briefs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]VersionBrief, error)
	// VersionIDs — все опубликованные версии сценария, для фильтра
	// списка назначений по сценарию.
	VersionIDs(ctx context.Context, scenarioID uuid.UUID) ([]uuid.UUID, error)
}

// RehearsalCounter — сколько прошедших репетиций у сценария есть ровно на
// данном отпечатке черновика (FR-SC-11). Допуск к режиму «оценка» по этому
// числу снят (архитектура 12.5) — счётчик остаётся только справочным полем
// экрана сценария (`Admission.Done`) и не блокирует публикацию. Реализует
// модуль rehearsals (этап 10); до него — заглушка, отвечающая 0
// (cmd/portal/stubs.go, D-06).
type RehearsalCounter interface {
	Count(ctx context.Context, scenarioID uuid.UUID, fingerprint string) (int, error)
}

// VersionSessionCounter — число сессий на каждой опубликованной версии, для
// `VersionSummary.sessions_count` экрана сценария. Реализует sessions
// (этапы 07–08); до них — заглушка с пустой картой (cmd/portal/stubs.go).
type VersionSessionCounter interface {
	Counts(ctx context.Context, versionIDs []uuid.UUID) (map[uuid.UUID]int, error)
}

// Authoring — контракт для модуля generation (этап 05, D-05): хранит и
// решает scenarios (владелец таблицы scenarios.generation, CLAUDE.md,
// «Устройство модуля»), формат содержимого состояния (State) решает сам
// generation — здесь оно проходит как непрозрачный json.RawMessage.
type Authoring interface {
	// GenerationState — снимок для GET /generation и для самого задания:
	// состояние как есть (nil, если сценарий никогда не входил в
	// авторство), текущий черновик (nil, если его ещё нет — нужен как base
	// для правки), режим и архивность сценария. 404, если сценария нет.
	GenerationState(ctx context.Context, id uuid.UUID) (GenerationSnapshot, error)

	// BeginGeneration — старт задания: в транзакции (SELECT … FOR UPDATE)
	// проверяет, что сценарий не в архиве, что не идёт другое задание и,
	// для правки (isEdit=true — voice-edit/text-edit), что черновик уже
	// есть, и одним UPDATE записывает state со статусом running. На любой
	// из трёх отказов и на отсутствие сценария — готовая *httpx.Error
	// (409/409/409/404), которую можно возвращать клиенту как есть.
	BeginGeneration(ctx context.Context, id uuid.UUID, isEdit bool, state json.RawMessage) error

	// UpdateGeneration — смена этапа/попытки во время идущего задания:
	// задание — единственный писатель generation, пока оно running,
	// поэтому проверок BeginGeneration здесь нет, только запись.
	UpdateGeneration(ctx context.Context, id uuid.UUID, state json.RawMessage) error

	// FinishGeneration — конец задания. document != nil (успех): документ
	// проверяется (scenariodoc.Validate) и сохраняется в черновик тем же
	// путём, что SaveDraft (draft_check, отпечаток, draft_updated_*), и
	// generation — одной транзакцией; passport.mode результата в этот
	// путь не входит — его приводит к scenarios.mode сам generation до
	// вызова. document == nil (провал): меняется только generation,
	// черновик остаётся как был.
	FinishGeneration(ctx context.Context, id uuid.UUID, state json.RawMessage, document json.RawMessage, actorID uuid.UUID) error

	// FailRunningGenerations — при старте портала переводит все задания
	// со статусом running в failed с переданным сообщением (arena-api.yaml,
	// getGeneration: «портал закрывает зависшие задания»). Возвращает
	// число закрытых заданий — для строки в логе при старте.
	FailRunningGenerations(ctx context.Context, message string) (int, error)
}

// GenerationSnapshot — то, что нужно за пределами scenarios, чтобы решить
// судьбу задания и собрать ответ GET /generation (решения этапа 05, D-31).
type GenerationSnapshot struct {
	// State — scenarios.generation как есть; nil, если сценарий заведён не
	// из брифа и авторства ещё не касался (GET /generation тогда отвечает
	// {"status":"idle"}).
	State json.RawMessage
	// Draft — текущий черновик, если он есть; base для правки
	// (voice-edit/text-edit) генератору, nil — для создания с нуля.
	Draft json.RawMessage
	Mode  gen.Mode
	// Archived — сценарий в архиве; используется скорее для диагностики,
	// чем для решения — BeginGeneration проверяет архивность сам, в своей
	// транзакции, не полагаясь на этот снимок вне её.
	Archived bool
}
