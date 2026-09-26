package scenariodoc

import "encoding/json"

// ConditionKind — один из двенадцати видов узла условия (раздел 12
// arena-scenario-format.md). Условие — дерево из готовых узлов, не строка
// с выражением (FR-SC-05): ровно один вид на объект.
type ConditionKind int

const (
	ConditionAll ConditionKind = iota
	ConditionAny
	ConditionNot
	ConditionAlways
	ConditionMeter
	ConditionFlag
	ConditionFact
	ConditionMove
	ConditionArgument
	ConditionOffer
	ConditionStage
	ConditionDeal
)

// Comparison — вид сравнения во втором выпадающем списке конструктора.
type Comparison string

const (
	CompLT    Comparison = "lt"
	CompLTE   Comparison = "lte"
	CompGT    Comparison = "gt"
	CompGTE   Comparison = "gte"
	CompEQ    Comparison = "eq"
	CompIs    Comparison = "is"
	CompIsNot Comparison = "is_not"
)

var numberComparisons = []Comparison{CompLT, CompLTE, CompGT, CompGTE, CompEQ}
var choiceComparisons = []Comparison{CompIs, CompIsNot}

// ConditionValueKind — вид значения третьего поля конструктора.
type ConditionValueKind string

const (
	ConditionValueNone     ConditionValueKind = "none"     // always, deal
	ConditionValueBool     ConditionValueKind = "bool"     // flag, fact
	ConditionValueInteger  ConditionValueKind = "integer"  // meter
	ConditionValueCount    ConditionValueKind = "count"    // move: число раз за разговор
	ConditionValueEvidence ConditionValueKind = "evidence" // argument: опоры, «с цифрой»
	ConditionValueIssue    ConditionValueKind = "issue"    // offer: число, дата или вариант
	ConditionValueStage    ConditionValueKind = "stage"    // stage: сам этап
)

// MeterScale — шкала состояния для узла meter.
type MeterScale string

const (
	MeterTrust       MeterScale = "trust"
	MeterPressure    MeterScale = "pressure"
	MeterCredibility MeterScale = "credibility"
	MeterPatience    MeterScale = "patience"
	MeterCredit      MeterScale = "credit"
	MeterTurn        MeterScale = "turn"
)

// RepeatScope — this_reply/ever для узлов move и argument.
type RepeatScope string

const (
	ScopeThisReply RepeatScope = "this_reply"
	ScopeEver      RepeatScope = "ever"
)

// OfferSide — чьё предложение проверяет узел offer.
type OfferSide string

const (
	OfferParticipant OfferSide = "participant"
	OfferOpponent    OfferSide = "opponent"
)

// OfferScope — this_reply/last для узла offer (только у side participant).
type OfferScope string

const (
	OfferScopeThisReply OfferScope = "this_reply"
	OfferScopeLast      OfferScope = "last"
)

// StageScope — current/visited для узла stage.
type StageScope string

const (
	StageCurrent StageScope = "current"
	StageVisited StageScope = "visited"
)

// DealState — agreed/none/beyond_participant_limits для узла deal.
type DealState string

const (
	DealAgreed                 DealState = "agreed"
	DealNone                   DealState = "none"
	DealBeyondParticipantLimit DealState = "beyond_participant_limits"
)

// Condition — один узел дерева условия. Ровно одна группа полей действует,
// по Kind: плоский тип проще интерфейса на 12 реализаций для дерева,
// которое парсится, обходится и переводится в русскую фразу, но само
// никогда не строится программно за пределами этого пакета.
type Condition struct {
	Kind ConditionKind

	Children []Condition // all, any
	Child    *Condition  // not

	Meter      MeterScale // meter
	MeterOp    Comparison
	MeterValue int

	FlagID string // flag
	FlagIs bool

	FactID       string // fact
	FactRevealed bool

	MoveID      string // move
	MoveScope   RepeatScope
	MoveAtLeast int
	HasAtLeast  bool

	ArgumentEvidence    []string // argument (внутри "argument": {...})
	ArgumentInterest    string
	HasArgumentInterest bool
	ArgumentConcrete    bool
	HasArgumentConcrete bool
	ArgumentScope       RepeatScope

	OfferIssue    string // offer
	OfferSide     OfferSide
	OfferScope    OfferScope
	HasOfferScope bool
	OfferOp       Comparison
	OfferValue    json.RawMessage // число, строка-дата или id варианта — тип решает issues[].type

	StageID    string // stage
	StageScope StageScope

	Deal DealState // deal
}

var conditionAllowedKeys = map[string][]string{
	"all":      {"all"},
	"any":      {"any"},
	"not":      {"not"},
	"always":   {"always"},
	"meter":    {"meter", "op", "value"},
	"flag":     {"flag", "is"},
	"fact":     {"fact", "revealed"},
	"move":     {"move", "scope", "at_least"},
	"argument": {"argument", "scope"},
	"offer":    {"offer", "side", "scope", "op", "value"},
	"stage":    {"stage", "scope"},
	"deal":     {"deal"},
}

var conditionDiscriminators = []string{
	"all", "any", "not", "always", "meter", "flag", "fact", "move",
	"argument", "offer", "stage", "deal",
}

const ruleConditions = "7"

// decodeCondition разбирает узел условия по правилам раздела 12: ровно
// один вид узла (правило 7), известные поля своего вида, глубина групп
// не больше двух (not в счёт не идёт). Возвращает диагностики вместо
// ошибки — как и весь пакет, продолжает разбор лучшим приближением, чтобы
// вызывающий код увидел все проблемы документа за один проход, а не
// первую попавшуюся.
func decodeCondition(raw json.RawMessage, path string) (Condition, []Diagnostic) {
	obj, ok := asObject(raw)
	if !ok {
		return Condition{}, []Diagnostic{{
			Severity: SeverityError, Rule: ruleConditions, Path: path,
			Message: "Условие должно быть объектом ровно одного вида узла.",
		}}
	}

	var found []string
	for _, key := range conditionDiscriminators {
		if _, present := obj[key]; present {
			found = append(found, key)
		}
	}
	if len(found) != 1 {
		return Condition{}, []Diagnostic{{
			Severity: SeverityError, Rule: ruleConditions, Path: path,
			Message: "Условие должно быть объектом ровно одного вида узла — «и», «или», «не», «всегда», шкала, отметка, факт, тип хода, довод, предложение, этап или сделка.",
		}}
	}
	kind := found[0]
	diags := checkKnownKeys(obj, conditionAllowedKeys[kind], path, ruleConditions)

	switch kind {
	case "all", "any":
		cond, d := decodeConditionGroup(kind, obj[kind], path)
		return cond, append(diags, d...)
	case "not":
		child, d := decodeCondition(obj["not"], ptrChild(path, "not"))
		diags = append(diags, d...)
		return Condition{Kind: ConditionNot, Child: &child}, diags
	case "always":
		return decodeConditionAlways(obj, path, diags)
	case "meter":
		return decodeConditionMeter(obj, path, diags)
	case "flag":
		return decodeConditionFlag(obj, path, diags)
	case "fact":
		return decodeConditionFact(obj, path, diags)
	case "move":
		return decodeConditionMove(obj, path, diags)
	case "argument":
		return decodeConditionArgument(obj, path, diags)
	case "offer":
		return decodeConditionOffer(obj, path, diags)
	case "stage":
		return decodeConditionStage(obj, path, diags)
	case "deal":
		return decodeConditionDeal(obj, path, diags)
	default:
		return Condition{}, diags
	}
}

func decodeConditionGroup(kind string, raw json.RawMessage, path string) (Condition, []Diagnostic) {
	arr, ok := asArray(raw)
	if !ok {
		return Condition{}, []Diagnostic{wrongType(path, ruleConditions, kind, "списком условий")}
	}
	condKind := ConditionAll
	if kind == "any" {
		condKind = ConditionAny
	}
	childPath := ptrChild(path, kind)
	children := make([]Condition, 0, len(arr))
	var diags []Diagnostic
	for i, el := range arr {
		child, d := decodeCondition(el, ptrIndex(childPath, i))
		children = append(children, child)
		diags = append(diags, d...)
	}
	cond := Condition{Kind: condKind, Children: children}
	if depth := maxGroupDepth(cond); depth > 2 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleConditions, Path: path,
			Message: "Условие вложено глубже двух уровней «и/или» — конструктор его не покажет; упростите условие.",
		})
	}
	return cond, diags
}

func decodeConditionAlways(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	if v, ok := asBool(obj["always"]); !ok || !v {
		diags = append(diags, wrongType(path, ruleConditions, "always", "значением true"))
	}
	return Condition{Kind: ConditionAlways}, diags
}

func decodeConditionMeter(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	cond := Condition{Kind: ConditionMeter}
	scale, ok := asString(obj["meter"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "meter"))
	}
	cond.Meter = MeterScale(scale)
	switch cond.Meter {
	case MeterTrust, MeterPressure, MeterCredibility, MeterPatience, MeterCredit, MeterTurn:
	default:
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleConditions, Path: ptrChild(path, "meter"),
			Message: "Неизвестная шкала — допустимы доверие, давление, достоверность, терпение, очки доводов, номер реплики.",
		})
	}
	op, ok := asString(obj["op"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "op"))
	}
	cond.MeterOp = Comparison(op)
	if !comparisonAllowed(cond.MeterOp, numberComparisons) {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleConditions, Path: ptrChild(path, "op"),
			Message: "Шкала сравнивается только «меньше», «не больше», «больше», «не меньше» или «равно».",
		})
	}
	value, integral, ok := asInt(obj["value"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "value"))
	} else if !integral {
		diags = append(diags, wrongType(path, ruleConditions, "value", "целым числом"))
	}
	cond.MeterValue = value
	return cond, diags
}

func decodeConditionFlag(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	id, ok := asString(obj["flag"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "flag"))
	}
	is, ok := asBool(obj["is"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "is"))
	}
	return Condition{Kind: ConditionFlag, FlagID: id, FlagIs: is}, diags
}

func decodeConditionFact(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	id, ok := asString(obj["fact"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "fact"))
	}
	revealed, ok := asBool(obj["revealed"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "revealed"))
	}
	return Condition{Kind: ConditionFact, FactID: id, FactRevealed: revealed}, diags
}

func decodeConditionMove(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	cond := Condition{Kind: ConditionMove}
	id, ok := asString(obj["move"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "move"))
	}
	cond.MoveID = id
	scope, ok := asString(obj["scope"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "scope"))
	}
	cond.MoveScope = RepeatScope(scope)
	if cond.MoveScope != ScopeThisReply && cond.MoveScope != ScopeEver {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleConditions, Path: ptrChild(path, "scope"),
			Message: "Тип хода проверяется «в этой реплике» или «за разговор».",
		})
	}
	if raw, present := obj["at_least"]; present {
		v, integral, ok := asInt(raw)
		if !ok || !integral {
			diags = append(diags, wrongType(path, ruleConditions, "at_least", "целым числом"))
		}
		cond.MoveAtLeast = v
		cond.HasAtLeast = true
	} else if cond.MoveScope == ScopeEver {
		diags = append(diags, missingField(path, ruleConditions, "at_least"))
	}
	return cond, diags
}

func decodeConditionArgument(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	cond := Condition{Kind: ConditionArgument}
	argRaw, present := obj["argument"]
	if !present {
		diags = append(diags, missingField(path, ruleConditions, "argument"))
		argRaw = json.RawMessage(`{}`)
	}
	argObj, ok := asObject(argRaw)
	if !ok {
		diags = append(diags, wrongType(path, ruleConditions, "argument", "объектом"))
		argObj = map[string]json.RawMessage{}
	} else {
		diags = append(diags, checkKnownKeys(argObj, []string{"evidence", "interest", "concrete"}, ptrChild(path, "argument"), ruleConditions)...)
	}
	if raw, present := argObj["evidence"]; present {
		if ev, ok := asStringArray(raw); ok {
			cond.ArgumentEvidence = ev
		} else {
			diags = append(diags, wrongType(ptrChild(path, "argument"), ruleConditions, "evidence", "списком опор"))
		}
	}
	if raw, present := argObj["interest"]; present {
		if v, ok := asString(raw); ok {
			cond.ArgumentInterest = v
			cond.HasArgumentInterest = true
		} else {
			diags = append(diags, wrongType(ptrChild(path, "argument"), ruleConditions, "interest", "строкой"))
		}
	}
	if raw, present := argObj["concrete"]; present {
		if v, ok := asBool(raw); ok {
			cond.ArgumentConcrete = v
			cond.HasArgumentConcrete = true
		} else {
			diags = append(diags, wrongType(ptrChild(path, "argument"), ruleConditions, "concrete", "true/false"))
		}
	}
	scope, ok := asString(obj["scope"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "scope"))
	}
	cond.ArgumentScope = RepeatScope(scope)
	if cond.ArgumentScope != ScopeThisReply && cond.ArgumentScope != ScopeEver {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleConditions, Path: ptrChild(path, "scope"),
			Message: "Довод проверяется «в этой реплике» или «за разговор».",
		})
	}
	return cond, diags
}

func decodeConditionOffer(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	cond := Condition{Kind: ConditionOffer}
	issue, ok := asString(obj["offer"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "offer"))
	}
	cond.OfferIssue = issue
	side, ok := asString(obj["side"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "side"))
	}
	cond.OfferSide = OfferSide(side)
	if cond.OfferSide != OfferParticipant && cond.OfferSide != OfferOpponent {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleConditions, Path: ptrChild(path, "side"),
			Message: "Предложение проверяется «у оппонента» или «у участника».",
		})
	}
	if raw, present := obj["scope"]; present {
		if v, ok := asString(raw); ok {
			cond.OfferScope = OfferScope(v)
			cond.HasOfferScope = true
		} else {
			diags = append(diags, wrongType(path, ruleConditions, "scope", "строкой"))
		}
	}
	op, ok := asString(obj["op"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "op"))
	}
	cond.OfferOp = Comparison(op)
	if raw, present := obj["value"]; present {
		cond.OfferValue = raw
	} else {
		diags = append(diags, missingField(path, ruleConditions, "value"))
	}
	return cond, diags
}

func decodeConditionStage(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	id, ok := asString(obj["stage"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "stage"))
	}
	scope, ok := asString(obj["scope"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "scope"))
	}
	cond := Condition{Kind: ConditionStage, StageID: id, StageScope: StageScope(scope)}
	if cond.StageScope != StageCurrent && cond.StageScope != StageVisited {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleConditions, Path: ptrChild(path, "scope"),
			Message: "Этап проверяется «текущий» или «уже пройден».",
		})
	}
	return cond, diags
}

func decodeConditionDeal(obj map[string]json.RawMessage, path string, diags []Diagnostic) (Condition, []Diagnostic) {
	v, ok := asString(obj["deal"])
	if !ok {
		diags = append(diags, missingField(path, ruleConditions, "deal"))
	}
	deal := DealState(v)
	if deal != DealAgreed && deal != DealNone && deal != DealBeyondParticipantLimit {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleConditions, Path: ptrChild(path, "deal"),
			Message: "Сделка проверяется как «заключена», «не заключена» или «заключена за пределами полномочий участника».",
		})
	}
	return Condition{Kind: ConditionDeal, Deal: deal}, diags
}

func comparisonAllowed(op Comparison, allowed []Comparison) bool {
	for _, a := range allowed {
		if a == op {
			return true
		}
	}
	return false
}

// maxGroupDepth считает глубину вложенных групп all/any; not глубину не
// увеличивает (раздел 12.2: «галочка "наоборот" уровнем не считается»).
func maxGroupDepth(cond Condition) int {
	switch cond.Kind {
	case ConditionNot:
		if cond.Child == nil {
			return 0
		}
		return maxGroupDepth(*cond.Child)
	case ConditionAll, ConditionAny:
		max := 0
		for _, c := range cond.Children {
			if d := maxGroupDepth(c); d > max {
				max = d
			}
		}
		return 1 + max
	default:
		return 0
	}
}

// walkCondition обходит все узлы дерева (включая группы) и вызывает visit
// на каждом — общий инструмент для правил, которым нужно найти хоть один
// узел определённого вида где-то в дереве (например «содержит ли условие
// узел move или argument» для правила 10).
func walkCondition(cond Condition, visit func(Condition)) {
	visit(cond)
	switch cond.Kind {
	case ConditionNot:
		if cond.Child != nil {
			walkCondition(*cond.Child, visit)
		}
	case ConditionAll, ConditionAny:
		for _, c := range cond.Children {
			walkCondition(c, visit)
		}
	}
}

// conditionHasKind — есть ли где-то в дереве узел одного из заданных видов.
func conditionHasKind(cond Condition, kinds ...ConditionKind) bool {
	found := false
	walkCondition(cond, func(c Condition) {
		for _, k := range kinds {
			if c.Kind == k {
				found = true
			}
		}
	})
	return found
}
