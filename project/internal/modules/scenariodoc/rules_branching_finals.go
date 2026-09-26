package scenariodoc

import "fmt"

const ruleBranchingFinals = "8"

// checkBranchingAndFinals — правило 8: не меньше трёх развилок и не
// меньше четырёх финалов.
func checkBranchingAndFinals(doc *Document) []Diagnostic {
	var diags []Diagnostic

	branches := 0
	for _, stage := range doc.Stages {
		if isBranching(doc, stage) {
			branches++
		}
	}
	if branches < 3 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleBranchingFinals, Path: ptr("stages"),
			Message: fmt.Sprintf("Развилок %d, нужно не меньше трёх: этапов, из которых разговор может пойти в разные стороны.", branches),
		})
	}

	if len(doc.Finals) < 4 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleBranchingFinals, Path: ptr("finals"),
			Message: fmt.Sprintf("Финалов %d, нужно не меньше четырёх.", len(doc.Finals)),
		})
	}

	return diags
}
