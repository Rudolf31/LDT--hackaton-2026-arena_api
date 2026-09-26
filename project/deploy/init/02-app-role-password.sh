#!/bin/sh
# Пароль роли arena_app задаёт скрипт развёртывания, не arena-db.sql
# (комментарий в самом файле схемы, строка 6-7). Выполняется ролью-владельцем
# сразу после 01-arena-db.sql — порядок держит имя файла.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
  ALTER ROLE arena_app PASSWORD '$ARENA_APP_PASSWORD';
EOSQL
