package scenariodoc_test

import (
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

func TestTemplatesArePublishable(t *testing.T) {
	templates := scenariodoc.Templates()
	if len(templates) != 7 {
		t.Fatalf("ожидалось 7 шаблонов (шесть сфер + демо), получено %d", len(templates))
	}
	seen := map[scenariodoc.Sphere]bool{}
	for _, tpl := range templates {
		t.Run(tpl.ID, func(t *testing.T) {
			if tpl.ID == "demo" {
				// демо — не отдельная сфера
			} else if seen[tpl.Sphere] {
				t.Fatalf("сфера %q встречается у второго шаблона", tpl.Sphere)
			} else {
				seen[tpl.Sphere] = true
			}
			diags := scenariodoc.Validate(tpl.Document)
			requireNoErrors(t, diags)
			assertNoBareIdentifiers(t, diags)
		})
	}
}

func TestSkeletonIsPublishable(t *testing.T) {
	data := scenariodoc.Skeleton()
	diags := scenariodoc.Validate(data)
	requireNoErrors(t, diags)
	assertNoBareIdentifiers(t, diags)
}

// TestTemplateEvidenceWeightsFR_SC_12 — FR-SC-12: в «Закупке» вес
// предложения конкурента строго выше внутреннего правила, во «Внутреннем
// повышении» — строго ниже.
func TestTemplateEvidenceWeightsFR_SC_12(t *testing.T) {
	byID := map[string]scenariodoc.Template{}
	for _, tpl := range scenariodoc.Templates() {
		byID[tpl.ID] = tpl
	}

	procurement := decodeWeights(t, byID["procurement"].Document)
	if !(procurement.competitorQuote > procurement.policyRule) {
		t.Fatalf("«Закупка»: вес предложения конкурента (%v) должен быть строго выше внутреннего правила (%v)",
			procurement.competitorQuote, procurement.policyRule)
	}

	promotion := decodeWeights(t, byID["internal_promotion"].Document)
	if !(promotion.competitorQuote < promotion.policyRule) {
		t.Fatalf("«Внутреннее повышение»: вес предложения конкурента (%v) должен быть строго ниже внутреннего правила (%v)",
			promotion.competitorQuote, promotion.policyRule)
	}
}

type weights struct {
	competitorQuote, policyRule float64
}

func decodeWeights(t *testing.T, document []byte) weights {
	t.Helper()
	doc := mustUnmarshal(t, document)
	ew := doc["evidence_weights"].(map[string]any)
	return weights{
		competitorQuote: ew["competitor_quote"].(float64),
		policyRule:      ew["policy_rule"].(float64),
	}
}

// TestInternalPromotionHigherBetter — FR-SC-13: во «Внутреннем повышении»
// предел выше старта принимается (направление higher_better).
func TestInternalPromotionHigherBetter(t *testing.T) {
	var salary map[string]any
	for _, tpl := range scenariodoc.Templates() {
		if tpl.ID != "internal_promotion" {
			continue
		}
		doc := mustUnmarshal(t, tpl.Document)
		for _, raw := range doc["issues"].([]any) {
			is := raw.(map[string]any)
			if is["direction"] == "higher_better" {
				salary = is
			}
		}
	}
	if salary == nil {
		t.Fatal("в шаблоне «Внутреннее повышение» нет предмета с направлением higher_better")
	}
}
