package scenariodoc

import "fmt"

const ruleMisc = "15"

// checkMisc — правило 15 (прочее): начальные значения шкал доверия,
// давления и достоверности — в собственных границах min/max; конец
// разговора по попыткам «сломать» оппонента наступает позже первой
// попытки ужесточения; этап конца — завершающий, этап ужесточения — нет.
// Целочисленность шкал и их изменений проверяется при разборе документа
// (schema.go) — здесь только то, что схема не ловит.
func checkMisc(doc *Document) []Diagnostic {
	var diags []Diagnostic

	diags = append(diags, checkBoundsWithinRange("доверие", doc.State.Trust, ptr("state", "trust"))...)
	diags = append(diags, checkBoundsWithinRange("давление", doc.State.Pressure, ptr("state", "pressure"))...)
	diags = append(diags, checkBoundsWithinRange("достоверность", doc.State.Credibility, ptr("state", "credibility"))...)

	jb := doc.Jailbreak
	if jb.EndAfterAttempts <= jb.InCharacterAttempts+1 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleMisc, Path: ptrChild(ptr("jailbreak"), "end_after_attempts"),
			Message: fmt.Sprintf(
				"Разговор заканчивается на попытке %d, а ответов в роли %d — ужесточения не будет; конец должен быть позже, чем попытка %d.",
				jb.EndAfterAttempts, jb.InCharacterAttempts, jb.InCharacterAttempts+1),
		})
	}
	if endStage := stageByID(doc, jb.EndStage); endStage != nil && !endStage.Terminal {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleMisc, Path: ptrChild(ptr("jailbreak"), "end_stage"),
			Message: fmt.Sprintf("Этап конца разговора «%s» после попыток «сломать» оппонента должен быть завершающим.", endStage.Label),
		})
	}
	if escStage := stageByID(doc, jb.EscalationStage); escStage != nil && escStage.Terminal {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleMisc, Path: ptrChild(ptr("jailbreak"), "escalation_stage"),
			Message: fmt.Sprintf("Этап ужесточения «%s» не может быть завершающим — разговор должен продолжиться.", escStage.Label),
		})
	}

	return diags
}

func checkBoundsWithinRange(label string, b Bounds, path string) []Diagnostic {
	if b.Initial < b.Min || b.Initial > b.Max {
		return []Diagnostic{{
			Severity: SeverityError, Rule: ruleMisc, Path: ptrChild(path, "initial"),
			Message: fmt.Sprintf("Начальное значение шкалы «%s» (%d) должно быть между её границами %d и %d.", label, b.Initial, b.Min, b.Max),
		}}
	}
	return nil
}
