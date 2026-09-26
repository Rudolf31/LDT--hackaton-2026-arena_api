package scenariodoc

import (
	"encoding/json"
	"fmt"
	"time"
)

const ruleFinalsReachability = "4"

// checkFinalsReachability — правило 4 (приближённо, arena-scenario-format.md
// раздел 17: «ловит явные ошибки, но не доказывает достижимость — это
// делают репетиции»): последний финал — «всегда» и ни один финал не стоит
// после него; ни один лист условия финала не ссылается на заведомо
// недостижимый этап, нераскрываемый факт, отметку, которую никто не
// ставит, или числовое/датовое значение вне зоны сделки. Обходятся все
// листья дерева независимо от «и»/«или» — это огрубляет и/или-семантику,
// но соответствует духу правила: ловить явные ошибки, а не доказывать
// достижимость полностью.
func checkFinalsReachability(doc *Document, dict LabelDictionary) []Diagnostic {
	var diags []Diagnostic
	if len(doc.Finals) == 0 {
		return diags
	}

	alwaysIndex := -1
	for i, f := range doc.Finals {
		if f.When.Kind == ConditionAlways {
			alwaysIndex = i
			break
		}
	}
	if alwaysIndex == -1 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleFinalsReachability, Path: ptrIndex(ptr("finals"), len(doc.Finals)-1),
			Message: "Последний финал должен срабатывать всегда — иначе разговор может закончиться без буквы.",
		})
	} else {
		for i := alwaysIndex + 1; i < len(doc.Finals); i++ {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleFinalsReachability, Path: ptrIndex(ptr("finals"), i),
				Message: fmt.Sprintf("Финал «%s» недостижим: раньше него проверяется финал «%s», который срабатывает всегда.",
					doc.Finals[i].Title, doc.Finals[alwaysIndex].Title),
			})
		}
	}

	achievableStages := reachableStages(doc)
	achievableFacts := map[string]bool{}
	for _, s := range doc.Stages {
		if !achievableStages[s.ID] {
			continue
		}
		for _, fid := range s.MayReveal {
			achievableFacts[fid] = true
		}
	}
	settableFlags := map[string]bool{}
	for _, me := range doc.MoveEffects {
		for _, f := range me.SetFlags {
			settableFlags[f] = true
		}
	}
	envelopes, choiceAcceptable := issueEnvelopes(doc)

	for fi, final := range doc.Finals {
		unreachable := false
		walkCondition(final.When, func(c Condition) {
			if unreachable {
				return
			}
			switch c.Kind {
			case ConditionStage:
				if c.StageID != "" && !achievableStages[c.StageID] {
					unreachable = true
				}
			case ConditionFact:
				if c.FactRevealed && c.FactID != "" && !achievableFacts[c.FactID] {
					unreachable = true
				}
			case ConditionFlag:
				if c.FlagIs && c.FlagID != "" && !settableFlags[c.FlagID] {
					unreachable = true
				}
			case ConditionMeter:
				if !meterValueInBounds(doc, c.Meter, c.MeterValue) {
					unreachable = true
				}
			case ConditionOffer:
				if !offerValueReachable(doc, envelopes, choiceAcceptable, c) {
					unreachable = true
				}
			}
		})
		if unreachable {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleFinalsReachability, Path: ptrIndex(ptr("finals"), fi),
				Message: fmt.Sprintf(
					"Финал «%s» недостижим: в условии есть то, чего разговор никогда не даст (значение за пределами уступок, нераскрываемый факт или недостижимый этап).",
					final.Title),
			})
		}
	}

	return diags
}

func meterValueInBounds(doc *Document, meter MeterScale, value int) bool {
	switch meter {
	case MeterTrust:
		return value >= doc.State.Trust.Min && value <= doc.State.Trust.Max
	case MeterPressure:
		return value >= doc.State.Pressure.Min && value <= doc.State.Pressure.Max
	case MeterCredibility:
		return value >= doc.State.Credibility.Min && value <= doc.State.Credibility.Max
	default:
		return true // patience, credit, turn — верхней границы формат не задаёт
	}
}

type valueEnvelope struct {
	min, max float64
	has      bool
}

func (e *valueEnvelope) add(v IssueValue) {
	k, ok := orderKey(v)
	if !ok {
		return
	}
	if !e.has || k < e.min {
		e.min = k
	}
	if !e.has || k > e.max {
		e.max = k
	}
	e.has = true
}

// issueEnvelopes — для каждого числового/датового предмета огибающая всех
// известных документу опорных значений (старт и пределы обеих сторон);
// для выбора — объединение допустимых вариантов обеих сторон.
func issueEnvelopes(doc *Document) (map[string]valueEnvelope, map[string]map[string]bool) {
	envelopes := map[string]valueEnvelope{}
	choices := map[string]map[string]bool{}
	for _, is := range doc.Issues {
		if is.Type == IssueChoice {
			set := map[string]bool{}
			for _, o := range is.Participant.Acceptable {
				set[o] = true
			}
			for _, o := range is.Opponent.Acceptable {
				set[o] = true
			}
			choices[is.ID] = set
			continue
		}
		var env valueEnvelope
		env.add(is.Opponent.Start)
		env.add(is.Participant.Target)
		env.add(is.Opponent.Limit.Value)
		for _, v := range is.Opponent.Limit.ByOption {
			env.add(v)
		}
		env.add(is.Participant.Limit.Value)
		for _, v := range is.Participant.Limit.ByOption {
			env.add(v)
		}
		envelopes[is.ID] = env
	}
	return envelopes, choices
}

func offerValueReachable(doc *Document, envelopes map[string]valueEnvelope, choices map[string]map[string]bool, c Condition) bool {
	issue := issueByID(doc, c.OfferIssue)
	if issue == nil {
		return true
	}
	if issue.Type == IssueChoice {
		optID, ok := asString(c.OfferValue)
		if !ok {
			return true
		}
		if c.OfferOp != CompIs {
			return true
		}
		return choices[issue.ID][optID]
	}
	env, ok := envelopes[issue.ID]
	if !ok || !env.has {
		return true
	}
	value, ok := offerValueOrderKey(c.OfferValue, issue.Type)
	if !ok {
		return true
	}
	switch c.OfferOp {
	case CompLT:
		return env.min < value
	case CompLTE:
		return env.min <= value
	case CompGT:
		return env.max > value
	case CompGTE:
		return env.max >= value
	case CompEQ:
		return env.min <= value && value <= env.max
	default:
		return true
	}
}

func offerValueOrderKey(raw json.RawMessage, issueType IssueType) (float64, bool) {
	switch issueType {
	case IssueNumber:
		return asNumber(raw)
	case IssueDate:
		s, ok := asString(raw)
		if !ok {
			return 0, false
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return 0, false
		}
		return float64(t.Unix()) / 86400, true
	default:
		return 0, false
	}
}
