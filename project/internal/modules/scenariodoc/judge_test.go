package scenariodoc_test

import (
	"strings"
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

func TestJudgeContextOfDemo(t *testing.T) {
	var demo []byte
	for _, tpl := range scenariodoc.Templates() {
		if tpl.ID == "demo" {
			demo = tpl.Document
		}
	}
	jc, err := scenariodoc.JudgeContextOf(demo)
	if err != nil {
		t.Fatal(err)
	}
	if jc.ProcessCriteria != "harvard_spin_v1" {
		t.Fatalf("набор критериев: %q", jc.ProcessCriteria)
	}
	if f, ok := jc.Finals["beyond_limits"]; !ok || f.Title == "" || f.Epilogue == "" {
		t.Fatalf("финал beyond_limits без названия или эпилога: %+v", f)
	}
	if jc.FactTexts["minor_scratch"] == "" {
		t.Fatal("нет текста факта minor_scratch")
	}
	if !strings.Contains(string(jc.Interests), "sell_today") {
		t.Fatalf("интересы оппонента: %s", jc.Interests)
	}
	// Пределов оппонента и этапов в выборке нет по построению: проверяем,
	// что их не протащил бриф.
	if strings.Contains(string(jc.Brief), "\"limit\"") || strings.Contains(string(jc.Brief), "\"stages\"") {
		t.Fatalf("в брифе участника оказались пределы или этапы: %s", jc.Brief)
	}
}
