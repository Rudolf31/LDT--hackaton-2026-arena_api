package scenariodoc

import "fmt"

const ruleReachability = "2"
const ruleDeadEnds = "3"

// checkReachability — правило 2: недостижимые этапы (обход от start_stage
// по transitions, timeout и переходам движка).
func checkReachability(doc *Document) []Diagnostic {
	reachable := reachableStages(doc)
	var diags []Diagnostic
	for i, stage := range doc.Stages {
		if stage.ID == "" || reachable[stage.ID] {
			continue
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleReachability, Path: ptrIndex(ptr("stages"), i),
			Message: fmt.Sprintf("В этап «%s» нельзя попасть — добавьте переход в этот этап.", stage.Label),
		})
	}
	return diags
}

// checkDeadEnds — правило 3: у каждого незавершающего этапа есть выход,
// есть хотя бы один завершающий этап, начальный и сворачивающий этапы не
// завершающие, у завершающего этапа нет переходов и уступок, а сделка и
// уход разговора — разные завершающие этапы.
func checkDeadEnds(doc *Document) []Diagnostic {
	var diags []Diagnostic
	terminalCount := 0

	for i, stage := range doc.Stages {
		path := ptrIndex(ptr("stages"), i)
		if stage.Terminal {
			terminalCount++
			if len(stage.Transitions) > 0 || len(stage.OfferPolicy) > 0 || stage.Timeout != nil {
				diags = append(diags, Diagnostic{
					Severity: SeverityError, Rule: ruleDeadEnds, Path: path,
					Message: fmt.Sprintf("Завершающий этап «%s» не может иметь переходов или уступок.", stage.Label),
				})
			}
			continue
		}
		if len(stage.Transitions) == 0 && stage.Timeout == nil {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleDeadEnds, Path: path,
				Message: fmt.Sprintf("Из этапа «%s» нельзя выйти: добавьте переход или ограничение по числу реплик.", stage.Label),
			})
		}
	}

	if terminalCount == 0 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDeadEnds, Path: ptr("stages"),
			Message: "Нужен хотя бы один завершающий этап.",
		})
	}

	if startStage := stageByID(doc, doc.StartStage); startStage != nil && startStage.Terminal {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDeadEnds, Path: ptr("start_stage"),
			Message: "Начальный этап не может быть завершающим.",
		})
	}

	if wrapup := stageByID(doc, doc.WrapupStage); wrapup != nil && wrapup.Terminal {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDeadEnds, Path: ptr("wrapup_stage"),
			Message: fmt.Sprintf("Этап сворачивания разговора «%s» не может быть завершающим: оппонент должен успеть подвести разговор к финалу.", wrapup.Label),
		})
	}

	if doc.EndStages.Deal != "" && doc.EndStages.Deal == doc.EndStages.NoDeal {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDeadEnds, Path: ptr("end_stages"),
			Message: "Этап сделки и этап ухода без сделки должны быть разными.",
		})
	}
	if deal := stageByID(doc, doc.EndStages.Deal); deal != nil && !deal.Terminal {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDeadEnds, Path: ptrChild(ptr("end_stages"), "deal"),
			Message: fmt.Sprintf("Этап сделки «%s» должен быть завершающим.", deal.Label),
		})
	}
	if noDeal := stageByID(doc, doc.EndStages.NoDeal); noDeal != nil && !noDeal.Terminal {
		diags = append(diags, Diagnostic{
			Severity: SeverityError, Rule: ruleDeadEnds, Path: ptrChild(ptr("end_stages"), "no_deal"),
			Message: fmt.Sprintf("Этап ухода без сделки «%s» должен быть завершающим.", noDeal.Label),
		})
	}

	return diags
}
