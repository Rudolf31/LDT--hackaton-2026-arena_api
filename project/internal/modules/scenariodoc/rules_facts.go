package scenariodoc

import (
	"fmt"
	"strings"
)

const ruleFacts = "10"
const ruleFactWarning = "fact-warning"

// checkFacts — правило 10: есть хотя бы один скрытый факт, каждый разрешён
// хотя бы на одном достижимом этапе, а факт, который может раскрыться без
// хода участника в этой реплике, раскрывается только манерой «сам» или
// «как довод» (иначе оппонент промолчал бы, хотя факт уже считался бы
// раскрытым — раздел 7 arena-scenario-format.md).
func checkFacts(doc *Document, dict LabelDictionary) []Diagnostic {
	var diags []Diagnostic

	hasHidden := false
	for _, f := range doc.Facts {
		if f.RevealWhen.Kind != ConditionAlways {
			hasHidden = true
			break
		}
	}
	if !hasHidden {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleFacts, Path: ptr("facts"),
			Message: "Нужен хотя бы один скрытый факт — то, что оппонент скрывает и раскрывает только при условии.",
		})
	}

	achievable := reachableStages(doc)

	for fi, fact := range doc.Facts {
		factPath := ptrIndex(ptr("facts"), fi)

		allowedSomewhere := false
		for _, s := range doc.Stages {
			if achievable[s.ID] && stringInList(s.MayReveal, fact.ID) {
				allowedSomewhere = true
				break
			}
		}
		if !allowedSomewhere {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleFacts, Path: factPath,
				Message: fmt.Sprintf("Скрытый факт «%s» нельзя раскрыть ни на одном этапе — добавьте его в «что можно раскрыть».", fact.Label),
			})
		}

		if fact.RevealWhen.Kind != ConditionAlways && !conditionHasKind(fact.RevealWhen, ConditionMove, ConditionArgument) {
			if fact.Style != StyleVolunteers && fact.Style != StyleAsArgument {
				diags = append(diags, Diagnostic{
					Severity: SeverityError, Rule: ruleFacts, Path: ptrChild(factPath, "style"),
					Message: fmt.Sprintf(
						"Факт «%s» может раскрыться без вопроса участника, а манера раскрытия — не «сам» и не «как довод»: %s промолчит, а факт будет считаться раскрытым; выберите манеру «сам» или «как довод» либо привяжите условие к ходу участника.",
						fact.Label, opponentNameOr(dict)),
				})
			}
		}
	}

	diags = append(diags, checkFactStageWarnings(doc, dict, achievable)...)
	return diags
}

// checkFactStageWarnings — предупреждение (не блокирует): вопрос,
// раскрывающий факт, распознаётся и на этапах, где раскрытие этого факта
// не разрешено, — такой вопрос там ничего не даст.
func checkFactStageWarnings(doc *Document, dict LabelDictionary, achievable map[string]bool) []Diagnostic {
	var diags []Diagnostic
	for fi, fact := range doc.Facts {
		moveIDs := revealMoveIDs(fact.RevealWhen)
		if len(moveIDs) == 0 {
			continue
		}
		gated := stagesGatedOnlyByFact(doc, fact.ID)
		var missing []string
		for _, s := range doc.Stages {
			if !achievable[s.ID] || gated[s.ID] || s.Terminal {
				continue
			}
			if stringInList(s.MayReveal, fact.ID) {
				continue
			}
			missing = append(missing, "«"+s.Label+"»")
		}
		if len(missing) == 0 {
			continue
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning, Rule: ruleFactWarning, Path: ptrIndex(ptr("facts"), fi),
			Message: fmt.Sprintf(
				"Факт «%s» раскрывается по вопросу участника, но на этапах %s не разрешён — такой вопрос там ничего не раскроет.",
				fact.Label, strings.Join(missing, ", ")),
		})
	}
	return diags
}

func revealMoveIDs(cond Condition) []string {
	var ids []string
	walkCondition(cond, func(c Condition) {
		if c.Kind == ConditionMove && c.MoveScope == ScopeThisReply && c.MoveID != "" {
			ids = append(ids, c.MoveID)
		}
	})
	return ids
}

type stageEdge struct {
	to   string
	when Condition
}

// stagesGatedOnlyByFact — этапы, все входящие переходы в которые условлены
// раскрытием именно этого факта (значит достижимы только после него, и их
// не нужно включать в список «факт там ничего не раскроет»).
func stagesGatedOnlyByFact(doc *Document, factID string) map[string]bool {
	incoming := map[string][]stageEdge{}
	for _, s := range doc.Stages {
		for _, t := range s.Transitions {
			incoming[t.To] = append(incoming[t.To], stageEdge{t.To, t.When})
		}
	}
	gated := map[string]bool{}
	for stageID, edges := range incoming {
		if stageID == doc.StartStage || len(edges) == 0 {
			continue
		}
		all := true
		for _, e := range edges {
			if !conditionReferencesFactRevealed(e.when, factID) {
				all = false
				break
			}
		}
		if all {
			gated[stageID] = true
		}
	}
	return gated
}

func conditionReferencesFactRevealed(cond Condition, factID string) bool {
	found := false
	walkCondition(cond, func(c Condition) {
		if c.Kind == ConditionFact && c.FactID == factID && c.FactRevealed {
			found = true
		}
	})
	return found
}
