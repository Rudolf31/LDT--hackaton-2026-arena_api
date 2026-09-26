package profiles

import (
	"context"
	"encoding/json"
	"fmt"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
)

// Transport реализует методы gen.StrictServerInterface для
// /api/portal/trainer-profiles/*; cmd/portal/api.go вызывает их по имени
// (D-17). Роль проверяет strict-middleware auth по x-roles контракта;
// какой вид профиля отдавать — решает роль из базы (actor).
type Transport struct {
	service *service
}

func currentActor(ctx context.Context) (actor.Actor, error) {
	a, ok := actor.From(ctx)
	if !ok {
		return actor.Actor{}, httpx.NewError(httpx.KindUnauthenticated, "Войдите в портал.")
	}
	return a, nil
}

func (t *Transport) ListTrainerProfiles(ctx context.Context, request gen.ListTrainerProfilesRequestObject) (gen.ListTrainerProfilesResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	includeArchived := request.Params.IncludeArchived != nil && *request.Params.IncludeArchived
	views, err := t.service.List(ctx, a, includeArchived, request.Params.KeyLast4)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListTrainerProfiles200JSONResponse, 0, len(views))
	for _, v := range views {
		p, err := toProfile(v)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (t *Transport) GetTrainerProfile(ctx context.Context, request gen.GetTrainerProfileRequestObject) (gen.GetTrainerProfileResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	v, err := t.service.Get(ctx, a, request.ProfileId)
	if err != nil {
		return nil, err
	}
	p, err := toProfile(v)
	if err != nil {
		return nil, err
	}
	return gen.GetTrainerProfile200JSONResponse(p), nil
}

func (t *Transport) CreateTrainerProfile(ctx context.Context, request gen.CreateTrainerProfileRequestObject) (gen.CreateTrainerProfileResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := rawSettings(ctx, request.Body.Settings)
	if err != nil {
		return nil, err
	}
	v, err := t.service.Create(ctx, a, request.Body.Name, request.Body.CopyFrom, settings)
	if err != nil {
		return nil, err
	}
	out, err := toAdmin(v.Admin)
	if err != nil {
		return nil, err
	}
	return gen.CreateTrainerProfile201JSONResponse(out), nil
}

func (t *Transport) UpdateTrainerProfile(ctx context.Context, request gen.UpdateTrainerProfileRequestObject) (gen.UpdateTrainerProfileResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := rawSettings(ctx, request.Body.Settings)
	if err != nil {
		return nil, err
	}
	v, err := t.service.Update(ctx, a, request.ProfileId, request.Body.Name, settings)
	if err != nil {
		return nil, err
	}
	out, err := toAdmin(v.Admin)
	if err != nil {
		return nil, err
	}
	return gen.UpdateTrainerProfile200JSONResponse(out), nil
}

func (t *Transport) ReplaceTrainerProfileKeys(ctx context.Context, request gen.ReplaceTrainerProfileKeysRequestObject) (gen.ReplaceTrainerProfileKeysResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	in, err := keysInput(ctx, request.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.service.ReplaceKeys(ctx, a, request.ProfileId, in)
	if err != nil {
		return nil, err
	}
	out, err := toAdmin(v.Admin)
	if err != nil {
		return nil, err
	}
	return gen.ReplaceTrainerProfileKeys200JSONResponse(out), nil
}

func (t *Transport) CheckTrainerProfile(ctx context.Context, request gen.CheckTrainerProfileRequestObject) (gen.CheckTrainerProfileResponseObject, error) {
	checkedAt, lines, err := t.service.Check(ctx, request.ProfileId)
	if err != nil {
		return nil, err
	}
	out := gen.CheckTrainerProfile200JSONResponse{CheckedAt: checkedAt}
	for _, l := range lines {
		row := struct {
			LatencyMs *int                             `json:"latency_ms,omitempty"`
			Message   *string                          `json:"message,omitempty"`
			Ok        bool                             `json:"ok"`
			Route     *gen.ModelRoute                  `json:"route,omitempty"`
			Slow      *bool                            `json:"slow,omitempty"`
			Target    gen.ProfileCheckResultRowsTarget `json:"target"`
		}{LatencyMs: l.LatencyMs, Message: l.Message, Ok: l.OK, Route: l.Route, Slow: l.Slow, Target: l.Target}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

func (t *Transport) ArchiveTrainerProfile(ctx context.Context, request gen.ArchiveTrainerProfileRequestObject) (gen.ArchiveTrainerProfileResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	v, err := t.service.Archive(ctx, a, request.ProfileId)
	if err != nil {
		return nil, err
	}
	out, err := toAdmin(v.Admin)
	if err != nil {
		return nil, err
	}
	return gen.ArchiveTrainerProfile200JSONResponse(out), nil
}

// rawSettings — поле settings так, как оно пришло в теле (D-43): null во
// вложенных блоках значит «выключено», и сгенерированный тип эту разницу
// теряет. nil — поля в теле не было.
func rawSettings(ctx context.Context, typed *gen.TrainerProfileSettings) (json.RawMessage, error) {
	if raw, ok := httpx.RawBody(ctx); ok {
		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, httpx.NewError(httpx.KindInvalidBody, "Тело запроса — не корректный JSON.")
		}
		s, ok := body["settings"]
		if !ok {
			return nil, nil
		}
		return s, nil
	}
	if typed == nil {
		return nil, nil
	}
	b, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("сериализация настроек профиля: %w", err)
	}
	return b, nil
}

// keysInput — тело PUT …/keys: нет поля — не менять, null — удалить (D-43).
func keysInput(ctx context.Context, typed *gen.TrainerProfileKeys) (KeysInput, error) {
	raw, ok := httpx.RawBody(ctx)
	if !ok {
		return KeysInput{
			OpenRouterSet: typed.OpenrouterKey != nil, OpenRouter: typed.OpenrouterKey,
			ModelServerSet: typed.ModelServerToken != nil, ModelServerToken: typed.ModelServerToken,
		}, nil
	}
	var body map[string]*string
	if err := json.Unmarshal(raw, &body); err != nil {
		return KeysInput{}, httpx.NewError(httpx.KindInvalidBody, "Тело запроса — не корректный JSON.")
	}
	var in KeysInput
	in.OpenRouter, in.OpenRouterSet = body["openrouter_key"]
	in.ModelServerToken, in.ModelServerSet = body["model_server_token"]
	return in, nil
}

func toProfile(v view) (gen.TrainerProfile, error) {
	var out gen.TrainerProfile
	if v.Admin != nil {
		a, err := toAdmin(v.Admin)
		if err != nil {
			return out, err
		}
		return out, out.FromTrainerProfileAdmin(a)
	}
	settings, err := ownSettings(v.Public.Settings)
	if err != nil {
		return out, err
	}
	updatedAt := v.Public.UpdatedAt
	return out, out.FromTrainerProfilePublic(gen.TrainerProfilePublic{
		Id: v.Public.ID, Name: v.Public.Name, IsDefault: v.Public.IsDefault, Revision: v.Public.Revision,
		Settings: settings, HasOpenrouterKey: v.Public.HasOpenRouterKey, HasModelServerToken: v.Public.HasModelServerToken,
		ArchivedAt: v.Public.ArchivedAt, UpdatedAt: &updatedAt,
	})
}

func toAdmin(v *adminView) (gen.TrainerProfileAdmin, error) {
	settings, err := ownSettings(v.Settings)
	if err != nil {
		return gen.TrainerProfileAdmin{}, err
	}
	updatedAt := v.UpdatedAt
	out := gen.TrainerProfileAdmin{
		Id: v.ID, Name: v.Name, IsDefault: v.IsDefault, Revision: v.Revision, Settings: settings,
		HasOpenrouterKey: v.HasOpenRouterKey, HasModelServerToken: v.HasModelServerToken,
		OpenrouterKeyLast4: v.OpenRouterKeyLast4, ModelServerTokenLast4: v.ModelServerTokenLast4,
		KeysChangedAt: v.KeysChangedAt, KeysChangedByName: v.KeysChangedByName,
		ArchivedAt: v.ArchivedAt, UpdatedAt: &updatedAt,
		RunningSessions: v.RunningSessions,
	}
	if v.SameKeyProfiles != nil {
		same := v.SameKeyProfiles
		out.SameKeyProfiles = &same
	}
	return out, nil
}

// ownSettings — собственные настройки профиля, без наследования: форма
// администратора показывает, что задано в этом профиле, а пустое поле
// значит «как в профиле по умолчанию».
func ownSettings(raw []byte) (gen.TrainerProfileSettings, error) {
	m, err := parseSettings(raw)
	if err != nil {
		return gen.TrainerProfileSettings{}, err
	}
	return toGen(m)
}
