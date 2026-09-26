package scenariodoc

import "fmt"

const ruleDealZone = "1"
const ruleDealZone1a = "1a"

// checkDealZone — правила 1 и 1а (раздел 17 arena-scenario-format.md):
// зона сделки не пуста, и стартовые/целевые значения сторон не выходят за
// их же пределы (FR-SC-13 — direction-aware, работает для обеих сторон
// шкалы, не только «меньше — лучше»).
func checkDealZone(doc *Document, dict LabelDictionary) []Diagnostic {
	var diags []Diagnostic
	for i, issue := range doc.Issues {
		path := ptrIndex(ptr("issues"), i)
		switch issue.Type {
		case IssueNumber, IssueDate:
			diags = append(diags, checkNumericZone(doc, dict, issue, path)...)
			diags = append(diags, checkNumericOpening(dict, issue, path)...)
		case IssueChoice:
			diags = append(diags, checkChoiceZone(dict, issue, path)...)
			diags = append(diags, checkChoiceOpening(dict, issue, path)...)
		}
	}
	return diags
}

func checkNumericZone(doc *Document, dict LabelDictionary, issue Issue, path string) []Diagnostic {
	anyNonEmpty := false
	var oppSample, partSample IssueValue
	haveSample := false
	for _, variant := range dependentVariants(doc, issue) {
		if !dependentVariantAcceptable(doc, issue, variant) {
			continue
		}
		oppVal := limitFor(issue.Opponent.Limit, variant)
		partVal := limitFor(issue.Participant.Limit, variant)
		oppKey, ok1 := orderKey(oppVal)
		partKey, ok2 := orderKey(partVal)
		if !ok1 || !ok2 {
			continue
		}
		if !haveSample {
			oppSample, partSample, haveSample = oppVal, partVal, true
		}
		if atLeastAsFavorable(issue.Direction, oppKey, partKey) {
			anyNonEmpty = true
			break
		}
	}
	if anyNonEmpty || !haveSample {
		return nil
	}
	return []Diagnostic{{
		Severity: SeverityError, Rule: ruleDealZone, Path: path,
		Message: fmt.Sprintf(
			"Зона сделки пуста: по предмету «%s» %s не уступит дальше %s, а участнику нельзя дальше %s — договориться невозможно.",
			dict.issueLabel(issue.ID), opponentNameOr(dict),
			formatIssueValue(dict, issue, oppSample), formatIssueValue(dict, issue, partSample)),
	}}
}

func checkNumericOpening(dict LabelDictionary, issue Issue, path string) []Diagnostic {
	var diags []Diagnostic

	startKey, ok1 := orderKey(issue.Opponent.Start)
	limitDefault := limitFor(issue.Opponent.Limit, "")
	limitKey, ok2 := orderKey(limitDefault)
	if ok1 && ok2 && !atLeastAsFavorable(issue.Direction, limitKey, startKey) {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDealZone1a, Path: ptrChild(path, "opponent"),
			Message: fmt.Sprintf(
				"Стартовое предложение оппонента по предмету «%s» (%s) выгоднее участнику, чем его предел (%s).",
				dict.issueLabel(issue.ID), formatIssueValue(dict, issue, issue.Opponent.Start), formatIssueValue(dict, issue, limitDefault)),
		})
	}

	targetKey, ok3 := orderKey(issue.Participant.Target)
	partLimitDefault := limitFor(issue.Participant.Limit, "")
	partLimitKey, ok4 := orderKey(partLimitDefault)
	if ok3 && ok4 && !atLeastAsFavorable(issue.Direction, targetKey, partLimitKey) {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDealZone1a, Path: ptrChild(path, "participant"),
			Message: fmt.Sprintf(
				"Цель участника по предмету «%s» (%s) хуже его собственного предела (%s).",
				dict.issueLabel(issue.ID), formatIssueValue(dict, issue, issue.Participant.Target), formatIssueValue(dict, issue, partLimitDefault)),
		})
	}

	return diags
}

func checkChoiceZone(dict LabelDictionary, issue Issue, path string) []Diagnostic {
	for _, p := range issue.Participant.Acceptable {
		if stringInList(issue.Opponent.Acceptable, p) {
			return nil
		}
	}
	return []Diagnostic{{
		Severity: SeverityError, Rule: ruleDealZone, Path: path,
		Message: fmt.Sprintf("Зона сделки пуста: по предмету «%s» нет варианта, который устроит обе стороны.", dict.issueLabel(issue.ID)),
	}}
}

func checkChoiceOpening(dict LabelDictionary, issue Issue, path string) []Diagnostic {
	var diags []Diagnostic

	if issue.Participant.Target.Kind == ValueOption && !stringInList(issue.Participant.Acceptable, issue.Participant.Target.Option) {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDealZone1a, Path: ptrChild(path, "participant"),
			Message: fmt.Sprintf(
				"Цель участника по предмету «%s» — вариант «%s», которого нет среди допустимых участнику.",
				dict.issueLabel(issue.ID), dict.issueOptionLabel(issue.ID, issue.Participant.Target.Option)),
		})
	}

	if len(issue.Opponent.Acceptable) > 0 && issue.Opponent.Start.Kind == ValueOption &&
		issue.Opponent.Acceptable[0] != issue.Opponent.Start.Option {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDealZone1a, Path: ptrChild(path, "opponent"),
			Message: fmt.Sprintf(
				"По предмету «%s» первый вариант в списке допустимых для оппонента («%s») должен совпадать с его стартовым предложением («%s»).",
				dict.issueLabel(issue.ID), dict.issueOptionLabel(issue.ID, issue.Opponent.Acceptable[0]),
				dict.issueOptionLabel(issue.ID, issue.Opponent.Start.Option)),
		})
	}

	return diags
}
