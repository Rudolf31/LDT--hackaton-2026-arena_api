package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
)

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

func requireBody[T any](body *T) (*T, error) {
	if body == nil {
		return nil, httpx.NewError(httpx.KindInvalidBody, "В запросе должно быть тело JSON.")
	}
	return body, nil
}

// --- создание, библиотека ---

func (t *Transport) ListScenarios(ctx context.Context, request gen.ListScenariosRequestObject) (gen.ListScenariosResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset := httpx.Pagination(request.Params.Limit, request.Params.Offset)
	p := listParams{Limit: limit, Offset: offset, Query: request.Params.Q, Tag: request.Params.Tag}
	if request.Params.Sphere != nil {
		v := string(*request.Params.Sphere)
		p.Sphere = &v
	}
	if request.Params.NegotiationType != nil {
		v := string(*request.Params.NegotiationType)
		p.NegotiationType = &v
	}
	if request.Params.Mode != nil {
		v := string(*request.Params.Mode)
		p.Mode = &v
	}
	if request.Params.Status != nil {
		v := string(*request.Params.Status)
		p.Status = &v
	}
	result, err := t.service.List(ctx, a, p)
	if err != nil {
		return nil, err
	}
	items := make([]gen.ScenarioListItem, 0, len(result.Items))
	for _, d := range result.Items {
		items = append(items, toScenarioListItem(d))
	}
	return gen.ListScenarios200JSONResponse{Items: items, Page: httpx.PageOf(result.Total, limit, offset)}, nil
}

func (t *Transport) CreateScenario(ctx context.Context, request gen.CreateScenarioRequestObject) (gen.CreateScenarioResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	body, err := requireBody(request.Body)
	if err != nil {
		return nil, err
	}
	fields, err := createFieldsFromRequest(*body)
	if err != nil {
		return nil, err
	}
	d, err := t.service.Create(ctx, a, fields)
	if err != nil {
		return nil, err
	}
	return gen.CreateScenario201JSONResponse(toScenarioCard(d)), nil
}

// createFieldsFromRequest разбирает CreateScenarioRequest по дискриминатору
// origin. Ветка brief разбирается из сырого JSON тела, а не через
// сгенерированный CreateFromBrief: та структура всё ещё несёт отменённую
// анкету questionnaire (D-26) и не знает про title.
func createFieldsFromRequest(body gen.CreateScenarioRequest) (createFields, error) {
	discriminator, err := body.Discriminator()
	if err != nil {
		return createFields{}, httpx.NewError(httpx.KindInvalidBody, "Не удалось определить происхождение сценария.")
	}
	switch gen.Origin(discriminator) {
	case gen.OriginBrief:
		raw, err := body.MarshalJSON()
		if err != nil {
			return createFields{}, fmt.Errorf("тело запроса: %w", err)
		}
		// Форма тела продублирована в platform/httpx/schema.go:patchOutdatedSchemas
		// (JSON Schema для проверки тела) — при добавлении/переименовании
		// поля здесь проверьте и её (найдено в код-ревью 04).
		var bf struct {
			Mode  gen.Mode `json:"mode"`
			Title *string  `json:"title"`
		}
		if err := json.Unmarshal(raw, &bf); err != nil {
			return createFields{}, httpx.NewError(httpx.KindInvalidBody, "Не удалось разобрать тело запроса.")
		}
		return createFields{Origin: gen.OriginBrief, Mode: bf.Mode, Title: bf.Title}, nil
	case gen.OriginTemplate:
		v, err := body.AsCreateFromTemplate()
		if err != nil {
			return createFields{}, httpx.NewError(httpx.KindInvalidBody, "Не удалось разобрать тело запроса.")
		}
		return createFields{Origin: gen.OriginTemplate, Mode: v.Mode, TemplateID: &v.TemplateId, Title: v.Title}, nil
	case gen.OriginCopy:
		v, err := body.AsCreateCopy()
		if err != nil {
			return createFields{}, httpx.NewError(httpx.KindInvalidBody, "Не удалось разобрать тело запроса.")
		}
		title := v.Title
		return createFields{
			Origin: gen.OriginCopy, Mode: v.Mode, SourceScenarioID: &v.SourceScenarioId,
			SourceVersion: v.SourceVersion, Title: &title,
		}, nil
	case gen.OriginManual:
		v, err := body.AsCreateManual()
		if err != nil {
			return createFields{}, httpx.NewError(httpx.KindInvalidBody, "Не удалось разобрать тело запроса.")
		}
		return createFields{Origin: gen.OriginManual, Mode: v.Mode, Title: v.Title}, nil
	default:
		return createFields{}, fieldError("/origin", "Неизвестное происхождение сценария.")
	}
}

func (t *Transport) ListScenarioTemplates(ctx context.Context, _ gen.ListScenarioTemplatesRequestObject) (gen.ListScenarioTemplatesResponseObject, error) {
	if _, err := currentActor(ctx); err != nil {
		return nil, err
	}
	return gen.ListScenarioTemplates200JSONResponse(t.service.TemplateList()), nil
}

func (t *Transport) ImportScenario(ctx context.Context, request gen.ImportScenarioRequestObject) (gen.ImportScenarioResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	body, err := requireBody(request.Body)
	if err != nil {
		return nil, err
	}
	d, err := t.service.Import(ctx, a, *body)
	if err != nil {
		return nil, err
	}
	return gen.ImportScenario201JSONResponse(toScenarioCard(d)), nil
}

func (t *Transport) GetScenario(ctx context.Context, request gen.GetScenarioRequestObject) (gen.GetScenarioResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	d, err := t.service.Get(ctx, a, request.ScenarioId)
	if err != nil {
		return nil, err
	}
	return gen.GetScenario200JSONResponse(toScenarioCard(d)), nil
}

func (t *Transport) ArchiveScenario(ctx context.Context, request gen.ArchiveScenarioRequestObject) (gen.ArchiveScenarioResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	body, err := requireBody(request.Body)
	if err != nil {
		return nil, err
	}
	d, err := t.service.Archive(ctx, a, request.ScenarioId, body.Archived)
	if err != nil {
		return nil, err
	}
	return gen.ArchiveScenario200JSONResponse(toScenarioCard(d)), nil
}

// --- черновик ---

func (t *Transport) GetDraft(ctx context.Context, request gen.GetDraftRequestObject) (gen.GetDraftResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	d, err := t.service.GetDraft(ctx, a, request.ScenarioId)
	if err != nil {
		return nil, err
	}
	etag := d.ETag
	return gen.GetDraft200JSONResponse{Body: toDraft(d), Headers: gen.GetDraft200ResponseHeaders{ETag: &etag}}, nil
}

func (t *Transport) SaveDraft(ctx context.Context, request gen.SaveDraftRequestObject) (gen.SaveDraftResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	body, err := requireBody(request.Body)
	if err != nil {
		return nil, err
	}
	d, err := t.service.SaveDraft(ctx, a, request.ScenarioId, body.Document, request.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return gen.SaveDraft200JSONResponse(toDraft(d)), nil
}

func (t *Transport) CheckDraft(ctx context.Context, request gen.CheckDraftRequestObject) (gen.CheckDraftResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	body, err := requireBody(request.Body)
	if err != nil {
		return nil, err
	}
	d, err := t.service.CheckDraft(ctx, a, request.ScenarioId, body.Document)
	if err != nil {
		return nil, err
	}
	return gen.CheckDraft200JSONResponse{Check: d.Check, Fingerprint: d.Fingerprint, ResetsAdmission: d.ResetsAdmission}, nil
}

// --- публикация, версии, экспорт ---

func (t *Transport) PublishScenario(ctx context.Context, request gen.PublishScenarioRequestObject) (gen.PublishScenarioResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	body, err := requireBody(request.Body)
	if err != nil {
		return nil, err
	}
	v, err := t.service.Publish(ctx, a, request.ScenarioId, body.Fingerprint)
	if err != nil {
		return nil, err
	}
	return gen.PublishScenario201JSONResponse(toVersionSummary(v)), nil
}

func (t *Transport) ListVersions(ctx context.Context, request gen.ListVersionsRequestObject) (gen.ListVersionsResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.service.ListVersions(ctx, a, request.ScenarioId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListVersions200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, toVersionSummary(r))
	}
	return out, nil
}

func (t *Transport) GetVersion(ctx context.Context, request gen.GetVersionRequestObject) (gen.GetVersionResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	v, err := t.service.GetVersion(ctx, a, request.ScenarioId, request.VersionNumber)
	if err != nil {
		return nil, err
	}
	return gen.GetVersion200JSONResponse(toVersion(v)), nil
}

func (t *Transport) ExportScenario(ctx context.Context, request gen.ExportScenarioRequestObject) (gen.ExportScenarioResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	result, err := t.service.Export(ctx, a, request.ScenarioId, request.Params.Version)
	if err != nil {
		return nil, err
	}
	disposition := fmt.Sprintf(`attachment; filename="%s"`, result.Filename)
	return gen.ExportScenario200JSONResponse{
		Body:    result.Document,
		Headers: gen.ExportScenario200ResponseHeaders{ContentDisposition: &disposition},
	}, nil
}

// --- преобразование в ответы контракта ---

func scenarioStatus(row scenarioRow) string {
	switch {
	case row.ArchivedAt != nil:
		return "archived"
	case row.VersionsCount > 0:
		return "published"
	default:
		return "draft"
	}
}

// draftDiffers — в черновике есть изменения, которых нет в последней
// версии; черновик без опубликованной версии тоже «отличается» — в нём
// есть содержание, которого нет нигде среди версий.
func draftDiffers(row scenarioRow, latest *versionRow) bool {
	if row.DraftFingerprint == nil {
		return false
	}
	if latest == nil {
		return true
	}
	return *row.DraftFingerprint != latest.Fingerprint
}

func blockingErrors(row scenarioRow) *int {
	if row.DraftCheck == nil {
		return nil
	}
	var cr struct {
		Blocking int `json:"blocking"`
	}
	if err := json.Unmarshal(row.DraftCheck, &cr); err != nil {
		// draft_check пишется только этим же модулем (toCheckResult) — если
		// он не разбирается, это дефект, а не пользовательский ввод; молчать
		// об этом нельзя (правило 8, найдено в код-ревью 04).
		log.Printf("scenarios: не удалось разобрать draft_check сценария %s: %v", row.ID, err)
		return nil
	}
	n := cr.Blocking
	return &n
}

// extractCopiedFrom — authoring.copied_from черновика, для карточки
// сценария (arena-scenario-format.md 18.1).
func extractCopiedFrom(document json.RawMessage) (uuid.UUID, int, bool) {
	if document == nil {
		return uuid.Nil, 0, false
	}
	var doc struct {
		Authoring struct {
			CopiedFrom *struct {
				ScenarioID string `json:"scenario_id"`
				Version    int    `json:"version"`
			} `json:"copied_from"`
		} `json:"authoring"`
	}
	if err := json.Unmarshal(document, &doc); err != nil || doc.Authoring.CopiedFrom == nil {
		return uuid.Nil, 0, false
	}
	id, err := uuid.Parse(doc.Authoring.CopiedFrom.ScenarioID)
	if err != nil {
		return uuid.Nil, 0, false
	}
	return id, doc.Authoring.CopiedFrom.Version, true
}

func toScenarioListItem(d scenarioDetail) gen.ScenarioListItem {
	row := d.Row
	title, sphere, negotiationType, tags, generating := passportOrGeneration(row)
	item := gen.ScenarioListItem{
		Id: row.ID, Slug: row.Slug, Title: title, Mode: gen.Mode(row.Mode), Origin: gen.Origin(row.Origin),
		Sphere: sphere, NegotiationType: negotiationType, Tags: tags,
		Status: gen.ScenarioListItemStatus(scenarioStatus(row)),
	}
	if d.LatestVersion != nil {
		vs := toVersionSummary(*d.LatestVersion)
		item.LatestVersion = &vs
	}
	if row.DraftDocument != nil {
		differs := draftDiffers(row, d.LatestVersion)
		item.DraftDiffers = &differs
	}
	item.BlockingErrors = blockingErrors(row)
	if row.DraftDocument == nil {
		g := generating
		item.Generating = &g
	}
	return item
}

func toScenarioCard(d scenarioDetail) gen.ScenarioCard {
	row := d.Row
	item := toScenarioListItem(d)
	card := gen.ScenarioCard{
		Id: item.Id, Slug: item.Slug, Title: item.Title, Mode: item.Mode, Origin: item.Origin,
		Sphere: item.Sphere, NegotiationType: item.NegotiationType, Tags: item.Tags,
		Status: gen.ScenarioCardStatus(item.Status), LatestVersion: item.LatestVersion,
		DraftDiffers: item.DraftDiffers, BlockingErrors: item.BlockingErrors, Generating: item.Generating,
		ArchivedAt: row.ArchivedAt,
	}
	createdAt := row.CreatedAt
	card.CreatedAt = &createdAt
	if row.CreatedByName != "" {
		n := row.CreatedByName
		card.CreatedByName = &n
	}
	locked := row.VersionsCount > 0
	card.ModeLocked = &locked
	count := row.VersionsCount
	card.VersionsCount = &count
	admission := toAdmission(d.Admission)
	card.Admission = &admission
	if id, version, ok := extractCopiedFrom(row.DraftDocument); ok {
		sid, v := id, version
		card.CopiedFrom = &struct {
			ScenarioId *uuid.UUID `json:"scenario_id,omitempty"`
			Version    *int       `json:"version,omitempty"`
		}{ScenarioId: &sid, Version: &v}
	}
	return card
}

func toAdmission(a admissionView) gen.Admission {
	return gen.Admission{Fingerprint: a.Fingerprint, Done: a.Done, Required: a.Required, Reset: a.Reset}
}

func toDraft(d draftView) gen.Draft {
	return gen.Draft{
		ScenarioId: d.ScenarioID, Document: d.Document, Fingerprint: d.Fingerprint, Check: d.Check,
		Admission: toAdmission(d.Admission), UpdatedAt: d.UpdatedAt, UpdatedByName: d.UpdatedByName,
	}
}

func toVersionSummary(v versionRow) gen.VersionSummary {
	title, engineVersion, publishedByName, sessionsCount := v.Title, v.EngineVersion, v.PublishedByName, v.SessionsCount
	return gen.VersionSummary{
		Id: v.ID, Number: v.Number, Fingerprint: v.Fingerprint, Mode: gen.Mode(v.Mode),
		Title: &title, EngineVersion: &engineVersion,
		AdmissionRehearsals: v.AdmissionRehearsals, AdmissionRequired: v.AdmissionRequired,
		PublishedAt: v.PublishedAt, PublishedByName: &publishedByName, SessionsCount: &sessionsCount,
	}
}

func toVersion(v versionRow) gen.Version {
	vs := toVersionSummary(v)
	return gen.Version{
		Id: vs.Id, Number: vs.Number, Fingerprint: vs.Fingerprint, Mode: vs.Mode, Title: vs.Title,
		EngineVersion: vs.EngineVersion, AdmissionRehearsals: vs.AdmissionRehearsals, AdmissionRequired: vs.AdmissionRequired,
		PublishedAt: vs.PublishedAt, PublishedByName: vs.PublishedByName, SessionsCount: vs.SessionsCount,
		Document: v.Document,
	}
}
