package audit

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
)

// Transport — адреса журнала: чтение с фильтрами и выгрузка в CSV (UC-A-09).
type Transport struct {
	service *service
}

func (t *Transport) ListAuditLog(ctx context.Context, request gen.ListAuditLogRequestObject) (gen.ListAuditLogResponseObject, error) {
	p := request.Params
	var outcome *string
	if p.Outcome != nil {
		o := string(*p.Outcome)
		outcome = &o
	}
	limit, offset := httpx.Pagination(p.Limit, p.Offset)
	views, total, err := t.service.List(ctx, Filter{
		From: p.From, To: p.To, ActorUserID: p.ActorUserId, GroupID: p.GroupId,
		SubjectNumber: p.SubjectNumber, Action: p.Action, Outcome: outcome,
	}, limit, offset)
	if err != nil {
		return nil, err
	}
	items := make([]gen.AuditEntry, 0, len(views))
	for _, v := range views {
		items = append(items, toEntry(v))
	}
	return gen.ListAuditLog200JSONResponse{Items: items, Page: httpx.PageOf(total, limit, offset)}, nil
}

func (t *Transport) ExportAuditLog(ctx context.Context, request gen.ExportAuditLogRequestObject) (gen.ExportAuditLogResponseObject, error) {
	a, ok := actor.From(ctx)
	if !ok {
		return nil, httpx.NewError(httpx.KindUnauthenticated, "Войдите в портал.")
	}
	b := request.Body
	data, err := t.service.Export(ctx, a.UserID, Filter{
		From: b.From, To: b.To, ActorUserID: b.ActorUserId, GroupID: b.GroupId,
		SubjectNumber: b.SubjectNumber, Action: b.Action,
	})
	if err != nil {
		return nil, err
	}
	disposition := fmt.Sprintf(`attachment; filename="arena-journal-%s.csv"`, time.Now().Format("2006-01-02"))
	return gen.ExportAuditLog200TextcsvResponse{
		Body:          bytes.NewReader(data),
		ContentLength: int64(len(data)),
		Headers:       gen.ExportAuditLog200ResponseHeaders{ContentDisposition: &disposition},
	}, nil
}

func toEntry(v View) gen.AuditEntry {
	e := gen.AuditEntry{
		Id:            v.ID,
		OccurredAt:    v.OccurredAt,
		ActorKind:     gen.AuditEntryActorKind(v.ActorKind),
		ActorUserId:   v.ActorUserID,
		ActorName:     v.ActorName,
		Action:        gen.AuditAction(v.Action),
		Outcome:       gen.AuditEntryOutcome(v.Outcome),
		SubjectNumber: v.SubjectNumber,
		SessionId:     v.SessionID,
		GroupId:       v.GroupID,
		GroupName:     v.GroupName,
		RowsCount:     v.RowsCount,
	}
	if len(v.Details) > 0 {
		d := v.Details
		e.Details = &d
	}
	return e
}
