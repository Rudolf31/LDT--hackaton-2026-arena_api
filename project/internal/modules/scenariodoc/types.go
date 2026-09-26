package scenariodoc

// Document — разобранный документ сценария arena-scenario/1. Строится
// функцией decodeDocument (schema.go) из произвольного JSON: при ошибках
// структуры decodeDocument всё равно возвращает документ по возможности
// целиком (нулевые значения на месте нераспознанных частей) — чтобы
// правила проверки могли отработать по остальным разделам и вернуть все
// найденные проблемы за один проход, а не только первую.
type Document struct {
	Format          string
	Passport        PassportSection
	Brief           Brief
	Opponent        Opponent
	Issues          []Issue
	State           State
	Facts           []Fact
	CustomMoves     []CustomMove
	EvidenceWeights EvidenceWeights
	MoveEffects     []MoveEffect
	StartStage      string
	Stages          []Stage
	EndStages       EndStages
	WrapupStage     string
	Finals          []Final
	Difficulty      Difficulty
	Jailbreak       Jailbreak
	ProcessCriteria string
	Authoring       Authoring
}

// Sphere — сфера сценария (passport.sphere).
type Sphere string

const (
	SphereProcurement        Sphere = "procurement"
	SphereInternalPromotion  Sphere = "internal_promotion"
	SphereInternalClient     Sphere = "internal_client"
	SphereSales              Sphere = "sales"
	SphereContractorDeadline Sphere = "contractor_deadline"
	SphereResourceSplit      Sphere = "resource_split"
	SphereOther              Sphere = "other"
)

// Mode — режим сценария (passport.mode).
type Mode string

const (
	ModeTraining   Mode = "training"
	ModeAssessment Mode = "assessment"
)

// NegotiationType — тип переговоров (passport.negotiation_type).
type NegotiationType string

const (
	NegotiationDistributive NegotiationType = "distributive"
	NegotiationIntegrative  NegotiationType = "integrative"
	NegotiationMixed        NegotiationType = "mixed"
)

// Origin — происхождение документа (passport.origin).
type Origin string

const (
	OriginBrief    Origin = "brief"
	OriginTemplate Origin = "template"
	OriginCopy     Origin = "copy"
	OriginManual   Origin = "manual"
)

type PassportSection struct {
	ID              string
	Version         int
	Title           string
	Mode            Mode
	Sphere          Sphere
	NegotiationType NegotiationType
	Tags            []string
	Origin          Origin
}

type Brief struct {
	Situation        string
	ParticipantRole  string
	Goal             string
	Constraints      []string
	FallbackOption   string
	KnownFacts       []string
	TimeLimitMinutes int
	Hints            []string
}

type Voice struct {
	VoiceID string
	Speed   float64
}

type Appearance struct {
	AvatarID string
}

type OpponentCard struct {
	Name         string
	Role         string
	Organization string
	SpeechManner string
	Voice        Voice
	Appearance   Appearance
}

type Interest struct {
	ID          string
	Label       string
	Weight      float64
	Description string
}

type OpponentBrief struct {
	Character      string
	Goals          []string
	Interests      []Interest
	FallbackOption string
	NeverConcede   []string
	ConcedeFor     []string
	WhatOffends    string
	UnknownAnswer  string
}

type Opponent struct {
	Card  OpponentCard
	Brief OpponentBrief
}

// IssueType — вид предмета торга (issues[].type).
type IssueType string

const (
	IssueNumber IssueType = "number"
	IssueDate   IssueType = "date"
	IssueChoice IssueType = "choice"
)

// Direction — что лучше участнику (issues[].direction), для number/date.
type Direction string

const (
	DirectionLowerBetter  Direction = "lower_better"
	DirectionHigherBetter Direction = "higher_better"
)

type Option struct {
	ID    string
	Label string
}

// ValueKind — какое поле IssueValue действительно.
type ValueKind int

const (
	ValueNumber ValueKind = iota
	ValueDate
	ValueOption
)

// IssueValue — значение предмета торга: ровно одно поле действительно,
// в зависимости от Kind (числа и даты и варианты выбора нельзя выразить
// одним типом Go без интерфейса, а тип предмета известен только из
// контекста issues[].type — поэтому явный дискриминатор проще интерфейса
// на вызывающей стороне).
type IssueValue struct {
	Kind   ValueKind
	Number float64
	Date   string // ISO ГГГГ-ММ-ДД
	Option string
}

// Limit — предел или граница уступки, может зависеть от варианта другого
// предмета (issues[].limits_depend_on): ByOption — по вариантам, Value —
// значение по умолчанию, если для варианта своего нет.
type Limit struct {
	Value    IssueValue
	ByOption map[string]IssueValue
}

type ParticipantSide struct {
	Target     IssueValue
	Limit      Limit    // для number/date
	Acceptable []string // для choice
}

type OpponentSide struct {
	Start      IssueValue
	Limit      Limit    // для number/date
	Acceptable []string // для choice, в порядке уступки; первый — старт
}

type Issue struct {
	ID             string
	Label          string
	Type           IssueType
	Unit           string
	Direction      Direction // для number/date
	Options        []Option  // для choice
	LimitsDependOn string    // id другого предмета-выбора
	RevealedByFact string    // id факта
	Participant    ParticipantSide
	Opponent       OpponentSide
}

type Bounds struct {
	Initial int
	Min     int
	Max     int
}

type Patience struct {
	Initial  int
	PerReply int
}

type Credit struct {
	Initial int
}

type FlagDef struct {
	ID    string
	Label string
}

type State struct {
	Trust       Bounds
	Pressure    Bounds
	Credibility Bounds
	Patience    Patience
	Credit      Credit
	Flags       []FlagDef
}

// FactStyle — манера раскрытия скрытого факта (facts[].style).
type FactStyle string

const (
	StyleOnDirectQuestion FactStyle = "on_direct_question"
	StyleReluctant        FactStyle = "reluctant"
	StyleVolunteers       FactStyle = "volunteers"
	StyleAsArgument       FactStyle = "as_argument"
)

// Change — изменение шкал состояния. Нулевое значение поля значит «не
// меняется»: JSON, где эта шкала не упомянута, и JSON, где она явно 0, для
// движка неразличимы и не обязаны различаться.
type Change struct {
	Trust       int
	Pressure    int
	Credibility int
	Patience    int
	Credit      int
}

type OnReveal struct {
	Change Change
}

type Fact struct {
	ID         string
	Label      string
	Text       string
	RevealWhen Condition
	Style      FactStyle
	OnReveal   *OnReveal
}

type CustomMove struct {
	ID          string
	Parent      string // id базового типа хода
	Label       string
	Description string
	Examples    []string
}

type EvidenceWeights struct {
	CompetitorQuote float64
	MarketPrice     float64
	PastContract    float64
	SpecDocument    float64
	ThirdParty      float64
	PolicyRule      float64
	OwnConstraint   float64
	Opinion         float64
	None            float64
}

// evidenceKeys — порядок и id девяти опор довода (раздел 9
// arena-scenario-format.md), общий для декодирования, правила 13 и
// содержания шаблонов.
var evidenceKeys = []string{
	"competitor_quote", "market_price", "past_contract", "spec_document",
	"third_party", "policy_rule", "own_constraint", "opinion", "none",
}

func (w EvidenceWeights) byKey(key string) (float64, bool) {
	switch key {
	case "competitor_quote":
		return w.CompetitorQuote, true
	case "market_price":
		return w.MarketPrice, true
	case "past_contract":
		return w.PastContract, true
	case "spec_document":
		return w.SpecDocument, true
	case "third_party":
		return w.ThirdParty, true
	case "policy_rule":
		return w.PolicyRule, true
	case "own_constraint":
		return w.OwnConstraint, true
	case "opinion":
		return w.Opinion, true
	case "none":
		return w.None, true
	default:
		return 0, false
	}
}

type MoveEffect struct {
	ID         string
	Move       string // тип хода; пусто — любой, кроме давления
	If         *Condition
	Once       bool
	Change     Change
	SetFlags   []string
	ClearFlags []string
	Note       string
}

type Directive struct {
	Goal           string
	AllowedIntents []string
	Forbidden      []string
	Tactics        []string
}

// OfferPolicy — правило уступок по предмету на этапе (раздел 11.3). Has*-
// флаги отличают «поле явно задано нулевым значением» (requires_reciprocity:
// false, unlock_cost: 0 — оба действительные значения) от «поля вовсе нет»,
// что и проверяет правило 12.
type OfferPolicy struct {
	Issue                  string
	CanMove                bool
	Step                   float64 // для number; для date — целые дни
	HasStep                bool
	StopAt                 Limit
	HasStopAt              bool
	RequiresReciprocity    bool
	HasRequiresReciprocity bool
	UnlockCost             int
	HasUnlockCost          bool
}

type Timeout struct {
	AfterReplies int
	To           string
}

type Transition struct {
	To   string
	When Condition
}

type Stage struct {
	ID            string
	Label         string
	Terminal      bool
	Directive     Directive
	OfferPolicy   []OfferPolicy
	MayReveal     []string
	Fillers       []string
	FallbackLines []string
	Transitions   []Transition
	Timeout       *Timeout
}

type EndStages struct {
	Deal   string
	NoDeal string
}

// Rank — буква финала (finals[].rank).
type Rank string

const (
	RankS Rank = "S"
	RankA Rank = "A"
	RankB Rank = "B"
	RankC Rank = "C"
	RankD Rank = "D"
	RankF Rank = "F"
)

type Final struct {
	ID       string
	Rank     Rank
	Title    string
	When     Condition
	Epilogue string
}

// IssueDifficultyAdjust — настройки уровня сложности для одного предмета
// торга (difficulty.easy/hard.issues.<id>). OpponentStart задан только для
// number/date (раздел 14: у choice старт — всегда первый вариант
// opponent.acceptable). LimitShift — число, для date — дни.
type IssueDifficultyAdjust struct {
	OpponentStart    *IssueValue
	HasOpponentStart bool
	LimitShift       float64
	HasLimitShift    bool
}

// LevelSettings — короткий набор настроек уровня сложности поверх базового
// документа (раздел 14 arena-scenario-format.md). Указатели/Has-флаги —
// поле в документе не обязано присутствовать, а нулевое значение (0) —
// частый действительный вход (например penalty_factor: 0 не то же самое,
// что «не задано»).
type LevelSettings struct {
	PatienceInitial     int
	HasPatienceInitial  bool
	TrustInitial        int
	HasTrustInitial     bool
	UnlockCostFactor    float64
	HasUnlockCostFactor bool
	PenaltyFactor       float64
	HasPenaltyFactor    bool
	Issues              map[string]IssueDifficultyAdjust
	FactConditions      map[string]Condition
	FactStyles          map[string]FactStyle
	OpponentManner      string
	ExtraTactics        []string
	Hints               []string
}

type Difficulty struct {
	Easy LevelSettings
	Hard LevelSettings
}

func (d Difficulty) forLevel(level Level) (LevelSettings, bool) {
	switch level {
	case LevelEasy:
		return d.Easy, true
	case LevelHard:
		return d.Hard, true
	default:
		return LevelSettings{}, false
	}
}

type Jailbreak struct {
	InCharacterAttempts int
	InCharacterHint     string
	EscalationStage     string
	EndAfterAttempts    int
	EndStage            string
}

type CopiedFrom struct {
	ScenarioID string
	Version    int
}

// Authoring — служебная часть документа, только порталу (раздел 18.1
// arena-scenario-format.md). Brief — актуальное поле для описания, с
// которого начался сценарий; поля authoring.questionnaire из отменённой
// версии формата здесь нет (CLAUDE.md, «Действующие решения»).
type Authoring struct {
	SourceTemplate string
	CopiedFrom     *CopiedFrom
	Brief          string
	Notes          string
}
