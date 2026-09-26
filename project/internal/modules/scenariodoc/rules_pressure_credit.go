package scenariodoc

import "fmt"

const rulePressureCredit = "13"

// checkPressureNoCredit — правило 13: давление не даёт очков доводов
// (раздел 10 arena-scenario-format.md): правило на ход давления не
// начисляет очки; правило без типа хода начисляет их только с условием на
// то, что участник сделал в этой реплике; вес «без опоры» строго 0.
func checkPressureNoCredit(doc *Document, dict LabelDictionary) []Diagnostic {
	var diags []Diagnostic

	for i, me := range doc.MoveEffects {
		if me.Change.Credit <= 0 {
			continue
		}
		path := ptrIndex(ptr("move_effects"), i)
		if me.Move != "" {
			base, ok := resolveBaseMove(doc, me.Move)
			if ok && pressureMoves[base] {
				diags = append(diags, Diagnostic{
					Severity: SeverityError, Rule: rulePressureCredit, Path: ptrChild(path, "change"),
					Message: "Давление без доводов не даёт очков доводов: уберите начисление очков из правила.",
				})
			}
			continue
		}
		if me.If == nil || !conditionReferencesThisReplyAction(*me.If) {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: rulePressureCredit, Path: ptrChild(path, "if"),
				Message: "Правило без типа хода начисляет очки доводов на любой реплике, даже на давлении: добавьте условие на то, что участник сделал в этой реплике.",
			})
		}
	}

	if doc.EvidenceWeights.None != 0 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: rulePressureCredit, Path: ptr("evidence_weights", "none"),
			Message: fmt.Sprintf("Вес «без опоры» должен быть строго 0, а не %s: давление без доводов не должно давать очков.", formatNumber(doc.EvidenceWeights.None)),
		})
	}

	return diags
}

// conditionReferencesThisReplyAction — содержит ли условие узел offer или
// argument со scope "в этой реплике" — признак того, что оно проверяет
// именно текущий ход участника, а не состояние вообще.
func conditionReferencesThisReplyAction(cond Condition) bool {
	found := false
	walkCondition(cond, func(c Condition) {
		if c.Kind == ConditionOffer && c.OfferSide == OfferParticipant && c.HasOfferScope && c.OfferScope == OfferScopeThisReply {
			found = true
		}
		if c.Kind == ConditionArgument && c.ArgumentScope == ScopeThisReply {
			found = true
		}
	})
	return found
}
