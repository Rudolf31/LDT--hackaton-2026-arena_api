package settings

import (
	"context"
	"errors"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
)

// Transport реализует ровно те методы gen.StrictServerInterface, что
// относятся к settings; cmd/portal/api.go вызывает их по имени (D-17).
type Transport struct {
	service Service
}

func newTransport(service Service) *Transport {
	return &Transport{service: service}
}

// Service — служба settings для других модулей (scenarios читает порог
// допуска к оценке, D-26/12.5). New возвращает только *Transport, поэтому
// зависимость достаётся через этот метод, а не отдельным полем сборки.
func (t *Transport) Service() Service { return t.service }

func (t *Transport) GetPortalSettings(ctx context.Context, _ gen.GetPortalSettingsRequestObject) (gen.GetPortalSettingsResponseObject, error) {
	result, err := t.service.Get(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetPortalSettings200JSONResponse(toGenSettings(result)), nil
}

func (t *Transport) UpdatePortalSettings(ctx context.Context, request gen.UpdatePortalSettingsRequestObject) (gen.UpdatePortalSettingsResponseObject, error) {
	if request.Body == nil {
		return nil, httpx.NewError(httpx.KindInvalidBody, "В запросе должно быть тело JSON.")
	}

	patch := Patch{
		AdmissionRehearsals:   request.Body.AdmissionRehearsals,
		AbandonTimeoutMinutes: request.Body.AbandonTimeoutMinutes,
		CodeMaxFailedAttempts: request.Body.CodeMaxFailedAttempts,
	}

	a, ok := actor.From(ctx)
	if !ok {
		return nil, httpx.NewError(httpx.KindUnauthenticated, "Войдите в портал.")
	}
	result, err := t.service.Update(ctx, patch, &a.UserID)
	if err != nil {
		var rangeErr *RangeError
		if errors.As(err, &rangeErr) {
			return gen.UpdatePortalSettings422ApplicationProblemPlusJSONResponse{
				UnprocessableApplicationProblemPlusJSONResponse: gen.UnprocessableApplicationProblemPlusJSONResponse(
					httpx.NewError(httpx.KindValidationFailed, "Значение вне допустимого диапазона.").Problem(),
				),
			}, nil
		}
		return nil, err
	}

	return gen.UpdatePortalSettings200JSONResponse(toGenSettings(result)), nil
}

func toGenSettings(s Settings) gen.PortalSettings {
	demoMode := s.DemoMode
	return gen.PortalSettings{
		AdmissionRehearsals:   s.AdmissionRehearsals,
		AbandonTimeoutMinutes: s.AbandonTimeoutMinutes,
		CodeMaxFailedAttempts: s.CodeMaxFailedAttempts,
		DemoMode:              &demoMode,
		UpdatedAt:             s.UpdatedAt,
		UpdatedByName:         s.UpdatedByName,
	}
}
