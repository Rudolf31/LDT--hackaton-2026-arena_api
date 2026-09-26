package consents

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
)

// Transport — POST /api/trainer/consents и
// POST /api/portal/people/{subjectId}/written-consents; cmd/portal/api.go
// вызывает их по имени (D-17).
type Transport struct {
	service  *service
	audience AudienceResolver
}

// TrainerRecordConsents — запрос 2 тренажёра (NFR-P-02). Кто пришёл —
// из токена (actor.Trainer, этап 07); какое назначение, режим и профиль —
// через AudienceResolver (D-48).
func (t *Transport) TrainerRecordConsents(ctx context.Context, request gen.TrainerRecordConsentsRequestObject) (gen.TrainerRecordConsentsResponseObject, error) {
	if _, ok := actor.TrainerFrom(ctx); !ok {
		return nil, httpx.NewError(httpx.KindUnauthenticated, "Откройте тренажёр по коду доступа ещё раз.")
	}
	audience, err := t.audience.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	answers := make([]Answer, 0, len(request.Body.Answers))
	for _, a := range request.Body.Answers {
		answers = append(answers, Answer{
			Kind: gen.ConsentKind(a.Kind), Answer: a.Answer, TextID: a.TextId, ShownSHA256: a.ShownTextSha256,
		})
	}
	out, err := t.service.Record(ctx, audience, answers)
	if err != nil {
		return nil, err
	}
	resp := gen.TrainerRecordConsents201JSONResponse{
		CanStart: out.CanStart, ExternalAiAllowed: out.ExternalAIAllowed, Message: out.Message,
		Records: make([]gen.ConsentRecord, 0, len(out.Records)),
	}
	for _, r := range out.Records {
		resp.Records = append(resp.Records, toRecord(r))
	}
	return resp, nil
}

func (t *Transport) RecordWrittenConsent(ctx context.Context, request gen.RecordWrittenConsentRequestObject) (gen.RecordWrittenConsentResponseObject, error) {
	a, ok := actor.From(ctx)
	if !ok {
		return nil, httpx.NewError(httpx.KindUnauthenticated, "Войдите в портал.")
	}
	in := WrittenInput{
		DocumentRef:     request.Body.DocumentRef,
		DocumentChannel: string(request.Body.DocumentChannel),
		SignedOn:        request.Body.SignedOn.Time,
	}
	if request.Body.ValidUntil != nil {
		v := request.Body.ValidUntil.Time
		in.ValidUntil = &v
	}
	r, err := t.service.RecordWritten(ctx, a, request.SubjectId, in)
	if err != nil {
		return nil, err
	}
	return gen.RecordWrittenConsent201JSONResponse(toRecord(r)), nil
}

func toRecord(r Record) gen.ConsentRecord {
	out := gen.ConsentRecord{
		Id: r.ID, Kind: r.Kind, Answer: r.Answer, AnsweredAt: r.AnsweredAt,
		TextVersion: r.TextVersion, DocumentRef: r.DocumentRef, RecordedByName: r.RecordedByName,
	}
	if r.DocumentChannel != nil {
		ch := gen.ConsentRecordDocumentChannel(*r.DocumentChannel)
		out.DocumentChannel = &ch
	}
	if r.SignedOn != nil {
		out.SignedOn = &openapi_types.Date{Time: *r.SignedOn}
	}
	if r.ValidUntil != nil {
		out.ValidUntil = &openapi_types.Date{Time: *r.ValidUntil}
	}
	return out
}
