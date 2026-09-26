// Package demo — демо-режим и первичная заливка данных (NFR-D-04). В этапе
// 02 — только заливка пользователей, группы и сотрудников (D-23); вход
// гостя демо появится в этапе 11.
package demo

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/consents"
	"arena-portal-backend/internal/modules/people"
	"arena-portal-backend/internal/modules/profiles"
)

type Module struct {
	seeder *seeder
}

func New(pool *pgxpool.Pool, users auth.Provisioner, staff people.Provisioner,
	profileProvisioner profiles.Provisioner, consentProvisioner consents.Provisioner,
) *Module {
	return &Module{seeder: &seeder{
		pool: pool, users: users, staff: staff,
		profiles: profileProvisioner, consents: consentProvisioner,
	}}
}
