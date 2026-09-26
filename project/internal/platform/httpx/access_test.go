package httpx

import (
	"reflect"
	"testing"

	arenaapi "arena-portal-backend/api"
	"arena-portal-backend/internal/api/gen"
)

// Каждая операция strict-сервера должна найти свои роли в таблице, иначе
// её доступ решало бы умолчание в middleware, а не контракт.
func TestOperationAccessCoversEveryStrictOperation(t *testing.T) {
	access, err := LoadOperationAccess(arenaapi.Spec)
	if err != nil {
		t.Fatalf("LoadOperationAccess: %v", err)
	}

	iface := reflect.TypeOf((*gen.StrictServerInterface)(nil)).Elem()
	for i := 0; i < iface.NumMethod(); i++ {
		name := iface.Method(i).Name
		if _, ok := access[name]; !ok {
			t.Errorf("у операции %s нет записи x-roles в таблице доступа", name)
		}
	}
	if len(access) != iface.NumMethod() {
		t.Errorf("в таблице доступа %d операций, а в strict-интерфейсе %d", len(access), iface.NumMethod())
	}
}

func TestOperationAccessSettingsAreClosed(t *testing.T) {
	access, err := LoadOperationAccess(arenaapi.Spec)
	if err != nil {
		t.Fatalf("LoadOperationAccess: %v", err)
	}
	patch := access["UpdatePortalSettings"]
	if patch.Allows(RolePublic) || !patch.Allows(RoleAdmin) || patch.Allows(RoleMethodologist) {
		t.Fatalf("PATCH /settings должен быть только у администратора, в контракте: %v", patch.Roles)
	}
	if get := access["GetPortalSettings"]; get.Allows(RolePublic) {
		t.Fatalf("GET /settings не должен быть открыт без входа, в контракте: %v", get.Roles)
	}
	if login := access["PortalLogin"]; !login.Allows(RolePublic) {
		t.Fatalf("вход должен быть открыт без входа, в контракте: %v", login.Roles)
	}
}
