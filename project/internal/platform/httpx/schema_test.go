package httpx

import "testing"

func TestSpecificity(t *testing.T) {
	cases := []struct {
		pattern string
		want    int
	}{
		{"/api/portal/things/special", 5},
		{"/api/portal/things/{id}", 4},
		{"/{a}/{b}", 1},
	}
	for _, c := range cases {
		if got := specificity(c.pattern); got != c.want {
			t.Fatalf("specificity(%q) = %d, ожидалось %d", c.pattern, got, c.want)
		}
	}
}

// testAmbiguousSpecYAML — два пути одной глубины и метода, один с литеральным
// сегментом, другой с {id}: match() должен всегда предпочитать литеральный,
// независимо от порядка обхода map при разборе paths (I-2, находка ревью).
const testAmbiguousSpecYAML = `
openapi: 3.1.0
info: {title: test, version: "0"}
paths:
  /things/special:
    get:
      operationId: thingsSpecial
  /things/{id}:
    get:
      operationId: thingsByID
`

func TestMatchPrefersLiteralSegmentOverParam(t *testing.T) {
	schemas, err := LoadBodySchemas([]byte(testAmbiguousSpecYAML), 1024, nil)
	if err != nil {
		t.Fatalf("LoadBodySchemas: %v", err)
	}

	op, matched := schemas.match("GET", "/things/special")
	if !matched {
		t.Fatal("ожидалось совпадение для /things/special")
	}
	if op.operationID != "thingsSpecial" {
		t.Fatalf("ожидалась операция thingsSpecial, получена %s (литеральный сегмент должен быть приоритетнее {id})", op.operationID)
	}

	op, matched = schemas.match("GET", "/things/anything-else")
	if !matched {
		t.Fatal("ожидалось совпадение для /things/anything-else через {id}")
	}
	if op.operationID != "thingsByID" {
		t.Fatalf("ожидалась операция thingsByID, получена %s", op.operationID)
	}
}
