package consents

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
)

func templates() map[gen.ConsentKind]template {
	out := map[gen.ConsentKind]template{}
	for _, k := range []gen.ConsentKind{
		gen.ConsentKindNoticeTraining, gen.ConsentKindConsentAssessment,
		gen.ConsentKindConsentExternalAi, gen.ConsentKindNoticeDemo,
	} {
		out[k] = template{ID: uuid.New(), Kind: k, Version: "1",
			Body: "Я, {{full_name}} ({{personnel_no}}). Куда: {{model_destinations}} Кто: {{model_providers}}"}
	}
	return out
}

func strp(s string) *string { return &s }

func baseInput() screenInput {
	return screenInput{
		Mode:               gen.ModeTraining,
		OpenRouterURL:      "https://openrouter.ai/api/v1",
		OwnServerURL:       "https://models.example.org/v1",
		ModelProvidersNote: "Qwen — Alibaba Cloud (Китай)",
		FullName:           strp("Анна Соколова"),
		PersonnelNo:        strp("DEMO-001"),
		WrittenConsentOK:   true,
		Templates:          templates(),
	}
}

func TestVariantFollowsProfileRoutes(t *testing.T) {
	cases := []struct {
		name       string
		openRouter string
		own        string
		want       gen.ConsentScreenVariant
		externalAI bool
	}{
		{"только OpenRouter", "https://openrouter.ai/api/v1", "", gen.ConsentScreenVariantA, true},
		{"только наш сервер", "", "https://models.example.org/v1", gen.ConsentScreenVariantB, false},
		{"оба", "https://openrouter.ai/api/v1", "https://models.example.org/v1", gen.ConsentScreenVariantV, true},
	}
	for _, c := range cases {
		in := baseInput()
		in.OpenRouterURL, in.OwnServerURL = c.openRouter, c.own
		s, err := buildScreen(in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Variant != c.want {
			t.Fatalf("%s: вариант %s, ждали %s", c.name, s.Variant, c.want)
		}
		hasExternal := false
		for _, tx := range s.Texts {
			if tx.Kind == gen.ConsentKindConsentExternalAi {
				hasExternal = true
			}
		}
		if hasExternal != c.externalAI {
			t.Fatalf("%s: вопрос про внешнюю нейросеть показан = %v", c.name, hasExternal)
		}
		if !s.CanProceed {
			t.Fatalf("%s: неожиданная блокировка: %s", c.name, *s.BlockedReason)
		}
	}
}

func TestWithdrawnExternalAIHidesQuestion(t *testing.T) {
	in := baseInput()
	in.ExternalAIWithdrawn = true
	s, err := buildScreen(in)
	if err != nil {
		t.Fatal(err)
	}
	if s.ExternalAIOffered || len(s.Texts) != 1 || s.Variant != gen.ConsentScreenVariantB {
		t.Fatalf("после отзыва согласия на внешнюю нейросеть вопроса быть не должно: %+v", s)
	}
	if strings.Contains(s.Texts[0].Body, "openrouter") {
		t.Fatal("в тексте не должно остаться адреса OpenRouter")
	}
}

func TestMainKindByMode(t *testing.T) {
	for _, c := range []struct {
		mode gen.Mode
		demo bool
		want gen.ConsentKind
	}{
		{gen.ModeTraining, false, gen.ConsentKindNoticeTraining},
		{gen.ModeAssessment, false, gen.ConsentKindConsentAssessment},
		{gen.ModeTraining, true, gen.ConsentKindNoticeDemo},
	} {
		in := baseInput()
		in.Mode, in.Demo = c.mode, c.demo
		s, err := buildScreen(in)
		if err != nil {
			t.Fatal(err)
		}
		if s.Texts[0].Kind != c.want {
			t.Fatalf("режим %s демо %v: основной текст %s, ждали %s", c.mode, c.demo, s.Texts[0].Kind, c.want)
		}
	}
}

func TestSubstitutionsAndHashChangeWithModelAddress(t *testing.T) {
	in := baseInput()
	first, err := buildScreen(in)
	if err != nil {
		t.Fatal(err)
	}
	body := first.Texts[0].Body
	if strings.Contains(body, "{{") || !strings.Contains(body, "Анна Соколова") || !strings.Contains(body, "DEMO-001") ||
		!strings.Contains(body, "https://models.example.org/v1") {
		t.Fatalf("подстановки не сделаны: %s", body)
	}
	if first.Texts[0].SHA256 != sha256Hex(body) {
		t.Fatal("SHA-256 должен считаться от текста с подстановками")
	}

	in.OwnServerURL = "https://other.example.org/v1"
	second, err := buildScreen(in)
	if err != nil {
		t.Fatal(err)
	}
	if second.Texts[0].SHA256 == first.Texts[0].SHA256 {
		t.Fatal("FR-AC-13: смена адреса моделей должна менять текст")
	}
}

func TestBlockedReasons(t *testing.T) {
	cases := []struct {
		name   string
		change func(*screenInput)
	}{
		{"нет маршрута", func(in *screenInput) { in.OpenRouterURL, in.OwnServerURL = "", "" }},
		{"оценка без ФИО", func(in *screenInput) {
			in.Mode = gen.ModeAssessment
			in.FullName = nil
			in.Pseudonym = strp("Сотрудник-4")
		}},
		{"оценка без письменного согласия", func(in *screenInput) { in.Mode = gen.ModeAssessment; in.WrittenConsentOK = false }},
		{"нет разработчиков моделей", func(in *screenInput) { in.ModelProvidersNote = "" }},
	}
	for _, c := range cases {
		in := baseInput()
		c.change(&in)
		s, err := buildScreen(in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.CanProceed || s.BlockedReason == nil || *s.BlockedReason == "" {
			t.Fatalf("%s: ожидалась блокировка с причиной", c.name)
		}
	}
}

func TestMissingTemplateIsError(t *testing.T) {
	in := baseInput()
	delete(in.Templates, gen.ConsentKindNoticeTraining)
	if _, err := buildScreen(in); err == nil {
		t.Fatal("без текста вида экран собираться не должен")
	}
}

func TestAnswersByKind(t *testing.T) {
	if got := answersFor(gen.ConsentKindNoticeTraining); len(got) != 1 || got[0] != gen.Acknowledged {
		t.Fatalf("уведомление: %v", got)
	}
	if got := answersFor(gen.ConsentKindConsentExternalAi); len(got) != 2 {
		t.Fatalf("согласие: %v", got)
	}
}
