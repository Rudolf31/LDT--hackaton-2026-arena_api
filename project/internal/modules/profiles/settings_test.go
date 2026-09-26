package profiles

import (
	"testing"
)

func mustParse(t *testing.T, s string) map[string]any {
	t.Helper()
	m, err := parseSettings([]byte(s))
	if err != nil {
		t.Fatalf("parseSettings: %v", err)
	}
	return m
}

const completeSettings = `{
	"openrouter": {"base_url": "https://openrouter.ai/api/v1"},
	"model_server": {"base_url": "https://models.example.org/v1"},
	"primary_route": "openrouter",
	"models": {
		"opponent": {"model": "qwen/qwen3-32b", "context_window": 32768, "max_tokens": 400},
		"participant_judge": {"model": "qwen/qwen3-32b", "context_window": 32768, "max_tokens": 800},
		"opponent_judge": {"model": "qwen/qwen3-32b", "context_window": 32768, "max_tokens": 800}
	},
	"camera": {"enabled": true, "emotion_labels_to_opponent": false},
	"limits": {"max_turns": 30, "session_minutes": 20}
}`

func TestMergeInheritsMissingFieldsFromDefault(t *testing.T) {
	base := mustParse(t, completeSettings)
	own := mustParse(t, `{"models": {"opponent": {"model": "other", "context_window": 8192, "max_tokens": 200}}, "limits": {"max_turns": 10}}`)

	eff := merge(base, own)

	models := eff["models"].(map[string]any)
	if models["opponent"].(map[string]any)["model"] != "other" {
		t.Fatal("своя модель оппонента должна перекрыть модель по умолчанию")
	}
	if models["participant_judge"] == nil {
		t.Fatal("модель судьи участника должна унаследоваться")
	}
	limits := eff["limits"].(map[string]any)
	if limits["max_turns"] != float64(10) || limits["session_minutes"] != float64(20) {
		t.Fatalf("лимиты слиты неверно: %v", limits)
	}
	if base["limits"].(map[string]any)["max_turns"] != float64(30) {
		t.Fatal("merge не должен менять профиль по умолчанию")
	}
}

func TestMergeNullDisablesInheritedRoute(t *testing.T) {
	eff := merge(mustParse(t, completeSettings), mustParse(t, `{"openrouter": null, "primary_route": "own_server"}`))
	if _, ok := eff["openrouter"]; ok {
		t.Fatal("openrouter: null должен выключить унаследованный маршрут")
	}
	r := routesOf(eff)
	if r.OpenRouter || !r.OwnServer {
		t.Fatalf("маршруты: %+v", r)
	}
	if errs := validateEffective(eff); len(errs) != 0 {
		t.Fatalf("ожидались настройки без ошибок, получили %v", errs)
	}
}

func TestValidateEffectiveRequiresModelsAndAnswerLength(t *testing.T) {
	eff := mustParse(t, `{"models": {"opponent": {"model": "x", "context_window": 8192}}}`)
	errs := validateEffective(eff)
	paths := map[string]bool{}
	for _, e := range errs {
		paths[e.Path] = true
		if e.Message == "" {
			t.Fatalf("пустая фраза у %s", e.Path)
		}
	}
	for _, want := range []string{
		"/settings/models/opponent/max_tokens",
		"/settings/models/participant_judge",
		"/settings/models/opponent_judge",
	} {
		if !paths[want] {
			t.Fatalf("нет ошибки по %s; получили %v", want, errs)
		}
	}
}

func TestValidateEffectivePrimaryRouteMustExist(t *testing.T) {
	eff := merge(mustParse(t, completeSettings), mustParse(t, `{"openrouter": null}`))
	errs := validateEffective(eff)
	if len(errs) != 1 || errs[0].Path != "/settings/primary_route" {
		t.Fatalf("ожидалась ошибка основного маршрута, получили %v", errs)
	}
}

func TestStripOpenRouter(t *testing.T) {
	eff := mustParse(t, completeSettings)
	out := stripOpenRouter(eff)
	if _, ok := out["openrouter"]; ok {
		t.Fatal("блок openrouter должен исчезнуть")
	}
	if out["primary_route"] != "own_server" {
		t.Fatalf("основной маршрут должен стать нашим сервером, получили %v", out["primary_route"])
	}
	if _, ok := eff["openrouter"]; !ok {
		t.Fatal("stripOpenRouter не должен менять исходные настройки")
	}

	noOwn := stripOpenRouter(mustParse(t, `{"openrouter": {"base_url": "https://openrouter.ai/api/v1"}, "primary_route": "openrouter"}`))
	if _, ok := noOwn["primary_route"]; ok {
		t.Fatal("без нашего сервера основного маршрута не остаётся")
	}
}

func TestEmotionLabelsFieldSurvivesSettingsRoundTrip(t *testing.T) {
	settings, err := toGen(mustParse(t, completeSettings))
	if err != nil {
		t.Fatalf("toGen: %v", err)
	}
	if settings.Camera == nil || settings.Camera.EmotionLabelsToOpponent == nil {
		t.Fatal("camera.emotion_labels_to_opponent — законное поле профиля (FR-RS-02) и должно сохраняться")
	}
}

func TestLast4CountsCharacters(t *testing.T) {
	if got := last4("sk-or-v1-abcdЖ9f2"); got != "Ж9f2" {
		t.Fatalf("last4 = %q", got)
	}
}
