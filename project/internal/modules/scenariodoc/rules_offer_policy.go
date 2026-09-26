package scenariodoc

import "fmt"

const ruleOpponentLimit = "5"
const ruleOfferPolicyFilled = "12"

// checkOpponentLimit — правило 5: оппонент не может уступить дальше
// собственного предела ни на одном этапе.
func checkOpponentLimit(doc *Document, dict LabelDictionary) []Diagnostic {
	var diags []Diagnostic
	for si, stage := range doc.Stages {
		for oi, op := range stage.OfferPolicy {
			if !op.CanMove {
				continue
			}
			issue := issueByID(doc, op.Issue)
			if issue == nil {
				continue
			}
			path := ptrIndex(ptrChild(ptrIndex(ptr("stages"), si), "offer_policy"), oi)
			switch issue.Type {
			case IssueNumber, IssueDate:
				for _, variant := range dependentVariants(doc, *issue) {
					stopVal := limitFor(op.StopAt, variant)
					limitVal := limitFor(issue.Opponent.Limit, variant)
					stopKey, ok1 := orderKey(stopVal)
					limitKey, ok2 := orderKey(limitVal)
					if !ok1 || !ok2 {
						continue
					}
					if atLeastAsFavorable(issue.Direction, limitKey, stopKey) {
						continue
					}
					variantSuffix := ""
					if variant != "" {
						variantSuffix = fmt.Sprintf(" при варианте «%s»", dict.issueOptionLabel(issue.LimitsDependOn, variant))
					}
					diags = append(diags, Diagnostic{
						Severity: SeverityError, Rule: ruleOpponentLimit, Path: path,
						Message: fmt.Sprintf(
							"На этапе «%s» %s может уступить по предмету «%s» до %s%s — это дальше его предела %s.",
							stage.Label, opponentNameOr(dict), dict.issueLabel(issue.ID),
							formatIssueValue(dict, *issue, stopVal), variantSuffix, formatIssueValue(dict, *issue, limitVal)),
					})
				}
			case IssueChoice:
				if op.StopAt.Value.Kind == ValueOption && !stringInList(issue.Opponent.Acceptable, op.StopAt.Value.Option) {
					diags = append(diags, Diagnostic{
						Severity: SeverityError, Rule: ruleOpponentLimit, Path: path,
						Message: fmt.Sprintf(
							"На этапе «%s» граница уступки по предмету «%s» — вариант «%s», недопустимый оппоненту.",
							stage.Label, dict.issueLabel(issue.ID), dict.issueOptionLabel(issue.ID, op.StopAt.Value.Option)),
					})
				}
			}
		}
	}
	return diags
}

// checkOfferPolicyFilled — правило 12: при can_move заполнены граница,
// стоимость и признак встречной уступки, для числа и даты — шаг (по дате —
// целыми днями), и предмет описан на этапе не больше одного раза.
func checkOfferPolicyFilled(doc *Document, dict LabelDictionary) []Diagnostic {
	var diags []Diagnostic
	for si, stage := range doc.Stages {
		seen := map[string]bool{}
		for oi, op := range stage.OfferPolicy {
			path := ptrIndex(ptrChild(ptrIndex(ptr("stages"), si), "offer_policy"), oi)
			if op.Issue != "" {
				if seen[op.Issue] {
					diags = append(diags, Diagnostic{
						Severity: SeverityError, Rule: ruleOfferPolicyFilled, Path: path,
						Message: fmt.Sprintf("Предмет «%s» описан в этапе «%s» больше одного раза.", dict.issueLabel(op.Issue), stage.Label),
					})
				}
				seen[op.Issue] = true
			}
			if !op.CanMove {
				continue
			}
			var missing []string
			if !op.HasStopAt {
				missing = append(missing, "граница")
			}
			if !op.HasUnlockCost {
				missing = append(missing, "стоимость шага")
			}
			if !op.HasRequiresReciprocity {
				missing = append(missing, "встречная уступка")
			}
			issue := issueByID(doc, op.Issue)
			if issue != nil && (issue.Type == IssueNumber || issue.Type == IssueDate) {
				if !op.HasStep {
					missing = append(missing, "шаг")
				} else if issue.Type == IssueDate && op.Step != float64(int(op.Step)) {
					diags = append(diags, Diagnostic{
						Severity: SeverityError, Rule: ruleOfferPolicyFilled, Path: path,
						Message: fmt.Sprintf("Шаг по предмету «%s» — это дата, шаг должен быть целыми днями.", dict.issueLabel(op.Issue)),
					})
				}
			}
			if len(missing) > 0 {
				diags = append(diags, Diagnostic{
					Severity: SeverityError, Rule: ruleOfferPolicyFilled, Path: path,
					Message: fmt.Sprintf("В этапе «%s» для предмета «%s» не заполнено: %s.", stage.Label, dict.issueLabel(op.Issue), joinRussianList(missing)),
				})
			}
		}
	}
	return diags
}

func joinRussianList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	out := items[0]
	for _, it := range items[1:] {
		out += ", " + it
	}
	return out
}
