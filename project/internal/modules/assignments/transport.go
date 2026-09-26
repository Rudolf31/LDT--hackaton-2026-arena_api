package assignments

import (
	"context"
	"time"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
)

// Transport — адреса /api/portal/assignments*; cmd/portal/api.go вызывает
// их по имени (D-17).
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

func (t *Transport) ListAssignments(ctx context.Context, request gen.ListAssignmentsRequestObject) (gen.ListAssignmentsResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	p := request.Params
	limit, offset := httpx.Pagination(p.Limit, p.Offset)
	views, total, err := t.service.List(ctx, a, ListFilter{
		ID: p.Id, GroupID: p.GroupId, SubjectID: p.SubjectId, ScenarioID: p.ScenarioId,
		Status: p.Status, BatchID: p.BatchId, DueBefore: p.DueBefore, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]gen.Assignment, 0, len(views))
	for _, v := range views {
		items = append(items, toAssignment(v))
	}
	return gen.ListAssignments200JSONResponse{Items: items, Page: httpx.PageOf(total, limit, offset)}, nil
}

func (t *Transport) CreateAssignments(ctx context.Context, request gen.CreateAssignmentsRequestObject) (gen.CreateAssignmentsResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	b := request.Body
	res, err := t.service.Create(ctx, a, CreateInput{
		SubjectIDs: b.SubjectIds, VersionID: b.ScenarioVersionId, Difficulty: b.Difficulty,
		ProfileID: b.TrainerProfileId, DueAt: b.DueAt,
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateAssignments201JSONResponse(toCreateResult(res)), nil
}

func (t *Transport) ReissueCodes(ctx context.Context, request gen.ReissueCodesRequestObject) (gen.ReissueCodesResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	res, err := t.service.Reissue(ctx, a, request.Body.AssignmentIds)
	if err != nil {
		return nil, err
	}
	return gen.ReissueCodes200JSONResponse(toCreateResult(res)), nil
}

func (t *Transport) ExtendAssignment(ctx context.Context, request gen.ExtendAssignmentRequestObject) (gen.ExtendAssignmentResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	v, err := t.service.Extend(ctx, a, request.AssignmentId, request.Body.DueAt)
	if err != nil {
		return nil, err
	}
	return gen.ExtendAssignment200JSONResponse(toAssignment(v)), nil
}

func (t *Transport) CancelAssignment(ctx context.Context, request gen.CancelAssignmentRequestObject) (gen.CancelAssignmentResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	v, err := t.service.Cancel(ctx, a, request.AssignmentId)
	if err != nil {
		return nil, err
	}
	return gen.CancelAssignment200JSONResponse(toAssignment(v)), nil
}

func (t *Transport) UnblockCode(ctx context.Context, request gen.UnblockCodeRequestObject) (gen.UnblockCodeResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	v, err := t.service.Unblock(ctx, a, request.AssignmentId)
	if err != nil {
		return nil, err
	}
	return gen.UnblockCode200JSONResponse(toAssignment(v)), nil
}

func toCreateResult(r CreateResult) gen.AssignmentCreateResult {
	out := gen.AssignmentCreateResult{BatchId: r.BatchID, Created: make([]gen.IssuedCode, 0, len(r.Created))}
	for _, c := range r.Created {
		out.Created = append(out.Created, gen.IssuedCode{
			AssignmentId: c.AssignmentID, Code: c.Code, Link: c.Link, Person: toPersonRef(c.Person),
		})
	}
	out.Skipped = make([]struct {
		AssignmentId *uuid.UUID `json:"assignment_id,omitempty"`
		Reason       string     `json:"reason"`
		SubjectId    uuid.UUID  `json:"subject_id"`
	}, 0, len(r.Skipped))
	for _, s := range r.Skipped {
		out.Skipped = append(out.Skipped, struct {
			AssignmentId *uuid.UUID `json:"assignment_id,omitempty"`
			Reason       string     `json:"reason"`
			SubjectId    uuid.UUID  `json:"subject_id"`
		}{AssignmentId: s.AssignmentID, Reason: s.Reason, SubjectId: s.SubjectID})
	}
	return out
}

func toAssignment(v View) gen.Assignment {
	r := v.Row
	out := gen.Assignment{
		Id: r.ID, Person: toPersonRef(v.Person), GroupId: r.GroupID,
		ScenarioId: v.Version.ScenarioID, ScenarioVersionId: r.VersionID, VersionNumber: v.Version.Number,
		Mode: v.Version.Mode, Difficulty: gen.Difficulty(r.Difficulty), TrainerProfileId: r.ProfileID,
		TrainerProfileName: v.ProfileName, DueAt: r.DueAt, BatchId: r.BatchID, Status: v.Status,
		LastAttemptAt: v.Sessions.LastStartedAt, CreatedAt: &r.CreatedAt, CreatedByName: r.CreatedByName,
		CancelledAt: r.CancelledAt,
	}
	title := v.Version.Title
	out.ScenarioTitle = &title
	newer := v.Version.NewerExists
	out.NewerVersionExists = &newer
	attempts := v.Sessions.Attempts
	out.Attempts = &attempts
	if v.Sessions.BreakStage != nil || v.Sessions.BreakTurn != nil {
		out.BreakPoint = &struct {
			Stage *string `json:"stage,omitempty"`
			Turn  *int    `json:"turn,omitempty"`
		}{Stage: v.Sessions.BreakStage, Turn: v.Sessions.BreakTurn}
	}
	if r.CodeID != nil {
		out.Code = &struct {
			BlockedAt      *time.Time `json:"blocked_at,omitempty"`
			FailedAttempts *int       `json:"failed_attempts,omitempty"`
			IssuedAt       *time.Time `json:"issued_at,omitempty"`
		}{BlockedAt: r.CodeBlockedAt, FailedAttempts: r.CodeFailed, IssuedAt: r.CodeIssuedAt}
	}
	if r.CancelReason != nil {
		reason := gen.AssignmentCancelReason(*r.CancelReason)
		out.CancelReason = &reason
		atCancel := v.Sessions.RunningAt(*r.CancelledAt)
		out.RunningSessionAtCancel = &atCancel
	}
	return out
}

func toPersonRef(p PersonRefView) gen.PersonRef {
	out := gen.PersonRef{SubjectId: p.SubjectID, Number: p.Number, DisplayName: p.DisplayName}
	if p.DisplayName != nil {
		pseudonym := p.IsPseudonym
		out.IsPseudonym = &pseudonym
	}
	return out
}
