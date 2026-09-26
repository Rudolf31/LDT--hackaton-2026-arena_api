package scenariodoc_test

import (
	"encoding/json"
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

func TestDescribeConditionMeter(t *testing.T) {
	cond := scenariodoc.Condition{Kind: scenariodoc.ConditionMeter, Meter: scenariodoc.MeterTrust, MeterOp: scenariodoc.CompLT, MeterValue: 25}
	got := scenariodoc.DescribeCondition(cond, scenariodoc.LabelDictionary{})
	want := "доверие меньше 25"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDescribeConditionFlag(t *testing.T) {
	dict := scenariodoc.LabelDictionary{Flags: map[string]string{"prepay_offered": "участник предложил полную предоплату"}}
	cond := scenariodoc.Condition{Kind: scenariodoc.ConditionFlag, FlagID: "prepay_offered", FlagIs: true}
	got := scenariodoc.DescribeCondition(cond, dict)
	want := "участник предложил полную предоплату"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDescribeConditionFact(t *testing.T) {
	dict := scenariodoc.LabelDictionary{
		OpponentName: "Сергей",
		Facts:        map[string]string{"revision": "ревизию с другим фланцем"},
	}
	cond := scenariodoc.Condition{Kind: scenariodoc.ConditionFact, FactID: "revision", FactRevealed: true}
	got := scenariodoc.DescribeCondition(cond, dict)
	want := "Сергей рассказал про ревизию с другим фланцем"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDescribeConditionMove(t *testing.T) {
	dict := scenariodoc.LabelDictionary{Moves: map[string]string{"insult": "Грубость"}}
	cond := scenariodoc.Condition{Kind: scenariodoc.ConditionMove, MoveID: "insult", MoveScope: scenariodoc.ScopeEver, MoveAtLeast: 2, HasAtLeast: true}
	got := scenariodoc.DescribeCondition(cond, dict)
	want := "грубость — не меньше двух раз за разговор"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDescribeConditionOffer(t *testing.T) {
	dict := scenariodoc.LabelDictionary{
		Issues:     map[string]string{"price": "Цена за штуку"},
		IssueTypes: map[string]scenariodoc.IssueType{"price": scenariodoc.IssueNumber},
	}
	cond := scenariodoc.Condition{
		Kind: scenariodoc.ConditionOffer, OfferIssue: "price", OfferSide: scenariodoc.OfferParticipant,
		HasOfferScope: true, OfferScope: scenariodoc.OfferScopeThisReply, OfferOp: scenariodoc.CompLTE,
		OfferValue: json.RawMessage(`17000`),
	}
	got := scenariodoc.DescribeCondition(cond, dict)
	want := "участник в этой реплике назвал «Цена за штуку» не больше 17 000"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDescribeConditionStage(t *testing.T) {
	dict := scenariodoc.LabelDictionary{Stages: map[string]string{"offended": "Задет"}}
	cond := scenariodoc.Condition{Kind: scenariodoc.ConditionStage, StageID: "offended", StageScope: scenariodoc.StageVisited}
	got := scenariodoc.DescribeCondition(cond, dict)
	want := "разговор уже был на этапе «Задет»"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDescribeConditionComposite(t *testing.T) {
	dict := scenariodoc.LabelDictionary{
		Flags: map[string]string{"bluff_suspected": "Игорь заподозрил, что участник блефует про конкурента"},
	}
	cond := scenariodoc.Condition{
		Kind: scenariodoc.ConditionAny,
		Children: []scenariodoc.Condition{
			{
				Kind: scenariodoc.ConditionAll,
				Children: []scenariodoc.Condition{
					{Kind: scenariodoc.ConditionMeter, Meter: scenariodoc.MeterPressure, MeterOp: scenariodoc.CompGTE, MeterValue: 60},
					{Kind: scenariodoc.ConditionMeter, Meter: scenariodoc.MeterCredibility, MeterOp: scenariodoc.CompLT, MeterValue: 40},
				},
			},
			{Kind: scenariodoc.ConditionFlag, FlagID: "bluff_suspected", FlagIs: true},
		},
	}
	got := scenariodoc.DescribeCondition(cond, dict)
	want := "давление не меньше 60 и достоверность меньше 40, или Игорь заподозрил, что участник блефует про конкурента"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestConditionCatalogExcludesStage(t *testing.T) {
	catalog := scenariodoc.ConditionCatalog()
	if len(catalog) != 7 {
		t.Fatalf("ожидалось 7 видов узлов в каталоге конструктора, получено %d", len(catalog))
	}
	for _, spec := range catalog {
		if spec.Kind == scenariodoc.ConditionStage {
			t.Fatal("каталог конструктора первой версии не должен показывать узел stage")
		}
		if spec.Label == "" {
			t.Fatalf("у вида %v нет подписи", spec.Kind)
		}
	}
}
