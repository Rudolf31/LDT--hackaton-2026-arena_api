package profiles

import (
	"encoding/json"
	"fmt"
	"strings"

	"arena-portal-backend/internal/api/gen"
)

// Настройки профиля хранятся так, как их прислал администратор: форма уже
// проверена закрытой схемой контракта (TrainerProfileSettings), а разница
// между «поля нет» и «поле равно null» для слияния важна, и сгенерированный
// тип её теряет (D-43). Поэтому здесь — map[string]any, а в
// gen.TrainerProfileSettings они превращаются только на выходе.

// parseSettings — настройки из jsonb или тела запроса. Пусто — пустой объект.
func parseSettings(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("разбор настроек профиля: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// merge — итоговые настройки профиля: пустые поля берутся из профиля по
// умолчанию (UC-A-04). Вложенные объекты сливаются по полям; значение
// null в своём профиле выключает то, что было бы унаследовано (например,
// `openrouter: null` — у этого профиля нет маршрута через OpenRouter), и в
// итоге такого поля нет вовсе. Входные карты не меняются.
func merge(base, own map[string]any) map[string]any {
	out := deepCopy(base)
	for k, v := range own {
		if v == nil {
			delete(out, k)
			continue
		}
		ownMap, ownIsMap := v.(map[string]any)
		baseMap, baseIsMap := out[k].(map[string]any)
		if ownIsMap && baseIsMap {
			out[k] = merge(baseMap, ownMap)
			continue
		}
		if ownIsMap {
			out[k] = merge(map[string]any{}, ownMap)
			continue
		}
		out[k] = v
	}
	return out
}

func deepCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if inner, ok := v.(map[string]any); ok {
			out[k] = deepCopy(inner)
			continue
		}
		if v == nil {
			continue
		}
		out[k] = v
	}
	return out
}

// modelRoles — три модели разговора в порядке экрана; названия — в
// родительном падеже для текстов ошибок.
var modelRoles = []struct {
	key   string
	title string
}{
	{"opponent", "оппонента"},
	{"participant_judge", "судьи участника"},
	{"opponent_judge", "судьи оппонента"},
}

// validateEffective — проверка UC-A-01 по итоговым настройкам. Окно
// контекста против лимита ходов не сверяется — достаточно ли его, решает
// администратор (D-42); проверяется, что у каждой из трёх моделей задана
// модель, окно и длина ответа (NFR-C-01: max_tokens обязателен у каждого
// вызова), и что основной маршрут есть в профиле.
func validateEffective(eff map[string]any) []gen.FieldError {
	var errs []gen.FieldError
	models, _ := eff["models"].(map[string]any)
	for _, role := range modelRoles {
		path := "/settings/models/" + role.key
		m, ok := models[role.key].(map[string]any)
		if !ok {
			errs = append(errs, gen.FieldError{Path: path,
				Message: "Не задана модель " + role.title + " — ни в этом профиле, ни в профиле по умолчанию."})
			continue
		}
		if s, _ := m["model"].(string); strings.TrimSpace(s) == "" {
			errs = append(errs, gen.FieldError{Path: path + "/model", Message: "Не указано название модели " + role.title + "."})
		}
		if n, _ := m["max_tokens"].(float64); n < 1 {
			errs = append(errs, gen.FieldError{Path: path + "/max_tokens", Message: "Не задана длина ответа модели " + role.title + "."})
		}
		if n, _ := m["context_window"].(float64); n < 1 {
			errs = append(errs, gen.FieldError{Path: path + "/context_window", Message: "Не задано окно контекста модели " + role.title + "."})
		}
	}

	r := routesOf(eff)
	switch eff["primary_route"] {
	case string(gen.TrainerProfileSettingsPrimaryRouteOpenrouter):
		if !r.OpenRouter {
			errs = append(errs, gen.FieldError{Path: "/settings/primary_route",
				Message: "Основной маршрут — OpenRouter, но адрес OpenRouter в профиле не задан."})
		}
	case string(gen.TrainerProfileSettingsPrimaryRouteOwnServer):
		if !r.OwnServer {
			errs = append(errs, gen.FieldError{Path: "/settings/primary_route",
				Message: "Основной маршрут — наш сервер моделей, но его адрес в профиле не задан."})
		}
	}
	return errs
}

// routesOf — какие маршруты к моделям заданы: есть адрес — есть маршрут.
func routesOf(eff map[string]any) Routes {
	return Routes{
		OpenRouter: hasBaseURL(eff, "openrouter"),
		OwnServer:  hasBaseURL(eff, "model_server"),
	}
}

func hasBaseURL(eff map[string]any, key string) bool {
	block, ok := eff[key].(map[string]any)
	if !ok {
		return false
	}
	url, _ := block["base_url"].(string)
	return strings.TrimSpace(url) != ""
}

// stripOpenRouter — настройки для участника без согласия на внешнюю
// нейросеть: ни адреса OpenRouter, ни указания на него как на основной
// маршрут (FR-AC-07: «без согласия в настройках клиента нет адреса
// внешнего провайдера»).
func stripOpenRouter(eff map[string]any) map[string]any {
	out := deepCopy(eff)
	delete(out, "openrouter")
	if out["primary_route"] == string(gen.TrainerProfileSettingsPrimaryRouteOpenrouter) {
		if hasBaseURL(out, "model_server") {
			out["primary_route"] = string(gen.TrainerProfileSettingsPrimaryRouteOwnServer)
		} else {
			delete(out, "primary_route")
		}
	}
	return out
}

// toGen — настройки в сгенерированном типе ответа. Схема закрыта, поэтому
// неизвестных полей здесь быть не может; ошибка — только от испорченного
// jsonb.
func toGen(m map[string]any) (gen.TrainerProfileSettings, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return gen.TrainerProfileSettings{}, fmt.Errorf("сериализация настроек профиля: %w", err)
	}
	var out gen.TrainerProfileSettings
	if err := json.Unmarshal(raw, &out); err != nil {
		return gen.TrainerProfileSettings{}, fmt.Errorf("настройки профиля не подходят под схему: %w", err)
	}
	return out, nil
}

// last4 — последние четыре символа ключа: единственное, что о ключе
// видит администратор (FR-PF-04). Символы, а не байты — чтобы CHECK
// char_length = 4 в базе совпадал.
func last4(key string) string {
	r := []rune(key)
	return string(r[len(r)-4:])
}
