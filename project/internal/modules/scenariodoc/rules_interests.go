package scenariodoc

import "fmt"

const ruleInterests = "14"

// checkInterests — правило 14: вес каждого интереса оппонента не меньше
// 0,35 (иначе довод, который в него попадает, стоил бы меньше довода мимо
// всех интересов — раздел 9 arena-scenario-format.md).
func checkInterests(doc *Document) []Diagnostic {
	var diags []Diagnostic
	for i, in := range doc.Opponent.Brief.Interests {
		if in.Weight < 0.35 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleInterests,
				Path: ptrIndex(ptr("opponent", "brief", "interests"), i),
				Message: fmt.Sprintf(
					"Вес интереса «%s» %s меньше 0,35 — довод, который в него попадает, стоил бы меньше довода мимо всех интересов.",
					in.Label, formatNumber(in.Weight)),
			})
		}
	}
	return diags
}
