package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"arena-portal-backend/internal/api/gen"
	"arena-portal-backend/internal/platform/actor"
	"arena-portal-backend/internal/platform/httpx"
	"arena-portal-backend/internal/platform/ratelimit"
)

// Transport — адреса /api/portal/auth/* и /api/portal/users/*, плюс strict
// middleware, через которое проходит каждая операция контракта.
type Transport struct {
	service      *service
	cookies      *sessionCodec
	access       map[string]httpx.OperationAccess
	demo         bool
	tokens       *trainerTokens
	codeLimiter  *ratelimit.Limiter
	tokenLimiter *ratelimit.Limiter
}

// trainerEnterOperation — ввод кода: единственный публичный адрес с
// пределом по адресу клиента (10 в минуту, arena-portal-hr.md 8.1).
const trainerEnterOperation = "TrainerEnter"

// requestState — то, что middleware передаёт обработчику и забирает
// обратно: адрес клиента для предела частоты входа и cookie, которую
// обработчик хочет поставить (вход) или стереть (выход).
type requestState struct {
	clientAddr string
	setCookie  *http.Cookie
}

type stateKey struct{}

func stateFrom(ctx context.Context) *requestState {
	if s, ok := ctx.Value(stateKey{}).(*requestState); ok {
		return s
	}
	return &requestState{}
}

func errUnauthenticated() *httpx.Error {
	return httpx.NewError(httpx.KindUnauthenticated, "Войдите в портал.")
}

// Middleware — strict middleware сгенерированного сервера: по имени
// операции находит её x-roles в контракте и пускает дальше только
// подходящую роль. Пользователь и его роль читаются из базы на каждом
// запросе; cookie продлевается на 8 часов с каждым запросом (D-19).
func (t *Transport) Middleware(next gen.StrictHandlerFunc, operation string) gen.StrictHandlerFunc {
	access, known := t.access[operation]
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		if !known {
			return nil, httpx.NewError(httpx.KindInternal, "У этой операции нет правил доступа.")
		}
		state := &requestState{clientAddr: clientAddr(r)}
		ctx = context.WithValue(ctx, stateKey{}, state)

		if access.Allows(httpx.RolePublic) {
			if operation == trainerEnterOperation {
				if err := allow(t.codeLimiter, state.clientAddr, "Слишком много попыток ввода кода. Подождите минуту."); err != nil {
					return nil, err
				}
			}
			resp, err := next(ctx, w, r, request)
			applyCookie(w, state)
			return resp, err
		}
		if !portalOperation(access) {
			ctx, err := t.authorizeTrainer(ctx, r, access)
			if err != nil {
				return nil, err
			}
			return next(ctx, w, r, request)
		}

		ctx, err := t.authorizeRoles(ctx, w, r, access.Roles, operation)
		if err != nil {
			return nil, err
		}
		resp, err := next(ctx, w, r, request)
		applyCookie(w, state)
		return resp, err
	}
}

// authorizeTrainer — адреса клиента-тренажёра: подписанный токен из
// заголовка Authorization, вид токена из x-roles операции, предел 30
// запросов в минуту на токен (D-56). Права по назначению проверяет
// служба по базе на каждом запросе — токен говорит только, кто пришёл.
func (t *Transport) authorizeTrainer(ctx context.Context, r *http.Request, access httpx.OperationAccess) (context.Context, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(raw) == "" {
		return ctx, httpx.NewError(httpx.KindUnauthenticated, "Откройте тренажёр по коду доступа.")
	}
	claims, err := t.tokens.Verify(strings.TrimSpace(raw))
	if err != nil {
		return ctx, httpx.NewError(httpx.KindUnauthenticated, "Время входа истекло — введите код заново.")
	}
	if !access.Allows(string(claims.Kind)) {
		return ctx, httpx.NewError(httpx.KindForbiddenRole, "Этот адрес недоступен с вашим входом в тренажёр.")
	}
	sum := sha256.Sum256([]byte(raw))
	if err := allow(t.tokenLimiter, hex.EncodeToString(sum[:]), "Слишком много запросов. Подождите минуту."); err != nil {
		return ctx, err
	}
	return actor.WithTrainer(ctx, actor.Trainer{
		Kind: string(claims.Kind), SubjectID: claims.SubjectID, AssignmentID: claims.AssignmentID,
		CodeID: claims.CodeID, DemoGuestID: claims.DemoGuestID,
	}), nil
}

// allow — 429 с Retry-After, если предел исчерпан; nil limiter — без предела.
func allow(l *ratelimit.Limiter, key, title string) error {
	if l == nil {
		return nil
	}
	if ok, retryAfter := l.Allow(key); !ok {
		return httpx.NewError(httpx.KindRateLimited, title).WithRetryAfter(int(math.Ceil(retryAfter.Seconds())))
	}
	return nil
}

// authorizeRoles — вход и проверка роли, общие для strict-middleware
// (Middleware выше, роли берутся из x-roles контракта) и обычного
// http-middleware (PortalMiddleware ниже, для четырёх ручных адресов
// авторства сценария, D-05, у которых записи в контракте нет и роли
// передаются явно). Продлевает cookie на успехе; отказ по роли пишет в
// журнал под действием операции, если оно есть (roleDenialActions).
func (t *Transport) authorizeRoles(ctx context.Context, w http.ResponseWriter, r *http.Request, roles []string, operation string) (context.Context, error) {
	u, err := t.authenticate(ctx, r)
	if err != nil {
		var httpErr *httpx.Error
		if errors.As(err, &httpErr) && httpErr.Kind == httpx.KindUnauthenticated {
			if _, cookieErr := r.Cookie(sessionCookieName); cookieErr == nil {
				http.SetCookie(w, t.cookies.clear())
			}
		}
		return ctx, err
	}
	if !slices.Contains(roles, string(u.Role)) {
		if err := t.service.DenyRole(ctx, u.ID, operation); err != nil {
			return ctx, err
		}
		return ctx, httpx.NewError(httpx.KindForbiddenRole, "Это действие недоступно вашей роли.")
	}
	http.SetCookie(w, t.cookies.issue(u.ID))
	return actor.With(ctx, actor.Actor{UserID: u.ID, Role: u.Role}), nil
}

// PortalMiddleware — обычное http-middleware для четырёх адресов
// авторства сценария (D-05): их нет в контракте, поэтому нет и записи в
// t.access — роли передаются вызывающей стороной (cmd/portal/router.go),
// а не читаются по имени операции. Переиспользует authorizeRoles — тот же
// вход, тот же отказ по роли и то же продление cookie, что и у
// сгенерированных операций.
func (t *Transport) PortalMiddleware(operation string, roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, err := t.authorizeRoles(r.Context(), w, r, roles, operation)
			if err != nil {
				var httpErr *httpx.Error
				if !errors.As(err, &httpErr) {
					httpErr = httpx.NewError(httpx.KindInternal, "Что-то пошло не так на сервере. Попробуйте ещё раз.")
				}
				httpx.WriteError(w, httpErr)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (t *Transport) authenticate(ctx context.Context, r *http.Request) (userRow, error) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return userRow{}, errUnauthenticated()
	}
	id, ok := t.cookies.parse(c.Value)
	if !ok {
		return userRow{}, errUnauthenticated()
	}
	u, active, err := t.service.ActiveUser(ctx, id)
	if err != nil {
		return userRow{}, err
	}
	if !active {
		return userRow{}, errUnauthenticated()
	}
	return u, nil
}

func portalOperation(a httpx.OperationAccess) bool {
	return a.Allows(httpx.RoleAdmin) || a.Allows(httpx.RoleMethodologist) || a.Allows(httpx.RoleObserver)
}

func applyCookie(w http.ResponseWriter, state *requestState) {
	if state.setCookie == nil {
		return
	}
	w.Header().Del("Set-Cookie")
	http.SetCookie(w, state.setCookie)
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func currentActor(ctx context.Context) (actor.Actor, error) {
	a, ok := actor.From(ctx)
	if !ok {
		return actor.Actor{}, errUnauthenticated()
	}
	return a, nil
}

// --- вход ---

func (t *Transport) PortalLogin(ctx context.Context, request gen.PortalLoginRequestObject) (gen.PortalLoginResponseObject, error) {
	state := stateFrom(ctx)
	u, err := t.service.Login(ctx, request.Body.Login, request.Body.Password, state.clientAddr)
	if err != nil {
		return nil, err
	}
	state.setCookie = t.cookies.issue(u.ID)
	return gen.PortalLogin200JSONResponse{Body: t.me(u)}, nil
}

func (t *Transport) PortalLogout(ctx context.Context, _ gen.PortalLogoutRequestObject) (gen.PortalLogoutResponseObject, error) {
	stateFrom(ctx).setCookie = t.cookies.clear()
	return gen.PortalLogout204Response{}, nil
}

func (t *Transport) PortalMe(ctx context.Context, _ gen.PortalMeRequestObject) (gen.PortalMeResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	u, err := t.service.User(ctx, a.UserID)
	if err != nil {
		return nil, err
	}
	return gen.PortalMe200JSONResponse(t.me(u)), nil
}

func (t *Transport) me(u userRow) gen.Me {
	return gen.Me{DemoMode: t.demo, User: toPortalUser(u)}
}

// --- пользователи ---

func (t *Transport) ListUsers(ctx context.Context, _ gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	users, err := t.service.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListUsers200JSONResponse, 0, len(users))
	for _, u := range users {
		out = append(out, toPortalUser(u))
	}
	return out, nil
}

func (t *Transport) CreateUser(ctx context.Context, request gen.CreateUserRequestObject) (gen.CreateUserResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	b := request.Body
	u, err := t.service.CreateUser(ctx, NewUser{Login: b.Login, Password: b.Password, FullName: b.FullName, Role: b.Role}, a.UserID)
	if err != nil {
		return nil, err
	}
	return gen.CreateUser201JSONResponse(toPortalUser(u)), nil
}

func (t *Transport) UpdateUser(ctx context.Context, request gen.UpdateUserRequestObject) (gen.UpdateUserResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	b := request.Body
	u, err := t.service.UpdateUser(ctx, request.UserId, UserChanges{
		FullName: b.FullName, Role: b.Role, IsActive: b.IsActive, NewPassword: b.NewPassword,
	}, a.UserID)
	if err != nil {
		return nil, err
	}
	return gen.UpdateUser200JSONResponse(toPortalUser(u)), nil
}

func (t *Transport) SetUserGroupAccess(ctx context.Context, request gen.SetUserGroupAccessRequestObject) (gen.SetUserGroupAccessResponseObject, error) {
	a, err := currentActor(ctx)
	if err != nil {
		return nil, err
	}
	u, err := t.service.SetGroupAccess(ctx, request.UserId, request.Body.GroupIds, a.UserID)
	if err != nil {
		return nil, err
	}
	return gen.SetUserGroupAccess200JSONResponse(toPortalUser(u)), nil
}

func toPortalUser(u userRow) gen.PortalUser {
	groups := u.GroupIDs
	if groups == nil {
		groups = []uuid.UUID{}
	}
	return gen.PortalUser{
		Id:          u.ID,
		Login:       u.Login,
		FullName:    u.FullName,
		Role:        u.Role,
		IsActive:    u.IsActive,
		LastLoginAt: u.LastLoginAt,
		GroupIds:    groups,
	}
}
