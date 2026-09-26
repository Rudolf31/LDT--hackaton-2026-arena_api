package scenariodoc

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// ApplyDifficulty возвращает документ с уже применёнными настройками
// уровня сложности (раздел 14 arena-scenario-format.md) — трансформацию
// базового документа, не копию с пометкой уровня. level == LevelNormal
// возвращает документ как есть (перекодированную копию входа). Работает
// на обобщённом дереве JSON (map[string]any), а не через повторную
// кодировку Document, — так вывод остаётся побайтово тем же документом
// с точечными правками, без риска разойтись с исходной записью в местах,
// которые правила сложности не трогают.
func ApplyDifficulty(document []byte, level Level) ([]byte, error) {
	var tree map[string]any
	if err := json.Unmarshal(document, &tree); err != nil {
		return nil, fmt.Errorf("документ должен быть JSON-объектом")
	}
	if level == LevelNormal {
		return json.Marshal(tree)
	}

	doc, diags := decodeDocument(document)
	for _, d := range diags {
		if d.Severity == SeverityError {
			return nil, fmt.Errorf("документ не проходит проверку схемы: %s", d.Message)
		}
	}
	settings, ok := doc.Difficulty.forLevel(level)
	if !ok {
		return nil, fmt.Errorf("неизвестный уровень сложности %q", level)
	}

	applyDifficultyToTree(tree, doc, settings)
	return json.Marshal(tree)
}

func applyDifficultyToTree(tree map[string]any, doc *Document, settings LevelSettings) {
	if state := treeObj(tree["state"]); state != nil {
		if settings.HasPatienceInitial {
			if p := treeObj(state["patience"]); p != nil {
				p["initial"] = float64(settings.PatienceInitial)
			}
		}
		if settings.HasTrustInitial {
			if t := treeObj(state["trust"]); t != nil {
				t["initial"] = float64(settings.TrustInitial)
			}
		}
	}

	issuesArr := treeArr(tree["issues"])
	for i, issue := range doc.Issues {
		if i >= len(issuesArr) {
			break
		}
		issueObj := treeObj(issuesArr[i])
		adj, has := settings.Issues[issue.ID]
		if issueObj == nil || !has {
			continue
		}
		if opponentObj := treeObj(issueObj["opponent"]); opponentObj != nil {
			if adj.HasOpponentStart && adj.OpponentStart != nil {
				opponentObj["start"] = issueValueToJSON(*adj.OpponentStart)
			}
			if adj.HasLimitShift {
				if limitObj := treeObj(opponentObj["limit"]); limitObj != nil {
					shiftLimitTree(limitObj, issue.Type, issue.Direction, adj.LimitShift)
				}
			}
		}
	}

	if settings.HasPenaltyFactor {
		for _, raw := range treeArr(tree["move_effects"]) {
			if effObj := treeObj(raw); effObj != nil {
				if changeObj := treeObj(effObj["change"]); changeObj != nil {
					applyPenaltyToChangeTree(changeObj, settings.PenaltyFactor)
				}
			}
		}
	}

	stagesArr := treeArr(tree["stages"])
	for si, stage := range doc.Stages {
		if si >= len(stagesArr) {
			break
		}
		stageObj := treeObj(stagesArr[si])
		if stageObj == nil {
			continue
		}
		opArr := treeArr(stageObj["offer_policy"])
		for oi, op := range stage.OfferPolicy {
			if oi >= len(opArr) {
				break
			}
			opObj := treeObj(opArr[oi])
			issue := issueByID(doc, op.Issue)
			if opObj == nil || issue == nil {
				continue
			}
			if adj, has := settings.Issues[op.Issue]; has && adj.HasLimitShift {
				if stopObj := treeObj(opObj["stop_at"]); stopObj != nil {
					shiftLimitTree(stopObj, issue.Type, issue.Direction, adj.LimitShift)
				}
			}
			if settings.HasUnlockCostFactor {
				if uc, ok := opObj["unlock_cost"].(float64); ok {
					opObj["unlock_cost"] = float64(roundHalfAwayFromZero(uc * settings.UnlockCostFactor))
				}
			}
		}
		if len(settings.ExtraTactics) > 0 && !stage.Terminal {
			if directiveObj := treeObj(stageObj["directive"]); directiveObj != nil {
				tactics := treeArr(directiveObj["tactics"])
				extra := make([]any, len(settings.ExtraTactics))
				for i, t := range settings.ExtraTactics {
					extra[i] = t
				}
				directiveObj["tactics"] = append(append([]any{}, tactics...), extra...)
			}
		}
	}

	if settings.OpponentManner != "" {
		if opponent := treeObj(tree["opponent"]); opponent != nil {
			if brief := treeObj(opponent["brief"]); brief != nil {
				if character, ok := brief["character"].(string); ok {
					brief["character"] = character + " " + settings.OpponentManner
				}
			}
		}
	}
	if len(settings.Hints) > 0 {
		if brief := treeObj(tree["brief"]); brief != nil {
			hints := make([]any, len(settings.Hints))
			for i, h := range settings.Hints {
				hints[i] = h
			}
			brief["hints"] = hints
		}
	}

	factsArr := treeArr(tree["facts"])
	for fi, fact := range doc.Facts {
		if fi >= len(factsArr) {
			break
		}
		factObj := treeObj(factsArr[fi])
		if factObj == nil {
			continue
		}
		if cond, has := settings.FactConditions[fact.ID]; has {
			factObj["reveal_when"] = conditionToJSON(cond)
		}
		if style, has := settings.FactStyles[fact.ID]; has {
			factObj["style"] = string(style)
		}
	}
}

func treeObj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func treeArr(v any) []any {
	a, _ := v.([]any)
	return a
}

func issueValueToJSON(v IssueValue) any {
	switch v.Kind {
	case ValueNumber:
		return v.Number
	case ValueDate:
		return v.Date
	case ValueOption:
		return v.Option
	default:
		return nil
	}
}

func shiftLimitTree(limitObj map[string]any, issueType IssueType, direction Direction, shift float64) {
	shift = directedShift(direction, shift)
	if val, ok := limitObj["value"]; ok {
		limitObj["value"] = shiftJSONValue(val, issueType, shift)
	}
	if byOpt := treeObj(limitObj["by_option"]); byOpt != nil {
		for k, v := range byOpt {
			byOpt[k] = shiftJSONValue(v, issueType, shift)
		}
	}
}

func shiftJSONValue(v any, issueType IssueType, shift float64) any {
	switch issueType {
	case IssueNumber:
		if n, ok := v.(float64); ok {
			return n + shift
		}
	case IssueDate:
		if s, ok := v.(string); ok {
			if t, err := time.Parse("2006-01-02", s); err == nil {
				return t.AddDate(0, 0, int(shift)).Format("2006-01-02")
			}
		}
	}
	return v
}

// directedShift — arena-scenario-format.md, раздел 14: «Плюс — выгоднее
// оппоненту с учётом направления». Знак сдвига в документе всегда значит
// «в чью пользу», а не «в какую сторону по числовой оси»: для
// lower_better выгода оппоненту — это увеличение числа (дороже для
// участника), для higher_better — наоборот, уменьшение (меньше зарплата).
func directedShift(direction Direction, shift float64) float64 {
	if direction == DirectionHigherBetter {
		return -shift
	}
	return shift
}

func applyPenaltyToChangeTree(changeObj map[string]any, factor float64) {
	for _, key := range []string{"trust", "credibility"} {
		if v, ok := changeObj[key].(float64); ok && v < 0 {
			changeObj[key] = float64(roundHalfAwayFromZero(v * factor))
		}
	}
}

func conditionToJSON(c Condition) map[string]any {
	switch c.Kind {
	case ConditionAll, ConditionAny:
		arr := make([]any, len(c.Children))
		for i, ch := range c.Children {
			arr[i] = conditionToJSON(ch)
		}
		key := "all"
		if c.Kind == ConditionAny {
			key = "any"
		}
		return map[string]any{key: arr}
	case ConditionNot:
		var child any
		if c.Child != nil {
			child = conditionToJSON(*c.Child)
		}
		return map[string]any{"not": child}
	case ConditionAlways:
		return map[string]any{"always": true}
	case ConditionMeter:
		return map[string]any{"meter": string(c.Meter), "op": string(c.MeterOp), "value": float64(c.MeterValue)}
	case ConditionFlag:
		return map[string]any{"flag": c.FlagID, "is": c.FlagIs}
	case ConditionFact:
		return map[string]any{"fact": c.FactID, "revealed": c.FactRevealed}
	case ConditionMove:
		m := map[string]any{"move": c.MoveID, "scope": string(c.MoveScope)}
		if c.HasAtLeast {
			m["at_least"] = float64(c.MoveAtLeast)
		}
		return m
	case ConditionArgument:
		arg := map[string]any{}
		if len(c.ArgumentEvidence) > 0 {
			ev := make([]any, len(c.ArgumentEvidence))
			for i, e := range c.ArgumentEvidence {
				ev[i] = e
			}
			arg["evidence"] = ev
		}
		if c.HasArgumentInterest {
			arg["interest"] = c.ArgumentInterest
		}
		if c.HasArgumentConcrete {
			arg["concrete"] = c.ArgumentConcrete
		}
		return map[string]any{"argument": arg, "scope": string(c.ArgumentScope)}
	case ConditionOffer:
		m := map[string]any{"offer": c.OfferIssue, "side": string(c.OfferSide), "op": string(c.OfferOp)}
		if c.HasOfferScope {
			m["scope"] = string(c.OfferScope)
		}
		var val any
		_ = json.Unmarshal(c.OfferValue, &val)
		m["value"] = val
		return m
	case ConditionStage:
		return map[string]any{"stage": c.StageID, "scope": string(c.StageScope)}
	case ConditionDeal:
		return map[string]any{"deal": string(c.Deal)}
	default:
		return map[string]any{}
	}
}

// roundHalfAwayFromZero — округление «половина от нуля» (раздел 6
// arena-scenario-format.md: −7,5 → −8, 7,5 → 8). math.Round в Go уже
// реализует именно это правило для обоих знаков.
func roundHalfAwayFromZero(f float64) int {
	return int(math.Round(f))
}

// applyDifficultyDoc — та же трансформация, что ApplyDifficulty, но над
// уже разобранным Document, а не байтами: используется внутри Validate,
// чтобы прогнать правила 1–10 на документе каждого уровня без повторного
// кодирования в JSON и обратно. Возвращает новый документ; исходный doc
// не изменяется — оба вызова (easy и hard) в Validate стартуют заново от
// одного и того же базового документа.
func applyDifficultyDoc(doc *Document, level Level) *Document {
	clone := *doc
	settings, ok := doc.Difficulty.forLevel(level)
	if !ok {
		return &clone
	}

	if settings.HasPatienceInitial {
		clone.State.Patience.Initial = settings.PatienceInitial
	}
	if settings.HasTrustInitial {
		clone.State.Trust.Initial = settings.TrustInitial
	}

	issueDirections := make(map[string]Direction, len(doc.Issues))
	for _, is := range doc.Issues {
		issueDirections[is.ID] = is.Direction
	}

	newIssues := make([]Issue, len(doc.Issues))
	copy(newIssues, doc.Issues)
	for i := range newIssues {
		if adj, has := settings.Issues[newIssues[i].ID]; has {
			newIssues[i] = applyIssueAdjustment(newIssues[i], adj)
		}
	}
	clone.Issues = newIssues

	if settings.HasPenaltyFactor {
		newEffects := make([]MoveEffect, len(doc.MoveEffects))
		copy(newEffects, doc.MoveEffects)
		for i := range newEffects {
			newEffects[i].Change = applyPenaltyFactor(newEffects[i].Change, settings.PenaltyFactor)
		}
		clone.MoveEffects = newEffects
	}

	newStages := make([]Stage, len(doc.Stages))
	copy(newStages, doc.Stages)
	for i := range newStages {
		newStages[i] = applyStageAdjustment(newStages[i], settings, issueDirections)
	}
	clone.Stages = newStages

	if len(settings.FactConditions) > 0 || len(settings.FactStyles) > 0 {
		newFacts := make([]Fact, len(doc.Facts))
		copy(newFacts, doc.Facts)
		for i := range newFacts {
			if cond, has := settings.FactConditions[newFacts[i].ID]; has {
				newFacts[i].RevealWhen = cond
			}
			if style, has := settings.FactStyles[newFacts[i].ID]; has {
				newFacts[i].Style = style
			}
		}
		clone.Facts = newFacts
	}

	if settings.OpponentManner != "" {
		clone.Opponent.Brief.Character = clone.Opponent.Brief.Character + " " + settings.OpponentManner
	}
	if len(settings.Hints) > 0 {
		clone.Brief.Hints = settings.Hints
	}

	return &clone
}

func applyIssueAdjustment(issue Issue, adj IssueDifficultyAdjust) Issue {
	if adj.HasOpponentStart && adj.OpponentStart != nil {
		issue.Opponent.Start = *adj.OpponentStart
	}
	if adj.HasLimitShift {
		issue.Opponent.Limit = shiftLimit(issue.Opponent.Limit, issue.Direction, adj.LimitShift)
	}
	return issue
}

func shiftLimit(limit Limit, direction Direction, shift float64) Limit {
	shift = directedShift(direction, shift)
	newLimit := Limit{Value: shiftIssueValue(limit.Value, shift)}
	if len(limit.ByOption) > 0 {
		newLimit.ByOption = make(map[string]IssueValue, len(limit.ByOption))
		for k, v := range limit.ByOption {
			newLimit.ByOption[k] = shiftIssueValue(v, shift)
		}
	}
	return newLimit
}

// shiftIssueValue — shift здесь уже направленный (см. directedShift),
// применяется как есть к сырому числу или дате.
func shiftIssueValue(v IssueValue, shift float64) IssueValue {
	switch v.Kind {
	case ValueNumber:
		v.Number += shift
	case ValueDate:
		if t, err := time.Parse("2006-01-02", v.Date); err == nil {
			v.Date = t.AddDate(0, 0, int(shift)).Format("2006-01-02")
		}
	}
	return v
}

func applyPenaltyFactor(c Change, factor float64) Change {
	if c.Trust < 0 {
		c.Trust = roundHalfAwayFromZero(float64(c.Trust) * factor)
	}
	if c.Credibility < 0 {
		c.Credibility = roundHalfAwayFromZero(float64(c.Credibility) * factor)
	}
	return c
}

func applyStageAdjustment(stage Stage, settings LevelSettings, issueDirections map[string]Direction) Stage {
	if len(stage.OfferPolicy) > 0 {
		newOP := make([]OfferPolicy, len(stage.OfferPolicy))
		copy(newOP, stage.OfferPolicy)
		for i := range newOP {
			if adj, has := settings.Issues[newOP[i].Issue]; has && adj.HasLimitShift {
				newOP[i].StopAt = shiftLimit(newOP[i].StopAt, issueDirections[newOP[i].Issue], adj.LimitShift)
			}
			if settings.HasUnlockCostFactor {
				newOP[i].UnlockCost = roundHalfAwayFromZero(float64(newOP[i].UnlockCost) * settings.UnlockCostFactor)
			}
		}
		stage.OfferPolicy = newOP
	}
	if len(settings.ExtraTactics) > 0 && !stage.Terminal {
		stage.Directive.Tactics = append(append([]string{}, stage.Directive.Tactics...), settings.ExtraTactics...)
	}
	return stage
}
