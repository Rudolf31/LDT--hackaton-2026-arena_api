package scenariodoc_test

import (
	"encoding/json"
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

func TestFingerprintStable(t *testing.T) {
	doc := mustLoadGolden(t)
	data := mustMarshal(t, doc)
	a, err := scenariodoc.Fingerprint(data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := scenariodoc.Fingerprint(data)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("отпечаток одного и того же документа различается: %s != %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("отпечаток должен быть 64 шестнадцатеричными знаками, получено %d: %s", len(a), a)
	}
}

func TestFingerprintIgnoresTagsOriginAuthoringVersion(t *testing.T) {
	doc := mustLoadGolden(t)
	base, err := scenariodoc.Fingerprint(mustMarshal(t, doc))
	if err != nil {
		t.Fatal(err)
	}

	passport := doc["passport"].(map[string]any)
	passport["tags"] = []any{"другой", "набор", "тегов"}
	passport["origin"] = "template"
	passport["version"] = 7.0
	doc["authoring"] = map[string]any{"source_template": "procurement", "brief": "Совсем другое описание.", "notes": "Другие заметки."}

	after, err := scenariodoc.Fingerprint(mustMarshal(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if base != after {
		t.Fatalf("отпечаток изменился от tags/origin/version/authoring: %s != %s", base, after)
	}
}

func TestFingerprintChangesWithContent(t *testing.T) {
	doc := mustLoadGolden(t)
	base, err := scenariodoc.Fingerprint(mustMarshal(t, doc))
	if err != nil {
		t.Fatal(err)
	}

	issueObj(t, doc, "price")["opponent"].(map[string]any)["start"] = 231.0

	after, err := scenariodoc.Fingerprint(mustMarshal(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if base == after {
		t.Fatal("отпечаток не изменился от правки содержания разговора")
	}
}

func TestFingerprintRoundTrip(t *testing.T) {
	doc := mustLoadGolden(t)
	data := mustMarshal(t, doc)
	before, err := scenariodoc.Fingerprint(data)
	if err != nil {
		t.Fatal(err)
	}

	var reimported map[string]any
	if err := json.Unmarshal(data, &reimported); err != nil {
		t.Fatal(err)
	}
	after, err := scenariodoc.Fingerprint(mustMarshal(t, reimported))
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("экспорт и повторный импорт дают разные отпечатки: %s != %s", before, after)
	}
}

func TestFingerprintIgnoresNumberFormatting(t *testing.T) {
	docA := map[string]any{"passport": map[string]any{"id": "x"}, "value": 1.0}
	docB := map[string]any{"passport": map[string]any{"id": "x"}, "value": 1}
	a, err := scenariodoc.Fingerprint(mustMarshal(t, docA))
	if err != nil {
		t.Fatal(err)
	}
	b, err := scenariodoc.Fingerprint(mustMarshal(t, docB))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("отпечаток должен различать 1.0 и 1 одинаково (JCS): %s != %s", a, b)
	}
}
