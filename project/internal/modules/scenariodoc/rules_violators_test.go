package scenariodoc_test

import (
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

// TestGoldenIsPublishable — golden-сценарий проходит проверку без
// блокирующих ошибок на всех трёх уровнях сложности (единственные
// оставшиеся диагностики — некритичные предупреждения).
func TestGoldenIsPublishable(t *testing.T) {
	doc := mustLoadGolden(t)
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireNoErrors(t, diags)
	assertNoBareIdentifiers(t, diags)
}

func TestRule1DealZoneEmpty(t *testing.T) {
	doc := mustLoadGolden(t)
	price := issueObj(t, doc, "price")
	limit := price["opponent"].(map[string]any)["limit"].(map[string]any)
	limit["value"] = 400.0
	limit["by_option"] = map[string]any{"prepay_100": 400.0}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "1")
}

func TestRule1ChoiceZoneEmpty(t *testing.T) {
	doc := mustLoadGolden(t)
	payment := issueObj(t, doc, "payment")
	payment["opponent"].(map[string]any)["acceptable"] = []any{"prepay_100"}
	payment["opponent"].(map[string]any)["start"] = "prepay_100"
	payment["participant"].(map[string]any)["acceptable"] = []any{"deferral_30"}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "1")
}

func TestRule1aStartBeyondLimit(t *testing.T) {
	doc := mustLoadGolden(t)
	price := issueObj(t, doc, "price")
	price["opponent"].(map[string]any)["start"] = 100.0
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "1a")
}

func TestRule1aFirstAcceptableMismatch(t *testing.T) {
	doc := mustLoadGolden(t)
	payment := issueObj(t, doc, "payment")
	payment["opponent"].(map[string]any)["acceptable"] = []any{"deferral_30", "prepay_100"}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "1a")
}

func TestRule2Unreachable(t *testing.T) {
	doc := mustLoadGolden(t)
	for _, raw := range doc["stages"].([]any) {
		stage := raw.(map[string]any)
		if stage["id"] == "trade_prepay" {
			continue
		}
		trs, ok := stage["transitions"].([]any)
		if !ok {
			continue
		}
		for _, traw := range trs {
			tr := traw.(map[string]any)
			if tr["to"] == "trade_prepay" {
				tr["to"] = "bargaining"
			}
		}
	}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "2")
}

func TestRule3DeadEnd(t *testing.T) {
	doc := mustLoadGolden(t)
	offended := stageObj(t, doc, "offended")
	delete(offended, "transitions")
	delete(offended, "timeout")
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "3")
}

func TestRule3EndStagesSame(t *testing.T) {
	doc := mustLoadGolden(t)
	doc["end_stages"].(map[string]any)["no_deal"] = "deal"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "3")
}

func TestRule4LastFinalNotAlways(t *testing.T) {
	doc := mustLoadGolden(t)
	finalObj(t, doc, "ok_deal")["when"] = map[string]any{"deal": "agreed"}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "4")
}

func TestRule4FinalAfterAlways(t *testing.T) {
	doc := mustLoadGolden(t)
	finals := doc["finals"].([]any)
	extra := map[string]any{
		"id": "impossible", "rank": "A", "title": "Недостижимый финал",
		"when":     map[string]any{"stage": "opening", "scope": "visited"},
		"epilogue": "Этот финал никогда не проверяется.",
	}
	doc["finals"] = append(finals, extra)
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "4")
}

func TestRule5OpponentStepBeyondLimit(t *testing.T) {
	doc := mustLoadGolden(t)
	op := offerPolicyObj(t, doc, "bargaining", "price")
	op["stop_at"] = map[string]any{"value": 100.0, "by_option": map[string]any{"prepay_100": 90.0}}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "5")
}

func TestRule6UnknownStageReference(t *testing.T) {
	doc := mustLoadGolden(t)
	stage := stageObj(t, doc, "opening")
	trs := stage["transitions"].([]any)
	trs[0].(map[string]any)["to"] = "closing_nonexistent"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "6")
}

func TestRule6DuplicateStageID(t *testing.T) {
	doc := mustLoadGolden(t)
	stages := doc["stages"].([]any)
	stages[1].(map[string]any)["id"] = stages[0].(map[string]any)["id"]
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "6")
}

func TestRule7TooDeepCondition(t *testing.T) {
	doc := mustLoadGolden(t)
	tooDeep := map[string]any{
		"any": []any{
			map[string]any{"all": []any{
				map[string]any{"any": []any{
					map[string]any{"flag": "prepay_offered", "is": true},
					map[string]any{"flag": "bluff_suspected", "is": true},
				}},
				map[string]any{"meter": "trust", "op": "gte", "value": 10.0},
			}},
			map[string]any{"always": true},
		},
	}
	finalObj(t, doc, "ok_deal")["when"] = tooDeep
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "7")
}

func TestRule7ChoiceWrongComparison(t *testing.T) {
	doc := mustLoadGolden(t)
	stage := stageObj(t, doc, "opening")
	trs := stage["transitions"].([]any)
	trs[1].(map[string]any)["when"] = map[string]any{
		"offer": "payment", "side": "participant", "scope": "this_reply", "op": "lt", "value": "prepay_100",
	}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "7")
}

func TestRule8TooFewFinals(t *testing.T) {
	doc := mustLoadGolden(t)
	finals := doc["finals"].([]any)
	var kept []any
	for _, f := range finals {
		if f.(map[string]any)["id"] == "no_deal" {
			continue
		}
		kept = append(kept, f)
	}
	doc["finals"] = kept
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "8")
}

func TestRule9TooFewExamples(t *testing.T) {
	doc := mustLoadGolden(t)
	cm := doc["custom_moves"].([]any)[0].(map[string]any)
	cm["examples"] = []any{"Пример один?", "Пример два?"}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "9")
}

func TestRule9NameCollidesWithBase(t *testing.T) {
	doc := mustLoadGolden(t)
	cm := doc["custom_moves"].([]any)[0].(map[string]any)
	cm["id"] = "probe_problem"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "9")
}

func TestRule10NoHiddenFact(t *testing.T) {
	doc := mustLoadGolden(t)
	factObj(t, doc, "warehouse_shortage")["reveal_when"] = map[string]any{"always": true}
	factObj(t, doc, "warehouse_shortage")["style"] = "volunteers"
	factObj(t, doc, "cash_gap")["reveal_when"] = map[string]any{"always": true}
	factObj(t, doc, "cash_gap")["style"] = "volunteers"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "10")
}

func TestRule10StyleWithoutMove(t *testing.T) {
	doc := mustLoadGolden(t)
	factObj(t, doc, "cash_gap")["style"] = "reluctant"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "10")
}

func TestRule10NeverRevealable(t *testing.T) {
	doc := mustLoadGolden(t)
	for _, raw := range doc["stages"].([]any) {
		stage := raw.(map[string]any)
		mayReveal, ok := stage["may_reveal"].([]any)
		if !ok {
			continue
		}
		var kept []any
		for _, f := range mayReveal {
			if f == "cash_gap" {
				continue
			}
			kept = append(kept, f)
		}
		stage["may_reveal"] = kept
	}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "10")
}

func TestRule12MissingStopAt(t *testing.T) {
	doc := mustLoadGolden(t)
	op := offerPolicyObj(t, doc, "bargaining", "price")
	delete(op, "stop_at")
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "12")
}

func TestRule12DuplicateIssueInStage(t *testing.T) {
	doc := mustLoadGolden(t)
	stage := stageObj(t, doc, "bargaining")
	op := stage["offer_policy"].([]any)
	dup := map[string]any{}
	for k, v := range op[0].(map[string]any) {
		dup[k] = v
	}
	stage["offer_policy"] = append(op, dup)
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "12")
}

func TestRule13PressureGivesCredit(t *testing.T) {
	doc := mustLoadGolden(t)
	me := moveEffectObj(t, doc, "insult_penalty")
	me["change"].(map[string]any)["credit"] = 10.0
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "13")
}

func TestRule13MovelessWithoutThisReplyCondition(t *testing.T) {
	doc := mustLoadGolden(t)
	me := moveEffectObj(t, doc, "prepay_trade")
	me["if"] = map[string]any{"meter": "trust", "op": "gte", "value": 10.0}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "13")
}

func TestRule13NoneWeightNotZero(t *testing.T) {
	doc := mustLoadGolden(t)
	doc["evidence_weights"].(map[string]any)["none"] = 0.1
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "13")
}

func TestRule14InterestWeightTooLow(t *testing.T) {
	doc := mustLoadGolden(t)
	interests := doc["opponent"].(map[string]any)["brief"].(map[string]any)["interests"].([]any)
	interests[1].(map[string]any)["weight"] = 0.2
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "14")
}

func TestRule15InitialOutOfBounds(t *testing.T) {
	doc := mustLoadGolden(t)
	pressure := doc["state"].(map[string]any)["pressure"].(map[string]any)
	pressure["initial"] = 95.0
	pressure["max"] = 90.0
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "15")
}

func TestRule15JailbreakEndTooEarly(t *testing.T) {
	doc := mustLoadGolden(t)
	doc["jailbreak"].(map[string]any)["end_after_attempts"] = 3.0
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "15")
}

func TestRule15EscalationStageTerminal(t *testing.T) {
	doc := mustLoadGolden(t)
	doc["jailbreak"].(map[string]any)["escalation_stage"] = "deal"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "15")
}

func TestSchemaUnknownField(t *testing.T) {
	doc := mustLoadGolden(t)
	doc["priority"] = "high"
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	requireDiagnostic(t, diags, "schema")
}

func TestSchemaChoiceOnlyEqualityComparison(t *testing.T) {
	doc := mustLoadGolden(t)
	stage := stageObj(t, doc, "opening")
	trs := stage["transitions"].([]any)
	trs[1].(map[string]any)["when"] = map[string]any{
		"offer": "payment", "side": "opponent", "op": "lt", "value": "prepay_100",
	}
	diags := scenariodoc.Validate(mustMarshal(t, doc))
	// side=opponent без scope — правило 7; op=lt на choice поймает либо 7
	// (сравнение), либо схема, в зависимости от узла — здесь важно, что
	// публикация блокируется хоть каким-то содержательным сообщением.
	found := false
	for _, d := range diags {
		if d.Severity == scenariodoc.SeverityError && (d.Rule == "7" || d.Rule == "schema") {
			found = true
		}
	}
	if !found {
		t.Fatalf("ожидалась блокирующая ошибка, диагностики: %+v", diags)
	}
}
