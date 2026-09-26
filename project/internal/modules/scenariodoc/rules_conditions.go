package scenariodoc

import "fmt"

// checkConditionsCorrectness — оставшаяся часть правила 7 (вид узла,
// известные поля и глубина уже проверены при разборе, conditions.go):
// сравнения подходят типу предмета, у offer правильный scope по стороне,
// отметка, которую условие проверяет, кем-то ставится, а объявленная
// отметка где-то проверяется.
func checkConditionsCorrectness(doc *Document, dict LabelDictionary) []Diagnostic {
	var diags []Diagnostic

	setFlags := map[string]bool{}
	for _, me := range doc.MoveEffects {
		for _, f := range me.SetFlags {
			setFlags[f] = true
		}
	}
	checkedFlags := map[string]bool{}

	visitAllConditions(doc, func(root Condition, rootPath string) {
		walkCondition(root, func(c Condition) {
			switch c.Kind {
			case ConditionOffer:
				diags = append(diags, checkOfferComparison(doc, dict, c, rootPath)...)
			case ConditionFlag:
				if c.FlagID == "" {
					return
				}
				checkedFlags[c.FlagID] = true
				if !setFlags[c.FlagID] {
					diags = append(diags, Diagnostic{
						Severity: SeverityError, Rule: ruleConditions, Path: rootPath,
						Message: fmt.Sprintf("Условие ждёт отметку «%s», но ни одно правило её не ставит — эта ветка никогда не сработает.", dict.flagLabel(c.FlagID)),
					})
				}
			}
		})
	})

	for _, fd := range doc.State.Flags {
		if fd.ID != "" && !checkedFlags[fd.ID] {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleConditions, Path: ptr("state", "flags"),
				Message: fmt.Sprintf("Отметка «%s» объявлена, но ни одно условие её не проверяет.", fd.Label),
			})
		}
	}

	return diags
}

func checkOfferComparison(doc *Document, dict LabelDictionary, c Condition, path string) []Diagnostic {
	issue := issueByID(doc, c.OfferIssue)
	var diags []Diagnostic
	if issue != nil {
		allowed := numberComparisons
		if issue.Type == IssueChoice {
			allowed = choiceComparisons
		}
		if !comparisonAllowed(c.OfferOp, allowed) {
			msg := fmt.Sprintf("Предмет «%s» — число или дата, его можно проверять только сравнениями «больше / меньше».", dict.issueLabel(issue.ID))
			if issue.Type == IssueChoice {
				msg = fmt.Sprintf("Предмет «%s» — выбор из вариантов, его можно проверять только «равно / не равно».", dict.issueLabel(issue.ID))
			}
			diags = append(diags, Diagnostic{Severity: SeverityError, Rule: ruleConditions, Path: path, Message: msg})
		}
	}
	switch c.OfferSide {
	case OfferParticipant:
		if !c.HasOfferScope || (c.OfferScope != OfferScopeThisReply && c.OfferScope != OfferScopeLast) {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleConditions, Path: path,
				Message: "Условие на предмет участника должно уточнять «в этой реплике» или «последнее названное».",
			})
		}
	case OfferOpponent:
		if c.HasOfferScope {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleConditions, Path: path,
				Message: "Условие на предложение оппонента не уточняется «в этой реплике» — у оппонента всегда текущее предложение.",
			})
		}
	}
	return diags
}
