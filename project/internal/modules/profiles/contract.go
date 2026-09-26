// Package profiles — профили тренажёра (FR-PF-01…06): модели, адреса, речь,
// режим ввода, камера, лимиты — в закрытой схеме settings; ключи провайдеров
// — в отдельных столбцах, зашифрованные мастер-ключом. Наружу ключ целиком
// уходит только через SnapshotWithKeys — клиенту-тренажёру при старте сессии
// и при погашении ссылки на репетицию (CLAUDE.md, правило 9; I-9).
package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"arena-portal-backend/internal/api/gen"
)

// ErrNotFound — такого профиля нет.
var ErrNotFound = errors.New("профиль тренажёра не найден")

// Routes — какие маршруты к моделям есть в итоговых настройках профиля:
// облако OpenRouter и/или наш сервер моделей. Отсюда consents выбирает
// вариант текста А/Б/В (D-47), а sessions — можно ли стартовать без
// согласия на внешнюю нейросеть.
type Routes struct {
	OpenRouter bool
	OwnServer  bool
}

// Effective — профиль с настройками после наследования пустых полей из
// профиля по умолчанию (UC-A-04). Ключей здесь нет.
type Effective struct {
	ID                 uuid.UUID
	Revision           int
	Archived           bool
	Settings           gen.TrainerProfileSettings
	Routes             Routes
	ModelProvidersNote string
}

// Snapshot — то, что уходит клиенту-тренажёру при старте: ревизия, итоговые
// настройки и ключи. Без согласия на внешнюю нейросеть в настройках нет
// блока openrouter, а OpenRouterKey всегда nil (FR-AC-07, NFR-S-01).
// Settings без ключей — это же пишется в sessions.profile_snapshot.
type Snapshot struct {
	ProfileID        uuid.UUID
	Revision         int
	Settings         gen.TrainerProfileSettings
	Routes           Routes
	OpenRouterKey    *string
	ModelServerToken *string
}

// Service — контракт profiles для других модулей (assignments, sessions,
// rehearsals, consents, demo).
type Service interface {
	DefaultID(ctx context.Context) (uuid.UUID, error)
	Effective(ctx context.Context, id uuid.UUID) (Effective, error)
	SnapshotWithKeys(ctx context.Context, id uuid.UUID, externalAIAllowed bool) (Snapshot, error)
	// ForClient — то же, что SnapshotWithKeys, но без ключей и без их
	// расшифровки: настройки для входа по коду, до согласия (FR-PF-02).
	ForClient(ctx context.Context, id uuid.UUID, externalAIAllowed bool) (Snapshot, error)
	// Names — названия профилей для экранов; неизвестные в карте отсутствуют.
	Names(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

// ProfileWithKeys — профиль для проверки: итоговые настройки и ключи.
type ProfileWithKeys struct {
	Effective
	OpenRouterKey    *string
	ModelServerToken *string
}

// CheckLine — строка результата «Проверить профиль»: одна модель или
// синтез речи (arena-portal-backend-architecture.md 10.3).
type CheckLine struct {
	Target    gen.ProfileCheckResultRowsTarget
	Route     *gen.ModelRoute
	OK        bool
	LatencyMs *int
	Slow      *bool
	Message   *string
}

// ProfileChecker — короткий тестовый запрос к трём моделям и синтезу речи
// (FR-PF-06). Пока — заглушка «не проверялось» (архитектура 10.3).
type ProfileChecker interface {
	Check(ctx context.Context, p ProfileWithKeys) ([]CheckLine, error)
}

// SessionCounter — интерфейс потребителя: сколько сессий по профилю идёт
// сейчас (TrainerProfileAdmin.running_sessions). Реализует sessions
// (этапы 07/08); до них собирается заглушка.
type SessionCounter interface {
	RunningByProfile(ctx context.Context, profileID uuid.UUID) (int, error)
}

// Provisioner — заведение профиля по умолчанию внутри чужой транзакции
// (базовая заливка при старте, D-46). created = false — профиль по
// умолчанию уже был.
type Provisioner interface {
	EnsureDefaultTx(ctx context.Context, tx pgx.Tx, name string, settings json.RawMessage) (created bool, err error)
}

// checkTimeout — «Проверить профиль» — единственный адрес портала, которому
// разрешено ждать до 20 секунд (архитектура 10.3).
const checkTimeout = 20 * time.Second
