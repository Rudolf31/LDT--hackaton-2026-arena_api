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
}

// Versions — то, что другие модули видят о версиях сценария (CLAUDE.md,
// «Устройство модуля»): контракт scenarios.Version(id) для назначений и
// сессий (04-scenarios.md, задача «Версии»).
type Versions interface {
	Version(ctx context.Context, id uuid.UUID) (VersionInfo, error)
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
