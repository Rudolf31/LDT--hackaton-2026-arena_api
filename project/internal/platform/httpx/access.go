package httpx

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Роли из расширения x-roles контракта. Три первые — роли портала
// (portal_users.role), остальные — виды токена клиента-тренажёра и адреса
// без входа.
const (
	RoleAdmin         = "admin"
	RoleMethodologist = "methodologist"
	RoleObserver      = "observer"
	RolePublic        = "public"
	RoleParticipant   = "participant"
	RoleGuest         = "guest"
	RoleRehearsal     = "rehearsal"
)

// OperationAccess — кто может вызвать операцию (x-roles) и нужен ли доступ
// к группе (x-group-access). Сам доступ к группе проверяет служба модуля:
// группа приходит то из тела, то из параметра, то из сессии.
type OperationAccess struct {
	Roles       []string
	GroupAccess bool
}

func (a OperationAccess) Allows(role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// LoadOperationAccess читает x-roles и x-group-access каждой операции
// контракта. Ключ — имя операции так, как его передаёт strict middleware
// сгенерированного сервера: operationId с заглавной первой буквой
// ("portalLogin" → "PortalLogin"). Операция без x-roles — ошибка старта:
// иначе её доступ решал бы случай.
func LoadOperationAccess(yamlContent []byte) (map[string]OperationAccess, error) {
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(yamlContent, &doc); err != nil {
		return nil, fmt.Errorf("разбор контракта: %w", err)
	}

	result := make(map[string]OperationAccess)
	for path, item := range doc.Paths {
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			node, ok := item[method]
			if !ok {
				continue
			}
			var op struct {
				OperationID string   `yaml:"operationId"`
				Roles       []string `yaml:"x-roles"`
				GroupAccess bool     `yaml:"x-group-access"`
			}
			if err := node.Decode(&op); err != nil {
				return nil, fmt.Errorf("разбор операции %s %s: %w", strings.ToUpper(method), path, err)
			}
			if op.OperationID == "" {
				continue
			}
			if len(op.Roles) == 0 {
				return nil, fmt.Errorf("у операции %s %s (%s) в контракте нет x-roles", strings.ToUpper(method), path, op.OperationID)
			}
			result[strictOperationName(op.OperationID)] = OperationAccess{Roles: op.Roles, GroupAccess: op.GroupAccess}
		}
	}
	return result, nil
}

func strictOperationName(operationID string) string {
	r, size := utf8.DecodeRuneInString(operationID)
	return string(unicode.ToUpper(r)) + operationID[size:]
}
