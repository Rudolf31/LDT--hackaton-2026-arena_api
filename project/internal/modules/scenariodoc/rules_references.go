package scenariodoc

import (
	"fmt"
	"regexp"
)

const ruleReferences = "6"

var substitutionPattern = regexp.MustCompile(`\{([a-z0-9_]+)\}`)

// idRegistry — множества существующих идентификаторов по категориям,
// собранные один раз на документ, чтобы правило 6 (ссылки на
// несуществующее) и правило 9 могли проверять каждое использование id без
// повторного обхода документа.
type idRegistry struct {
	stages    map[string]bool
	facts     map[string]bool
	flags     map[string]bool
	issues    map[string]bool
	interests map[string]bool
	moves     map[string]bool // базовые + собственные
}

func buildIDRegistry(doc *Document) idRegistry {
	reg := idRegistry{
		stages: map[string]bool{}, facts: map[string]bool{}, flags: map[string]bool{},
		issues: map[string]bool{}, interests: map[string]bool{}, moves: map[string]bool{},
	}
	for _, s := range doc.Stages {
		reg.stages[s.ID] = true
	}
	for _, f := range doc.Facts {
		reg.facts[f.ID] = true
	}
	for _, f := range doc.State.Flags {
		reg.flags[f.ID] = true
	}
	for _, is := range doc.Issues {
		reg.issues[is.ID] = true
	}
	for _, in := range doc.Opponent.Brief.Interests {
		reg.interests[in.ID] = true
	}
	for _, m := range baseMoves {
		reg.moves[m.ID] = true
	}
	for _, cm := range doc.CustomMoves {
		reg.moves[cm.ID] = true
	}
	return reg
}

// checkDuplicateIDs — часть правила 6: идентификаторы не повторяются
// внутри своей категории.
func checkDuplicateIDs(doc *Document) []Diagnostic {
	var diags []Diagnostic
	diags = append(diags, checkDuplicatesIn(stageIDs(doc), "этап", ptr("stages"))...)
	diags = append(diags, checkDuplicatesIn(factIDsList(doc), "факт", ptr("facts"))...)
	diags = append(diags, checkDuplicatesIn(flagIDsList(doc), "отметка", ptr("state", "flags"))...)
	diags = append(diags, checkDuplicatesIn(issueIDsList(doc), "предмет торга", ptr("issues"))...)
	diags = append(diags, checkDuplicatesIn(customMoveIDsList(doc), "собственный тип хода", ptr("custom_moves"))...)
	diags = append(diags, checkDuplicatesIn(finalIDsList(doc), "финал", ptr("finals"))...)
	return diags
}

func stageIDs(doc *Document) []string {
	out := make([]string, 0, len(doc.Stages))
	for _, s := range doc.Stages {
		out = append(out, s.ID)
	}
	return out
}
func factIDsList(doc *Document) []string {
	out := make([]string, 0, len(doc.Facts))
	for _, f := range doc.Facts {
		out = append(out, f.ID)
	}
	return out
}
func flagIDsList(doc *Document) []string {
	out := make([]string, 0, len(doc.State.Flags))
	for _, f := range doc.State.Flags {
		out = append(out, f.ID)
	}
	return out
}
func issueIDsList(doc *Document) []string {
	out := make([]string, 0, len(doc.Issues))
	for _, is := range doc.Issues {
		out = append(out, is.ID)
	}
	return out
}
func customMoveIDsList(doc *Document) []string {
	out := make([]string, 0, len(doc.CustomMoves))
	for _, cm := range doc.CustomMoves {
		out = append(out, cm.ID)
	}
	return out
}
func finalIDsList(doc *Document) []string {
	out := make([]string, 0, len(doc.Finals))
	for _, f := range doc.Finals {
		out = append(out, f.ID)
	}
	return out
}

func checkDuplicatesIn(ids []string, kind, path string) []Diagnostic {
	seen := map[string]bool{}
	var diags []Diagnostic
	for _, id := range ids {
		if id == "" {
			continue
		}
		if seen[id] {
			diags = append(diags, Diagnostic{
				Severity: SeverityError, Rule: ruleReferences, Path: path,
				Message: fmt.Sprintf("Идентификатор «%s» повторяется — у каждого элемента «%s» должно быть своё имя.", id, kind),
			})
		}
		seen[id] = true
	}
	return diags
}

// checkReferences — правило 6: все использования идентификаторов ведут на
// существующие этапы, факты, отметки, типы ходов, интересы, предметы
// торга и их варианты.
func checkReferences(doc *Document, dict LabelDictionary) []Diagnostic {
	reg := buildIDRegistry(doc)
	var diags []Diagnostic

	checkStageRef := func(id, path, what string) {
		if id != "" && !reg.stages[id] {
			diags = append(diags, refDiag(path, fmt.Sprintf("%s ведёт на несуществующий этап «%s».", what, id)))
		}
	}
	checkFactRef := func(id, path, what string) {
		if id != "" && !reg.facts[id] {
			diags = append(diags, refDiag(path, fmt.Sprintf("%s ссылается на несуществующий факт «%s».", what, id)))
		}
	}
	checkFlagRef := func(id, path, what string) {
		if id != "" && !reg.flags[id] {
			diags = append(diags, refDiag(path, fmt.Sprintf("%s ссылается на несуществующую отметку «%s».", what, id)))
		}
	}
	checkIssueRef := func(id, path, what string) {
		if id != "" && !reg.issues[id] {
			diags = append(diags, refDiag(path, fmt.Sprintf("%s ссылается на несуществующий предмет торга «%s».", what, id)))
		}
	}
	checkMoveRef := func(id, path, what string) {
		if id != "" && !reg.moves[id] {
			diags = append(diags, refDiag(path, fmt.Sprintf("%s ссылается на несуществующий тип хода «%s».", what, id)))
		}
	}

	checkStageRef(doc.StartStage, ptr("start_stage"), "Начальный этап")
	checkStageRef(doc.EndStages.Deal, ptrChild(ptr("end_stages"), "deal"), "Этап сделки")
	checkStageRef(doc.EndStages.NoDeal, ptrChild(ptr("end_stages"), "no_deal"), "Этап ухода без сделки")
	checkStageRef(doc.WrapupStage, ptr("wrapup_stage"), "Этап сворачивания разговора")
	checkStageRef(doc.Jailbreak.EscalationStage, ptrChild(ptr("jailbreak"), "escalation_stage"), "Этап ужесточения")
	checkStageRef(doc.Jailbreak.EndStage, ptrChild(ptr("jailbreak"), "end_stage"), "Этап конца разговора")

	for si, stage := range doc.Stages {
		stagePath := ptrIndex(ptr("stages"), si)
		for ti, tr := range stage.Transitions {
			trPath := ptrIndex(ptrChild(stagePath, "transitions"), ti)
			checkStageRef(tr.To, ptrChild(trPath, "to"), fmt.Sprintf("Переход из этапа «%s»", stage.Label))
			diags = append(diags, checkConditionReferences(doc, dict, reg, tr.When, ptrChild(trPath, "when"))...)
		}
		if stage.Timeout != nil {
			checkStageRef(stage.Timeout.To, ptrChild(ptrChild(stagePath, "timeout"), "to"), fmt.Sprintf("Ограничение по репликам этапа «%s»", stage.Label))
		}
		for _, factID := range stage.MayReveal {
			checkFactRef(factID, ptrChild(stagePath, "may_reveal"), fmt.Sprintf("Этап «%s»", stage.Label))
		}
		for oi, op := range stage.OfferPolicy {
			opPath := ptrIndex(ptrChild(stagePath, "offer_policy"), oi)
			checkIssueRef(op.Issue, ptrChild(opPath, "issue"), fmt.Sprintf("Правило уступок этапа «%s»", stage.Label))
			issue := issueByID(doc, op.Issue)
			if issue != nil {
				diags = append(diags, checkByOptionKeys(dict, doc, *issue, op.StopAt, ptrChild(opPath, "stop_at"))...)
			}
		}
		for fi, line := range stage.FallbackLines {
			for _, m := range substitutionPattern.FindAllStringSubmatch(line, -1) {
				if !reg.issues[m[1]] {
					diags = append(diags, refDiag(
						ptrIndex(ptrChild(stagePath, "fallback_lines"), fi),
						fmt.Sprintf("В запасной реплике этапа «%s» подстановка {%s} не соответствует ни одному предмету торга.", stage.Label, m[1]),
					))
				}
			}
		}
	}

	for fi, fact := range doc.Facts {
		factPath := ptrIndex(ptr("facts"), fi)
		diags = append(diags, checkConditionReferences(doc, dict, reg, fact.RevealWhen, ptrChild(factPath, "reveal_when"))...)
	}

	for ii, issue := range doc.Issues {
		issuePath := ptrIndex(ptr("issues"), ii)
		if issue.LimitsDependOn != "" {
			checkIssueRef(issue.LimitsDependOn, ptrChild(issuePath, "limits_depend_on"), fmt.Sprintf("Предмет «%s»", issue.Label))
		}
		if issue.RevealedByFact != "" {
			checkFactRef(issue.RevealedByFact, ptrChild(issuePath, "revealed_by_fact"), fmt.Sprintf("Предмет «%s»", issue.Label))
		}
		diags = append(diags, checkByOptionKeys(dict, doc, issue, issue.Participant.Limit, ptrChild(issuePath, "participant"))...)
		diags = append(diags, checkByOptionKeys(dict, doc, issue, issue.Opponent.Limit, ptrChild(issuePath, "opponent"))...)
	}

	for mi, me := range doc.MoveEffects {
		mePath := ptrIndex(ptr("move_effects"), mi)
		checkMoveRef(me.Move, ptrChild(mePath, "move"), "Правило последствий хода")
		if me.If != nil {
			diags = append(diags, checkConditionReferences(doc, dict, reg, *me.If, ptrChild(mePath, "if"))...)
		}
		for _, fl := range me.SetFlags {
			checkFlagRef(fl, ptrChild(mePath, "set_flags"), "Правило последствий хода")
		}
		for _, fl := range me.ClearFlags {
			checkFlagRef(fl, ptrChild(mePath, "clear_flags"), "Правило последствий хода")
		}
	}

	for fi, final := range doc.Finals {
		finalPath := ptrIndex(ptr("finals"), fi)
		diags = append(diags, checkConditionReferences(doc, dict, reg, final.When, ptrChild(finalPath, "when"))...)
	}

	for issueID := range doc.Difficulty.Easy.Issues {
		checkIssueRef(issueID, ptrChild(ptr("difficulty", "easy", "issues"), issueID), "Настройка уровня «лёгкий»")
	}
	for issueID := range doc.Difficulty.Hard.Issues {
		checkIssueRef(issueID, ptrChild(ptr("difficulty", "hard", "issues"), issueID), "Настройка уровня «сложный»")
	}
	for factID, cond := range doc.Difficulty.Easy.FactConditions {
		checkFactRef(factID, ptrChild(ptr("difficulty", "easy", "fact_conditions"), factID), "Настройка уровня «лёгкий»")
		diags = append(diags, checkConditionReferences(doc, dict, reg, cond, ptrChild(ptr("difficulty", "easy", "fact_conditions"), factID))...)
	}
	for factID := range doc.Difficulty.Easy.FactStyles {
		checkFactRef(factID, ptrChild(ptr("difficulty", "easy", "fact_styles"), factID), "Настройка уровня «лёгкий»")
	}
	for factID, cond := range doc.Difficulty.Hard.FactConditions {
		checkFactRef(factID, ptrChild(ptr("difficulty", "hard", "fact_conditions"), factID), "Настройка уровня «сложный»")
		diags = append(diags, checkConditionReferences(doc, dict, reg, cond, ptrChild(ptr("difficulty", "hard", "fact_conditions"), factID))...)
	}
	for factID := range doc.Difficulty.Hard.FactStyles {
		checkFactRef(factID, ptrChild(ptr("difficulty", "hard", "fact_styles"), factID), "Настройка уровня «сложный»")
	}

	diags = append(diags, checkDuplicateIDs(doc)...)
	return diags
}

func checkByOptionKeys(dict LabelDictionary, doc *Document, issue Issue, limit Limit, path string) []Diagnostic {
	if len(limit.ByOption) == 0 || issue.LimitsDependOn == "" {
		return nil
	}
	dep := issueByID(doc, issue.LimitsDependOn)
	if dep == nil {
		return nil
	}
	var diags []Diagnostic
	for optionID := range limit.ByOption {
		found := false
		for _, o := range dep.Options {
			if o.ID == optionID {
				found = true
				break
			}
		}
		if !found {
			diags = append(diags, refDiag(ptrChild(ptrChild(path, "limit"), "by_option"),
				fmt.Sprintf("Значение по варианту «%s» у предмета «%s» ссылается на несуществующий вариант предмета «%s».",
					optionID, dict.issueLabel(issue.ID), dict.issueLabel(issue.LimitsDependOn))))
		}
	}
	return diags
}

func refDiag(path, message string) Diagnostic {
	return Diagnostic{Severity: SeverityError, Rule: ruleReferences, Path: path, Message: message}
}

// checkConditionReferences обходит дерево условия и проверяет каждую
// ссылку на отметку, факт, тип хода, интерес, предмет торга или вариант,
// а также этап (узел stage) — на существование в документе.
func checkConditionReferences(doc *Document, dict LabelDictionary, reg idRegistry, cond Condition, path string) []Diagnostic {
	var diags []Diagnostic
	walkCondition(cond, func(c Condition) {
		switch c.Kind {
		case ConditionFlag:
			if c.FlagID != "" && !reg.flags[c.FlagID] {
				diags = append(diags, refDiag(path, fmt.Sprintf("Условие проверяет отметку «%s», которой нет в списке отметок.", c.FlagID)))
			}
		case ConditionFact:
			if c.FactID != "" && !reg.facts[c.FactID] {
				diags = append(diags, refDiag(path, fmt.Sprintf("Условие проверяет факт «%s», которого нет в списке скрытых фактов.", c.FactID)))
			}
		case ConditionMove:
			if c.MoveID != "" && !reg.moves[c.MoveID] {
				diags = append(diags, refDiag(path, fmt.Sprintf("Условие проверяет тип хода «%s», которого нет в списке типов ходов.", c.MoveID)))
			}
		case ConditionArgument:
			if c.HasArgumentInterest && c.ArgumentInterest != "" && !reg.interests[c.ArgumentInterest] {
				diags = append(diags, refDiag(path, fmt.Sprintf("Условие проверяет интерес «%s», которого нет у оппонента.", c.ArgumentInterest)))
			}
			for _, ev := range c.ArgumentEvidence {
				if _, ok := evidenceLabels[ev]; !ok {
					diags = append(diags, refDiag(path, fmt.Sprintf("Условие ссылается на неизвестную опору довода «%s».", ev)))
				}
			}
		case ConditionOffer:
			if c.OfferIssue == "" || reg.issues[c.OfferIssue] {
				issue := issueByID(doc, c.OfferIssue)
				if issue != nil && issue.Type == IssueChoice {
					if optID, ok := asString(c.OfferValue); ok {
						found := false
						for _, o := range issue.Options {
							if o.ID == optID {
								found = true
								break
							}
						}
						if !found {
							diags = append(diags, refDiag(path, fmt.Sprintf("Условие ссылается на несуществующий вариант «%s» предмета «%s».", optID, dict.issueLabel(c.OfferIssue))))
						}
					}
				}
			} else {
				diags = append(diags, refDiag(path, fmt.Sprintf("Условие ссылается на несуществующий предмет торга «%s».", c.OfferIssue)))
			}
		case ConditionStage:
			if c.StageID != "" && !reg.stages[c.StageID] {
				diags = append(diags, refDiag(path, fmt.Sprintf("Условие проверяет этап «%s», которого нет в списке этапов.", c.StageID)))
			}
		}
	})
	return diags
}
