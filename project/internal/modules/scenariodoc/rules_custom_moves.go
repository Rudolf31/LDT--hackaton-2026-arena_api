package scenariodoc

import "fmt"

const ruleCustomMoves = "9"

// checkCustomMoves — правило 9: у собственного типа хода не меньше трёх
// примеров, и его имя не совпадает с базовым типом.
func checkCustomMoves(doc *Document) []Diagnostic {
	var diags []Diagnostic
	for i, cm := range doc.CustomMoves {
		path := ptrIndex(ptr("custom_moves"), i)
		if len(cm.Examples) < 3 {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleCustomMoves, Path: ptrChild(path, "examples"),
				Message: fmt.Sprintf("У типа хода «%s» меньше трёх примеров реплик — судья участника будет его путать; добавьте примеры.", cm.Label),
			})
		}
		if cm.ID != "" && isBaseMove(cm.ID) {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleCustomMoves, Path: ptrChild(path, "id"),
				Message: fmt.Sprintf("Имя собственного типа хода «%s» совпадает с базовым типом — выберите другое имя.", cm.ID),
			})
		}
	}
	return diags
}
