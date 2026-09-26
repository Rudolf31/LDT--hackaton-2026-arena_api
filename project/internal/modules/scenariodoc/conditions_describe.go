package scenariodoc

import (
	"fmt"
	"strconv"
	"strings"
)

var meterScaleLabels = map[MeterScale]string{
	MeterTrust:       "доверие",
	MeterPressure:    "давление",
	MeterCredibility: "достоверность",
	MeterPatience:    "терпение",
	MeterCredit:      "очки доводов",
	MeterTurn:        "номер реплики",
}

var comparisonPhrases = map[Comparison]string{
	CompLT:  "меньше",
	CompLTE: "не больше",
	CompGT:  "больше",
	CompGTE: "не меньше",
	CompEQ:  "равно",
}

var evidenceLabels = map[string]string{
	"competitor_quote": "предложение конкурента",
	"market_price":     "рыночная цена",
	"past_contract":    "прошлый договор",
	"spec_document":    "техническая документация",
	"third_party":      "мнение третьей стороны",
	"policy_rule":      "внутреннее правило компании",
	"own_constraint":   "собственное ограничение",
	"opinion":          "личное мнение",
	"none":             "без опоры",
}

func evidenceLabel(id string) string {
	if l, ok := evidenceLabels[id]; ok {
		return l
	}
	return id
}

// ruGenitiveCount — «N раз» в родительном падеже числительного (не меньше
// двух раз, не меньше пяти раз…) для малых чисел, встречающихся в условиях
// на счётчик ходов; для больших чисел — обычная цифра.
var ruGenitiveNumbers = map[int]string{
	1: "одного", 2: "двух", 3: "трёх", 4: "четырёх", 5: "пяти",
	6: "шести", 7: "семи", 8: "восьми", 9: "девяти", 10: "десяти",
}

func ruGenitiveCount(n int) string {
	if word, ok := ruGenitiveNumbers[n]; ok {
		return word
	}
	return strconv.Itoa(n)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = []rune(strings.ToLower(string(runes[0])))[0]
	return string(runes)
}

// DescribeCondition переводит условие в русскую фразу (раздел 12,
// «читается как»). dict даёт подписи отметок/фактов/этапов/предметов —
// узлы, которым подписи не нужны (always, deal), описываются и с пустым
// словарём.
func DescribeCondition(cond Condition, dict LabelDictionary) string {
	switch cond.Kind {
	case ConditionAll:
		return joinConditionParts(cond.Children, dict, " и ")
	case ConditionAny:
		return joinConditionParts(cond.Children, dict, ", или ")
	case ConditionNot:
		if cond.Child == nil {
			return "не (условие не задано)"
		}
		return "не (" + DescribeCondition(*cond.Child, dict) + ")"
	case ConditionAlways:
		return "всегда"
	case ConditionMeter:
		scale := meterScaleLabels[cond.Meter]
		if scale == "" {
			scale = string(cond.Meter)
		}
		return fmt.Sprintf("%s %s %d", scale, comparisonPhrases[cond.MeterOp], cond.MeterValue)
	case ConditionFlag:
		if cond.FlagIs {
			return dict.flagLabel(cond.FlagID)
		}
		return "не: " + dict.flagLabel(cond.FlagID)
	case ConditionFact:
		if cond.FactRevealed {
			return opponentNameOr(dict) + " рассказал про " + dict.factLabel(cond.FactID)
		}
		return "«" + dict.factLabel(cond.FactID) + "» ещё не раскрыт"
	case ConditionMove:
		label := lowerFirst(dict.moveLabelOf(cond.MoveID))
		if cond.MoveScope == ScopeThisReply {
			return label + " — в этой реплике"
		}
		return fmt.Sprintf("%s — не меньше %s раз за разговор", label, ruGenitiveCount(cond.MoveAtLeast))
	case ConditionArgument:
		return describeArgument(cond, dict)
	case ConditionOffer:
		return describeOffer(cond, dict)
	case ConditionStage:
		if cond.StageScope == StageVisited {
			return "разговор уже был на этапе «" + dict.stageLabel(cond.StageID) + "»"
		}
		return "разговор сейчас на этапе «" + dict.stageLabel(cond.StageID) + "»"
	case ConditionDeal:
		switch cond.Deal {
		case DealAgreed:
			return "сделка заключена"
		case DealNone:
			return "сделка не заключена"
		case DealBeyondParticipantLimit:
			return "сделка заключена за пределами полномочий участника"
		default:
			return "сделка в неизвестном состоянии"
		}
	default:
		return "неизвестное условие"
	}
}

func opponentNameOr(dict LabelDictionary) string {
	if dict.OpponentName != "" {
		return dict.OpponentName
	}
	return "оппонент"
}

func joinConditionParts(children []Condition, dict LabelDictionary, sep string) string {
	parts := make([]string, 0, len(children))
	for _, c := range children {
		parts = append(parts, DescribeCondition(c, dict))
	}
	return strings.Join(parts, sep)
}

func describeArgument(cond Condition, dict LabelDictionary) string {
	var subject string
	if len(cond.ArgumentEvidence) == 0 {
		subject = "довод"
	} else {
		labels := make([]string, 0, len(cond.ArgumentEvidence))
		for _, ev := range cond.ArgumentEvidence {
			labels = append(labels, evidenceLabel(ev))
		}
		subject = "довод «" + strings.Join(labels, "», «") + "»"
	}
	if cond.HasArgumentConcrete {
		if cond.ArgumentConcrete {
			subject += " с цифрой"
		} else {
			subject += " без цифры"
		}
	}
	if cond.HasArgumentInterest {
		subject += " в интерес «" + dict.interestLabel(cond.ArgumentInterest) + "»"
	}
	if cond.ArgumentScope == ScopeThisReply {
		return "в этой реплике " + subject
	}
	return "за разговор был " + subject
}

func describeOffer(cond Condition, dict LabelDictionary) string {
	issueLabel := dict.issueLabel(cond.OfferIssue)
	var who string
	switch cond.OfferSide {
	case OfferParticipant:
		if cond.OfferScope == OfferScopeLast {
			who = "участник последний раз назвал"
		} else {
			who = "участник в этой реплике назвал"
		}
	case OfferOpponent:
		who = "у оппонента сейчас"
	default:
		who = "названо"
	}
	value := describeOfferValue(cond, dict)
	if cond.OfferOp == CompIs || cond.OfferOp == CompIsNot {
		verb := "равно"
		if cond.OfferOp == CompIsNot {
			verb = "не равно"
		}
		return fmt.Sprintf("%s «%s» %s %s", who, issueLabel, verb, value)
	}
	cmp := comparisonPhrases[cond.OfferOp]
	if cmp == "" {
		cmp = string(cond.OfferOp)
	}
	return fmt.Sprintf("%s «%s» %s %s", who, issueLabel, cmp, value)
}

func describeOfferValue(cond Condition, dict LabelDictionary) string {
	issueType := dict.IssueTypes[cond.OfferIssue]
	switch issueType {
	case IssueNumber:
		if n, ok := asNumber(cond.OfferValue); ok {
			return formatNumber(n)
		}
	case IssueDate:
		if s, ok := asString(cond.OfferValue); ok {
			return formatRussianDate(s)
		}
	case IssueChoice:
		if s, ok := asString(cond.OfferValue); ok {
			return "«" + dict.issueOptionLabel(cond.OfferIssue, s) + "»"
		}
	}
	if s, ok := asString(cond.OfferValue); ok {
		return s
	}
	if n, ok := asNumber(cond.OfferValue); ok {
		return formatNumber(n)
	}
	return "?"
}

// formatNumber — число с пробелом между разрядами тысяч, как в примерах
// спецификации («17 000»), без хвостового ".0" у целых.
func formatNumber(n float64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	whole := int64(n)
	frac := n - float64(whole)
	s := strconv.FormatInt(whole, 10)
	var grouped strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			grouped.WriteByte(' ')
		}
		grouped.WriteRune(r)
	}
	out := grouped.String()
	if frac > 0.0001 {
		out += strings.TrimRight(strings.TrimPrefix(strconv.FormatFloat(frac, 'f', 2, 64), "0"), "0")
	}
	if neg {
		out = "-" + out
	}
	return out
}

var ruMonthGenitive = [...]string{
	"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

// formatRussianDate — «10 марта 2027 года» из "2027-03-10".
func formatRussianDate(iso string) string {
	parts := strings.Split(iso, "-")
	if len(parts) != 3 {
		return iso
	}
	year, err1 := strconv.Atoi(parts[0])
	month, err2 := strconv.Atoi(parts[1])
	day, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil || month < 1 || month > 12 {
		return iso
	}
	return fmt.Sprintf("%d %s %d года", day, ruMonthGenitive[month-1], year)
}
