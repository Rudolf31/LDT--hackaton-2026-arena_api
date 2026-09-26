// Package demo — демо-режим и первичная заливка данных (NFR-D-04). В этапе
// 02 — только заливка пользователей, группы и сотрудников (D-23); вход
// гостя демо появится в этапе 11.
package demo

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"arena-portal-backend/internal/modules/auth"
	"arena-portal-backend/internal/modules/people"
)

type Module struct {
	seeder *seeder
}

func New(pool *pgxpool.Pool, users auth.Provisioner, staff people.Provisioner) *Module {
	return &Module{seeder: &seeder{pool: pool, users: users, staff: staff}}
}
