package people

import (
	"context"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
)

// defaultProfileName — у группы без своего профиля действует профиль по
// умолчанию (FR-PF-02). Название своего профиля появится с модулем
// profiles (этап 06).
const defaultProfileName = "Профиль по умолчанию"

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

// --- группы ---

func (t *Transport) ListGroups(ctx context.Context, request gen.ListGroupsRequestObject) (gen.ListGroupsResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	includeArchived := request.Params.IncludeArchived != nil && *request.Params.IncludeArchived
	groups, err := t.service.ListGroups(ctx, a, includeArchived)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListGroups200JSONResponse, 0, len(groups))
	for _, g := range groups {
		out = append(out, toGroup(g))
	}
	return out, nil
}

func (t *Transport) CreateGroup(ctx context.Context, request gen.CreateGroupRequestObject) (gen.CreateGroupResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	g, err := t.service.CreateGroup(ctx, a, groupFields(request.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateGroup201JSONResponse(toGroup(g)), nil
}

func (t *Transport) UpdateGroup(ctx context.Context, request gen.UpdateGroupRequestObject) (gen.UpdateGroupResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	g, err := t.service.UpdateGroup(ctx, a, request.GroupId, groupFields(request.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateGroup200JSONResponse(toGroup(g)), nil
}

func groupFields(b *gen.GroupWrite) GroupFields {
	return GroupFields{Name: b.Name, Department: b.Department, TrainerProfileID: b.TrainerProfileId, Archived: b.Archived}
}

func toGroup(g groupView) gen.Group {
	count := g.PeopleCount
	out := gen.Group{
		Id:               g.ID,
		Name:             g.Name,
		Department:       g.Department,
		TrainerProfileId: g.TrainerProfileID,
		ArchivedAt:       g.ArchivedAt,
		HasAccess:        g.HasAccess,
		PeopleCount:      &count,
	}
	if g.TrainerProfileID == nil {
		name := defaultProfileName
		out.TrainerProfileName = &name
	}
	return out
}

// --- сотрудники ---

func (t *Transport) ListPeople(ctx context.Context, request gen.ListPeopleRequestObject) (gen.ListPeopleResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	limit, offset := httpx.Pagination(request.Params.Limit, request.Params.Offset)
	rows, total, err := t.service.ListPeople(ctx, a, request.Params.GroupId, request.Params.Q, limit, offset)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Person, 0, len(rows))
	for _, p := range rows {
		items = append(items, toPerson(p))
	}
	return gen.ListPeople200JSONResponse{Items: items, Page: httpx.PageOf(total, limit, offset)}, nil
}

func (t *Transport) CreatePerson(ctx context.Context, request gen.CreatePersonRequestObject) (gen.CreatePersonResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := t.service.CreatePerson(ctx, a, personFields(request.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreatePerson201JSONResponse(toPerson(p)), nil
}

// GetPersonCard — назначения, согласия, возражения и решения появятся
// с их модулями (этапы 06–10); до тех пор списки пустые.
func (t *Transport) GetPersonCard(ctx context.Context, request gen.GetPersonCardRequestObject) (gen.GetPersonCardResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	card, err := t.service.PersonCard(ctx, a, request.SubjectId)
	if err != nil {
		return nil, err
	}
	return gen.GetPersonCard200JSONResponse{
		Person:         toPerson(card.personRow),
		ResultsVisible: card.ResultsVisible,
		Assignments:    []gen.Assignment{},
		Consents:       []gen.ConsentRecord{},
		Decisions:      []gen.Decision{},
		Objections:     []gen.Objection{},
	}, nil
}

func (t *Transport) UpdatePerson(ctx context.Context, request gen.UpdatePersonRequestObject) (gen.UpdatePersonResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := t.service.UpdatePerson(ctx, a, request.SubjectId, personFields(request.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdatePerson200JSONResponse(toPerson(p)), nil
}

func (t *Transport) WithdrawConsent(ctx context.Context, request gen.WithdrawConsentRequestObject) (gen.WithdrawConsentResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := t.service.WithdrawConsent(ctx, a, request.SubjectId, request.Body.Scope, request.Body.ConfirmName)
	if err != nil {
		return nil, err
	}

	out := gen.WithdrawConsent200JSONResponse{Number: r.Number, Scope: gen.ConsentWithdrawalResultScope(r.Scope)}
	running := false
	out.RunningSession = &running
	var message string
	if r.Scope == gen.ConsentWithdrawalScopeAll {
		cancelled := r.CancelledAssignments
		out.CancelledAssignments = &cancelled
		message = "Согласие отозвано: данные сотрудника удалены, ключ шифрования уничтожен. Остался только номер " + r.Number + "."
	} else {
		message = "Отмечено: следующие сессии сотрудника пойдут без внешней нейросети."
	}
	out.Message = &message
	return out, nil
}

func personFields(b *gen.PersonWrite) PersonFields {
	return PersonFields{GroupID: b.GroupId, FullName: b.FullName, Pseudonym: b.Pseudonym, PersonnelNo: b.PersonnelNo, JobTitle: b.JobTitle}
}

func toPerson(p personRow) gen.Person {
	groupName := p.GroupName
	return gen.Person{
		SubjectId:             p.SubjectID,
		Number:                p.Number,
		GroupId:               p.GroupID,
		GroupName:             &groupName,
		FullName:              p.FullName,
		Pseudonym:             p.Pseudonym,
		PersonnelNo:           p.PersonnelNo,
		JobTitle:              p.JobTitle,
		ExternalAiWithdrawnAt: p.ExternalAIWithdrawnAt,
	}
}
