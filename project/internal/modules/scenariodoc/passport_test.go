package scenariodoc_test

import (
	"testing"

	"arena-portal-backend/internal/modules/scenariodoc"
)

func TestPassport(t *testing.T) {
	doc := mustLoadGolden(t)
	p, err := scenariodoc.Passport(mustMarshal(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Поставка кабеля к сроку" {
		t.Fatalf("неверное название: %q", p.Title)
	}
	if p.Sphere != scenariodoc.SphereProcurement {
		t.Fatalf("неверная сфера: %q", p.Sphere)
	}
	if p.NegotiationType != scenariodoc.NegotiationMixed {
		t.Fatalf("неверный тип переговоров: %q", p.NegotiationType)
	}
	if len(p.Tags) != 2 {
		t.Fatalf("неверные теги: %v", p.Tags)
	}
}
