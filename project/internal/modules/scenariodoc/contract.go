// Package scenariodoc — документ сценария arena-scenario/1: типы, схема,
// пятнадцать правил проверки (FR-SC-08) на трёх уровнях сложности,
// применение уровня сложности (раздел 14 arena-scenario-format.md),
// отпечаток (FR-SC-09, FR-SC-11, FR-SC-14), шесть шаблонов сфер + демо-
// шаблон + каркас «вручную» (FR-SC-02, FR-SC-12) и справочник условий для
// конструктора (FR-SC-05). Единственное исключение из общего устройства
// модуля в этом проекте (CLAUDE.md, «Устройство модуля»): чистая
// библиотека без HTTP и без базы, тестируется целиком юнит-тестами.
//
// Файла arena-scenario.schema.json нет и не будет (CLAUDE.md, «Известные
// расхождения») — схема документа живёт только здесь, в Go-коде.
package scenariodoc

// EngineVersion — версия проверяющего кода, которая пишется в
// CheckResult.engine_version и в scenario_versions.engine_version (D-27).
// Пакета движка нет (CLAUDE.md, «Действующие решения»): эту роль занимает
// сам scenariodoc, и версия у него одна на весь портал, без отдельного
// журнала изменений — меняется вручную при значимой правке правил.
const EngineVersion = "arena-portal/scenariodoc-1"

// Level — уровень сложности сценария.
type Level string

const (
	LevelEasy   Level = "easy"
	LevelNormal Level = "normal"
	LevelHard   Level = "hard"
)

// Severity — тяжесть диагностики проверки.
type Severity string

const (
	// SeverityError блокирует публикацию (FR-SC-08).
	SeverityError Severity = "error"
	// SeverityWarning не блокирует публикацию.
	SeverityWarning Severity = "warning"
)

// Diagnostic — одно сообщение проверки документа. Message — готовая русская
// фраза с подписями вместо идентификаторов (FR-SC-07), Path — путь к полю
// в формате JSON Pointer (RFC 6901), Level — уровень сложности, на котором
// найдена проблема, Rule — машиночитаемый код правила ("schema", "1", "1a",
// "2" … "15", "fact-warning") для проверки покрытия правил тестами (I-12).
type Diagnostic struct {
	Severity Severity
	Level    Level
	Rule     string
	Path     string
	Message  string
}

// Fingerprinter — контракт наружу для отпечатка документа. Сигнатура
// зафиксирована дословно в arena-portal-backend-architecture.md:620-625.
type Fingerprinter interface {
	// Fingerprint возвращает 64 шестнадцатеричных знака в нижнем регистре.
	Fingerprint(document []byte) (string, error)
}

// Template — один из шести шаблонов сфер или демо-шаблон (раздел 18
// arena-scenario-format.md, FR-SC-12). Document уже проходит Validate без
// блокирующих ошибок на всех трёх уровнях сложности — это проверяется
// тестом пакета, а не пересчитывается при каждом вызове Templates.
type Template struct {
	ID          string
	Title       string
	Description string
	Sphere      Sphere
	Document    []byte
}

// EvidenceWeight — одна опора довода документа с весом (UC-M-02, карточка
// шаблона). Evidence — машинный id опоры (raздел 9 arena-scenario-format.md),
// Label — русская подпись.
type EvidenceWeight struct {
	Evidence string
	Label    string
	Weight   float64
}

// PassportSummary — выборка паспорта документа для библиотеки сценариев
// (название, сфера, тип переговоров, теги). Названа не Passport, чтобы не
// конфликтовать в одной области видимости с функцией Passport.
type PassportSummary struct {
	Title           string
	Sphere          Sphere
	NegotiationType NegotiationType
	Tags            []string
}

// ConditionKindSpec — одна строка каталога условий для конструктора
// (раздел 12.2 arena-scenario-format.md): вид узла, подпись первого
// выпадающего списка, допустимые сравнения второго, вид значения третьего.
type ConditionKindSpec struct {
	Kind        ConditionKind
	Label       string
	Comparisons []Comparison
	ValueKind   ConditionValueKind
}
