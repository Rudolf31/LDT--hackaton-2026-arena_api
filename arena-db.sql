-- =====================================================================
-- Арена переговоров · база данных портала · PostgreSQL 16
-- Версия 0.2 — черновик к обсуждению. Пояснения — в arena-db.md.
--
-- Запускается один раз на пустой базе в кодировке UTF8 ролью-владельцем
-- (не ролью приложения). Приложение подключается ролью arena_app; пароль ей
-- задаёт скрипт развёртывания: ALTER ROLE arena_app PASSWORD '…'.
-- Если роли ещё нет, файл создаёт её сам — для этого владельцу нужно
-- право CREATEROLE (в контейнере postgres оно есть у пользователя по умолчанию).
--
-- Расширения не нужны: gen_random_uuid() и sha256() есть в ядре.
-- Хеши кодов доступа и всё шифрование считает приложение.
-- Демо-данные (NFR-D-04) заливает скрипт приложения, а не этот файл:
-- отпечатки сценариев и оценки считает пакет движка.
-- =====================================================================

BEGIN;

-- ---------------------------------------------------------------------
-- Роль приложения и схема
-- ---------------------------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'arena_app') THEN
    CREATE ROLE arena_app LOGIN;
  END IF;
END
$$;

CREATE SCHEMA arena;
REVOKE ALL ON SCHEMA arena FROM PUBLIC;
COMMENT ON SCHEMA arena IS 'Портал HR «Арена переговоров»';

SET search_path TO arena;

-- ---------------------------------------------------------------------
-- Перечисления: только списки, которыми владеет портал.
-- Списки формата сценария (сфера, типы ходов, этапы, опоры доводов)
-- хранятся текстом: их проверяет пакет движка.
-- ---------------------------------------------------------------------

CREATE TYPE user_role        AS ENUM ('methodologist', 'observer', 'admin');
CREATE TYPE scenario_mode    AS ENUM ('training', 'assessment');
CREATE TYPE scenario_origin  AS ENUM ('brief', 'template', 'copy', 'manual');
CREATE TYPE difficulty_level AS ENUM ('easy', 'normal', 'hard');
-- Порядок значений задаёт сортировку: S лучше всех.
CREATE TYPE final_rank       AS ENUM ('S', 'A', 'B', 'C', 'D', 'F');
CREATE TYPE session_status   AS ENUM (
  'in_progress',           -- идёт
  'completed',             -- дошла до финала
  'ended_by_participant',  -- «прервана участником»
  'turn_limit',            -- «закончились ходы»
  'time_limit',            -- «закончилось время»
  'connection_lost',       -- «прервана: потеря связи» (буфер клиента переполнен)
  'abandoned'              -- «прервана»: закрыта сервером по таймауту
);
CREATE TYPE process_status   AS ENUM ('pending', 'ok', 'not_received', 'too_short', 'not_scored');
CREATE TYPE scoring_profile  AS ENUM ('normal', 'short');
CREATE TYPE speaker_role     AS ENUM ('participant', 'opponent');
CREATE TYPE move_source      AS ENUM ('judge', 'keywords');
CREATE TYPE model_route      AS ENUM ('openrouter', 'own_server', 'stub');
CREATE TYPE consent_kind     AS ENUM (
  'notice_training',       -- уведомление перед тренировкой
  'consent_assessment',    -- согласие перед оценкой
  'consent_external_ai',   -- отправка разговора во внешнюю нейросеть
  'notice_demo',           -- уведомление в демо
  'written_assessment'     -- отметка HR о письменном согласии (бумага или ЭДО)
);
CREATE TYPE consent_answer   AS ENUM ('acknowledged', 'granted', 'refused');
CREATE TYPE audit_action     AS ENUM (
  'login', 'login_failed',
  'session_opened', 'session_reduced_opened', 'session_reviewed',
  'export_created', 'audit_exported',
  'assignment_created', 'assignment_cancelled', 'assignment_extended',
  'code_issued', 'code_reissued', 'code_blocked', 'code_unblocked',
  'decision_recorded', 'objection_submitted', 'objection_answered',
  'profile_saved', 'profile_key_changed',
  'user_saved', 'access_granted', 'access_revoked',
  'person_saved', 'consent_withdrawn', 'external_ai_withdrawn',
  'written_consent_recorded',
  'group_saved',
  'scenario_published', 'scenario_mode_changed', 'scenario_archived', 'settings_changed',
  'session_closed_by_server', 'session_reopened'
);

-- ---------------------------------------------------------------------
-- Пользователи портала, профили тренажёра, группы, доступ
-- ---------------------------------------------------------------------

CREATE TABLE portal_users (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  login         text        NOT NULL UNIQUE,
  password_hash text        NOT NULL,
  full_name     text        NOT NULL,
  role          user_role   NOT NULL,
  is_active     boolean     NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_login_at timestamptz,
  CONSTRAINT portal_users_login_format CHECK (login ~ '^[a-z0-9._-]{3,64}$')
);
COMMENT ON TABLE portal_users IS 'Сотрудники HR, которые входят в портал: методолог, наблюдатель, администратор (FR-AC-08). Не участники.';

CREATE TABLE trainer_profiles (
  id                       uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  name                     text        NOT NULL UNIQUE,
  is_default               boolean     NOT NULL DEFAULT false,
  settings                 jsonb       NOT NULL DEFAULT '{}'::jsonb,
  revision                 integer     NOT NULL DEFAULT 1,
  openrouter_key_enc       bytea,
  openrouter_key_last4     text,
  model_server_token_enc   bytea,
  model_server_token_last4 text,
  keys_changed_at          timestamptz,
  keys_changed_by          uuid        REFERENCES portal_users (id),
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  updated_by               uuid        REFERENCES portal_users (id),
  archived_at              timestamptz,
  CONSTRAINT trainer_profiles_revision CHECK (revision >= 1),
  CONSTRAINT trainer_profiles_settings_object CHECK (jsonb_typeof(settings) = 'object'),
  CONSTRAINT trainer_profiles_openrouter_pair CHECK ((openrouter_key_enc IS NULL) = (openrouter_key_last4 IS NULL)),
  CONSTRAINT trainer_profiles_token_pair CHECK ((model_server_token_enc IS NULL) = (model_server_token_last4 IS NULL)),
  CONSTRAINT trainer_profiles_last4 CHECK (char_length(openrouter_key_last4) = 4 AND char_length(model_server_token_last4) = 4),
  CONSTRAINT trainer_profiles_default_not_archived CHECK (NOT (is_default AND archived_at IS NOT NULL))
);
CREATE UNIQUE INDEX trainer_profiles_one_default ON trainer_profiles (is_default) WHERE is_default;
COMMENT ON TABLE trainer_profiles IS 'Профили тренажёра (FR-PF-01…05): модели, адреса, речь, камера, лимиты — в settings, форма закрыта схемой API (ключей там быть не может). Ключи зашифрованы приложением. Удаления нет: профиль архивируется, профиль по умолчанию — никогда.';
COMMENT ON COLUMN trainer_profiles.openrouter_key_enc IS 'Ключ OpenRouter, AES-256-GCM мастер-ключом из окружения. Целиком отдаётся только тренажёру: при старте сессии с согласием (FR-AC-07) и при погашении ссылки репетиции (FR-SC-10); администратору — только последние 4 символа (FR-PF-04, отступление — arena-api.md, 8).';
COMMENT ON COLUMN trainer_profiles.is_default IS 'Профиль по умолчанию. У роли приложения нет права UPDATE на этот столбец: снять отметку нельзя, поэтому профиль по умолчанию ровно один (FR-PF-05).';
COMMENT ON COLUMN trainer_profiles.revision IS 'Растёт на единицу при каждом сохранении; сессия запоминает, на какой ревизии прошла (FR-PF-03).';

CREATE VIEW trainer_profiles_public AS
SELECT id,
       name,
       is_default,
       settings,
       revision,
       (openrouter_key_enc IS NOT NULL)     AS has_openrouter_key,
       (model_server_token_enc IS NOT NULL) AS has_model_server_token,
       created_at,
       updated_at,
       archived_at
FROM trainer_profiles;
COMMENT ON VIEW trainer_profiles_public IS 'Профили без ключей и их последних символов — для всех ответов API не-администраторам (FR-PF-04).';

CREATE TABLE employee_groups (
  id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  name               text        NOT NULL UNIQUE,
  department         text,
  trainer_profile_id uuid        REFERENCES trainer_profiles (id),
  created_at         timestamptz NOT NULL DEFAULT now(),
  created_by         uuid        REFERENCES portal_users (id),
  archived_at        timestamptz
);
COMMENT ON TABLE employee_groups IS 'Группы сотрудников (UC-A-05). Доступ к результатам даётся на группу (FR-AC-08). Пустой профиль — профиль по умолчанию (FR-PF-02).';

CREATE TABLE user_group_access (
  user_id    uuid        NOT NULL REFERENCES portal_users (id),
  group_id   uuid        NOT NULL REFERENCES employee_groups (id),
  granted_by uuid        NOT NULL REFERENCES portal_users (id),
  granted_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, group_id)
);
CREATE INDEX user_group_access_group_idx ON user_group_access (group_id);
COMMENT ON TABLE user_group_access IS 'Кто из пользователей портала видит результаты какой группы (FR-AC-08, UC-A-06). Снятие доступа — удаление строки и запись в журнал.';

-- ---------------------------------------------------------------------
-- Участники: обезличенный номер отдельно от человека (NFR-PR-01)
-- ---------------------------------------------------------------------

CREATE TABLE subjects (
  id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  number           text        NOT NULL UNIQUE,
  is_demo          boolean     NOT NULL DEFAULT false,
  data_key_wrapped bytea,
  key_destroyed_at timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT subjects_key_state CHECK ((data_key_wrapped IS NULL) = (key_destroyed_at IS NOT NULL))
);
CREATE UNIQUE INDEX subjects_one_demo ON subjects (is_demo) WHERE is_demo;
COMMENT ON TABLE subjects IS 'Обезличенный номер участника и ключ шифрования его данных. На номер ссылаются назначения, сессии, журнал. Строки не удаляются; при отзыве согласия ключ стирается (NFR-PR-01).';
COMMENT ON COLUMN subjects.data_key_wrapped IS 'Ключ данных участника (32 байта), зашифрованный мастер-ключом. NULL — ключ уничтожен, тексты участника прочитать нельзя.';

CREATE TABLE people (
  subject_id               uuid        PRIMARY KEY REFERENCES subjects (id),
  group_id                 uuid        NOT NULL REFERENCES employee_groups (id),
  full_name                text,
  pseudonym                text,
  personnel_no             text,
  job_title                text,
  external_ai_withdrawn_at timestamptz,
  created_at               timestamptz NOT NULL DEFAULT now(),
  created_by               uuid        REFERENCES portal_users (id),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT people_has_name CHECK (full_name IS NOT NULL OR pseudonym IS NOT NULL)
);
CREATE INDEX people_group_idx ON people (group_id);
CREATE UNIQUE INDEX people_personnel_no_key ON people (personnel_no) WHERE personnel_no IS NOT NULL;
COMMENT ON TABLE people IS 'Таблица «номер → человек» (NFR-PR-01): ФИО, псевдоним, табельный номер, группа. При отзыве согласия строка удаляется в одной транзакции со стиранием ключа в subjects.';

-- ---------------------------------------------------------------------
-- Настройки портала и тексты согласий
-- ---------------------------------------------------------------------

CREATE TABLE portal_settings (
  id                       boolean     PRIMARY KEY DEFAULT true,
  admission_rehearsals     integer     NOT NULL DEFAULT 3,
  abandon_timeout_minutes  integer     NOT NULL DEFAULT 10,
  code_max_failed_attempts integer     NOT NULL DEFAULT 10,
  updated_at               timestamptz NOT NULL DEFAULT now(),
  updated_by               uuid        REFERENCES portal_users (id),
  CONSTRAINT portal_settings_singleton CHECK (id),
  CONSTRAINT portal_settings_admission CHECK (admission_rehearsals BETWEEN 1 AND 20),
  CONSTRAINT portal_settings_timeout CHECK (abandon_timeout_minutes BETWEEN 2 AND 120),
  CONSTRAINT portal_settings_attempts CHECK (code_max_failed_attempts BETWEEN 3 AND 50)
);
INSERT INTO portal_settings DEFAULT VALUES;
COMMENT ON TABLE portal_settings IS 'Одна строка: порог репетиций для допуска к оценке (FR-SC-11), таймаут брошенной сессии (FR-ST-03), число неудачных вводов кода до блокировки (NFR-S-02). Демо-режим — переменная окружения, не здесь.';

CREATE TABLE consent_texts (
  id          uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  kind        consent_kind NOT NULL,
  version     text         NOT NULL,
  body        text         NOT NULL,
  body_sha256 text         NOT NULL,
  created_at  timestamptz  NOT NULL DEFAULT now(),
  created_by  uuid         REFERENCES portal_users (id),
  CONSTRAINT consent_texts_kind CHECK (kind <> 'written_assessment'),
  CONSTRAINT consent_texts_hash CHECK (body_sha256 = encode(sha256(convert_to(body, 'UTF8')), 'hex')),
  UNIQUE (kind, version)
);
COMMENT ON TABLE consent_texts IS 'Версии текстов согласия и уведомлений (arena-consent-texts.md, раздел 0). Действует последняя версия своего вида. Тексты не правятся — только новая версия.';

-- ---------------------------------------------------------------------
-- Сценарии, версии, репетиции
-- ---------------------------------------------------------------------

CREATE TABLE scenarios (
  id                uuid            PRIMARY KEY DEFAULT gen_random_uuid(),
  slug              text            NOT NULL,
  mode              scenario_mode   NOT NULL,
  origin            scenario_origin NOT NULL,
  draft_document    jsonb,
  draft_fingerprint text,
  draft_check       jsonb,
  draft_updated_at  timestamptz,
  draft_updated_by  uuid            REFERENCES portal_users (id),
  generation        jsonb,
  archived_at       timestamptz,
  created_at        timestamptz     NOT NULL DEFAULT now(),
  created_by        uuid            NOT NULL REFERENCES portal_users (id),
  CONSTRAINT scenarios_slug_format CHECK (slug ~ '^[a-z0-9_]{1,48}$'),
  CONSTRAINT scenarios_draft_pair CHECK ((draft_document IS NULL) = (draft_fingerprint IS NULL)),
  CONSTRAINT scenarios_draft_fingerprint CHECK (draft_fingerprint ~ '^[0-9a-f]{64}$'),
  CONSTRAINT scenarios_draft_object CHECK (jsonb_typeof(draft_document) = 'object'),
  CONSTRAINT scenarios_draft_mode CHECK (draft_document IS NULL OR coalesce(draft_document #>> '{passport,mode}' = mode::text, false)),
  CONSTRAINT scenarios_draft_slug CHECK (draft_document IS NULL OR coalesce(draft_document #>> '{passport,id}' = slug, false)),
  CONSTRAINT scenarios_has_content CHECK (draft_document IS NOT NULL OR generation IS NOT NULL),
  CONSTRAINT scenarios_generation_object CHECK (jsonb_typeof(generation) = 'object'),
  CONSTRAINT scenarios_id_mode_key UNIQUE (id, mode)
);
-- slug не уникален: импорт на тот же портал даёт второй сценарий с тем же passport.id (FR-SC-14)
CREATE INDEX scenarios_slug_idx ON scenarios (slug);
COMMENT ON TABLE scenarios IS 'Сценарий в библиотеке (FR-SC-01, FR-SC-14) и его текущий черновик. Название, сфера, тип переговоров и теги не дублируются: библиотека читает их из draft_document->''passport'', а у генерации без черновика — из generation->''questionnaire''. Режим — здесь; с первой публикации он заблокирован внешним ключом из scenario_versions (FR-MD-01).';
COMMENT ON COLUMN scenarios.mode IS 'Режим сценария. UPDATE при наличии хотя бы одной опубликованной версии падает на внешнем ключе scenario_versions_mode_locks_scenario (FR-MD-01).';
COMMENT ON COLUMN scenarios.draft_fingerprint IS 'Отпечаток черновика по разделу 19 формата (SHA-256 от JCS без passport.version, tags, origin и authoring). Считает пакет движка при каждом сохранении.';
COMMENT ON COLUMN scenarios.generation IS 'Ход генерации из брифа: анкета, шаг, состояние, ошибка. Задание живёт в памяти процесса; при старте портал переводит оставшиеся running в failed («Портал перезапускался — повторите шаг»).';

CREATE TABLE scenario_versions (
  id                   uuid          PRIMARY KEY DEFAULT gen_random_uuid(),
  scenario_id          uuid          NOT NULL,
  number               integer       NOT NULL,
  fingerprint          text          NOT NULL,
  format               text          NOT NULL,
  engine_version       text          NOT NULL,
  document             jsonb         NOT NULL,
  mode                 scenario_mode NOT NULL,
  title                text          NOT NULL,
  sphere               text          NOT NULL,
  negotiation_type     text          NOT NULL,
  admission_rehearsals integer,
  admission_required   integer,
  published_by         uuid          NOT NULL REFERENCES portal_users (id),
  published_at         timestamptz   NOT NULL DEFAULT now(),
  CONSTRAINT scenario_versions_number CHECK (number >= 1),
  CONSTRAINT scenario_versions_fingerprint CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
  CONSTRAINT scenario_versions_format CHECK (format ~ '^arena-scenario/[0-9]+$'),
  CONSTRAINT scenario_versions_document_object CHECK (jsonb_typeof(document) = 'object'),
  CONSTRAINT scenario_versions_doc_number CHECK (coalesce(document #>> '{passport,version}' = number::text, false)),
  CONSTRAINT scenario_versions_doc_mode CHECK (coalesce(document #>> '{passport,mode}' = mode::text, false)),
  CONSTRAINT scenario_versions_doc_title CHECK (coalesce(document #>> '{passport,title}' = title, false)),
  CONSTRAINT scenario_versions_doc_sphere CHECK (coalesce(document #>> '{passport,sphere}' = sphere, false)),
  CONSTRAINT scenario_versions_doc_type CHECK (coalesce(document #>> '{passport,negotiation_type}' = negotiation_type, false)),
  CONSTRAINT scenario_versions_doc_format CHECK (coalesce(document ->> 'format' = format, false)),
  CONSTRAINT scenario_versions_admission CHECK (
    mode = 'training'
    OR (admission_rehearsals IS NOT NULL
        AND admission_required IS NOT NULL
        AND admission_rehearsals >= admission_required)),
  CONSTRAINT scenario_versions_number_key UNIQUE (scenario_id, number),
  CONSTRAINT scenario_versions_fingerprint_key UNIQUE (scenario_id, fingerprint),
  CONSTRAINT scenario_versions_id_scenario_mode_key UNIQUE (id, scenario_id, mode),
  -- FR-MD-01: с первой публикации режим сценария не меняется
  CONSTRAINT scenario_versions_mode_locks_scenario FOREIGN KEY (scenario_id, mode)
    REFERENCES scenarios (id, mode) ON UPDATE RESTRICT
);
COMMENT ON TABLE scenario_versions IS 'Опубликованные версии сценария (FR-SC-09). Неизменяемы: у роли приложения нет UPDATE и DELETE. Назначения и сессии ссылаются на версию, а не на сценарий. Первая версия фиксирует режим сценария (FR-MD-01).';
COMMENT ON COLUMN scenario_versions.admission_rehearsals IS 'Сколько засчитанных репетиций было ровно на этом отпечатке в момент публикации (FR-SC-11). Для оценки не меньше admission_required.';

CREATE TABLE rehearsals (
  id                   uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
  scenario_id          uuid             NOT NULL REFERENCES scenarios (id),
  fingerprint          text             NOT NULL,
  difficulty           difficulty_level NOT NULL,
  trainer_profile_id   uuid             NOT NULL REFERENCES trainer_profiles (id),
  issued_by            uuid             NOT NULL REFERENCES portal_users (id),
  issued_at            timestamptz      NOT NULL DEFAULT now(),
  link_hash            bytea            NOT NULL UNIQUE,
  link_expires_at      timestamptz      NOT NULL,
  redeemed_at          timestamptz,
  profile_revision     integer,
  uploaded_at          timestamptz,
  engine_version       text,
  record               jsonb,
  result               jsonb,
  final_id             text,
  result_rank          final_rank,
  counts_for_admission boolean          GENERATED ALWAYS AS (uploaded_at IS NOT NULL) STORED,
  CONSTRAINT rehearsals_fingerprint CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
  CONSTRAINT rehearsals_link_len CHECK (octet_length(link_hash) = 32),
  CONSTRAINT rehearsals_link_expiry CHECK (link_expires_at > issued_at),
  CONSTRAINT rehearsals_redeemed_in_time CHECK (redeemed_at IS NULL OR redeemed_at BETWEEN issued_at AND link_expires_at),
  CONSTRAINT rehearsals_redeem_revision CHECK ((redeemed_at IS NULL) = (profile_revision IS NULL)),
  CONSTRAINT rehearsals_upload_after_redeem CHECK (uploaded_at IS NULL OR (redeemed_at IS NOT NULL AND uploaded_at >= redeemed_at)),
  CONSTRAINT rehearsals_upload_fields CHECK (
    (uploaded_at IS NULL) = (record IS NULL)
    AND (uploaded_at IS NULL) = (result IS NULL)
    AND (uploaded_at IS NULL) = (engine_version IS NULL)),
  CONSTRAINT rehearsals_record_object CHECK (jsonb_typeof(record) = 'object'),
  CONSTRAINT rehearsals_final_after_upload CHECK (uploaded_at IS NOT NULL OR (final_id IS NULL AND result_rank IS NULL))
);
CREATE INDEX rehearsals_admission_idx ON rehearsals (scenario_id, fingerprint)
  WHERE counts_for_admission;
COMMENT ON TABLE rehearsals IS 'Репетиции автора (FR-SC-10). Идут в тренажёре: строка появляется при выдаче одноразовой ссылки, погашается тренажёром и получает запись с разбором. Не сессии, в статистику не входят. Допуск к оценке — число строк с counts_for_admission на отпечатке черновика (FR-SC-11).';
COMMENT ON COLUMN rehearsals.link_hash IS 'HMAC-SHA256 токена ссылки с секретом сервера; самого токена в базе нет. Ссылка работает один раз: погашение — UPDATE … SET redeemed_at = now() WHERE link_hash = $1 AND redeemed_at IS NULL AND link_expires_at > now().';
COMMENT ON COLUMN rehearsals.issued_by IS 'Кто получил ссылку и репетирует. Токен репетиции привязан к нему: отключённый пользователь или пользователь без роли методолога или администратора ссылку не погасит.';
COMMENT ON COLUMN rehearsals.record IS 'Запись репетиции от тренажёра как пришла: реплики, отказы компонентов, разбор каждой реплики с текстом запроса к оппоненту, финал, ответ судьи. Тексты — автора сценария, не участника: не шифруются.';
COMMENT ON COLUMN rehearsals.result IS 'Обе оценки по отдельности — пересчёт портала функциями пакета движка по записи.';
COMMENT ON COLUMN rehearsals.counts_for_admission IS 'Идёт ли в допуск своего отпечатка: запись загружена. Отпечаток ссылки не меняется, поэтому правка черновика во время репетиции её не портит.';

-- ---------------------------------------------------------------------
-- Назначения и коды доступа
-- ---------------------------------------------------------------------

CREATE TABLE assignments (
  id                  uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_id          uuid             NOT NULL REFERENCES subjects (id),
  group_id            uuid             NOT NULL REFERENCES employee_groups (id),
  scenario_version_id uuid             NOT NULL REFERENCES scenario_versions (id),
  difficulty          difficulty_level NOT NULL,
  trainer_profile_id  uuid             NOT NULL REFERENCES trainer_profiles (id),
  due_at              timestamptz      NOT NULL,
  batch_id            uuid,
  created_by          uuid             NOT NULL REFERENCES portal_users (id),
  created_at          timestamptz      NOT NULL DEFAULT now(),
  cancelled_at        timestamptz,
  cancelled_by        uuid             REFERENCES portal_users (id),
  cancel_reason       text,
  CONSTRAINT assignments_cancel_pair CHECK ((cancelled_at IS NULL) = (cancel_reason IS NULL)),
  CONSTRAINT assignments_cancel_reason CHECK (cancel_reason IN ('by_hr', 'consent_withdrawn'))
);
CREATE INDEX assignments_group_created_idx ON assignments (group_id, created_at DESC);
CREATE INDEX assignments_subject_idx ON assignments (subject_id);
CREATE INDEX assignments_version_idx ON assignments (scenario_version_id);
COMMENT ON TABLE assignments IS 'Назначение (FR-AC-01): версия + сложность + адресат + профиль тренажёра + срок; режим берётся из версии. group_id — группа на момент назначения, по ней проверяется доступ к сессиям.';

CREATE TABLE access_codes (
  id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  assignment_id   uuid        NOT NULL REFERENCES assignments (id),
  selector_hash   bytea       NOT NULL,
  code_hash       bytea       NOT NULL UNIQUE,
  issued_by       uuid        REFERENCES portal_users (id),
  issued_at       timestamptz NOT NULL DEFAULT now(),
  failed_attempts integer     NOT NULL DEFAULT 0,
  last_failed_at  timestamptz,
  blocked_at      timestamptz,
  revoked_at      timestamptz,
  revoke_reason   text,
  CONSTRAINT access_codes_selector_len CHECK (octet_length(selector_hash) = 32),
  CONSTRAINT access_codes_code_len CHECK (octet_length(code_hash) = 32),
  CONSTRAINT access_codes_attempts CHECK (failed_attempts >= 0),
  CONSTRAINT access_codes_revoke_pair CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL)),
  CONSTRAINT access_codes_revoke_reason CHECK (revoke_reason IN ('reissued', 'assignment_cancelled', 'consent_withdrawn'))
);
CREATE UNIQUE INDEX access_codes_one_active ON access_codes (assignment_id) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX access_codes_active_selector ON access_codes (selector_hash) WHERE revoked_at IS NULL;
COMMENT ON TABLE access_codes IS 'Коды ARENA-XXXX-XXXX-XXX (FR-AC-02). Кода в открытом виде нет: только HMAC-SHA256 с секретом сервера. Перевыпуск — отзыв старой строки и новая строка в одной транзакции (FR-AC-12).';
COMMENT ON COLUMN access_codes.selector_hash IS 'HMAC первых четырёх символов кода. По нему находится код, к которому относится неудачная попытка, — иначе попытки не с чем считать (NFR-S-02).';

-- ---------------------------------------------------------------------
-- Согласия (FR-AC-06, FR-AC-07, FR-AC-13)
-- ---------------------------------------------------------------------

CREATE TABLE consent_records (
  id               uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_id       uuid           NOT NULL REFERENCES subjects (id),
  assignment_id    uuid           REFERENCES assignments (id),
  kind             consent_kind   NOT NULL,
  answer           consent_answer NOT NULL,
  usable           boolean        GENERATED ALWAYS AS (answer <> 'refused') STORED,
  text_id          uuid           REFERENCES consent_texts (id),
  shown_text_hmac  bytea,
  document_ref     text,
  document_channel text,
  signed_on        date,
  valid_until      date,
  recorded_by      uuid           REFERENCES portal_users (id),
  answered_at      timestamptz    NOT NULL DEFAULT now(),
  CONSTRAINT consent_records_channel CHECK (document_channel IN ('paper', 'edo')),
  CONSTRAINT consent_records_on_screen CHECK (
    kind = 'written_assessment'
    OR (text_id IS NOT NULL
        AND shown_text_hmac IS NOT NULL
        AND octet_length(shown_text_hmac) = 32
        AND document_ref IS NULL
        AND recorded_by IS NULL)),
  CONSTRAINT consent_records_written CHECK (
    kind <> 'written_assessment'
    OR (text_id IS NULL
        AND shown_text_hmac IS NULL
        AND answer = 'granted'
        AND document_ref IS NOT NULL
        AND document_channel IS NOT NULL
        AND signed_on IS NOT NULL
        AND recorded_by IS NOT NULL)),
  CONSTRAINT consent_records_answer CHECK (
    (kind IN ('notice_training', 'notice_demo') AND answer = 'acknowledged')
    OR (kind IN ('consent_assessment', 'consent_external_ai', 'written_assessment') AND answer IN ('granted', 'refused'))),
  -- цель составных внешних ключей из sessions: запись того же участника и не отказ
  CONSTRAINT consent_records_usable_key UNIQUE (id, subject_id, usable)
);
CREATE INDEX consent_records_subject_idx ON consent_records (subject_id, kind, answered_at DESC);
CREATE INDEX consent_records_assignment_idx ON consent_records (assignment_id);
COMMENT ON TABLE consent_records IS 'Ответы участника на экране «Согласие и уведомление» (вид, версия текста, HMAC показанного текста, время, кто) и отметки HR о письменном согласии на бумаге или в ЭДО. Только добавление.';
COMMENT ON COLUMN consent_records.shown_text_hmac IS 'HMAC-SHA256 показанного текста (с ФИО и табельным номером) на ключе данных участника. Не простой хеш: шаблон известен, и перебор по списку сотрудников восстановил бы человека. После отзыва согласия ключ стёрт — связать запись с человеком нельзя (NFR-PR-01). Портал сверяет присланный клиентом SHA-256 со своим текстом, а пишет HMAC.';
COMMENT ON COLUMN consent_records.usable IS 'Запись годится как основание для сессии: не отказ. Вид и срок valid_until проверяет приложение.';

-- ---------------------------------------------------------------------
-- Сессии и ходы
-- ---------------------------------------------------------------------

CREATE TABLE sessions (
  id                       uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
  assignment_id            uuid             REFERENCES assignments (id),
  subject_id               uuid             NOT NULL REFERENCES subjects (id),
  group_id                 uuid             REFERENCES employee_groups (id),
  scenario_id              uuid             NOT NULL,
  scenario_version_id      uuid             NOT NULL,
  mode                     scenario_mode    NOT NULL,
  difficulty               difficulty_level NOT NULL,
  is_demo                  boolean          NOT NULL DEFAULT false,
  demo_guest_id            uuid,
  trainer_profile_id       uuid             NOT NULL REFERENCES trainer_profiles (id),
  profile_revision         integer          NOT NULL,
  profile_snapshot         jsonb            NOT NULL,
  engine_version           text             NOT NULL,
  criteria_set             text             NOT NULL,
  external_ai_allowed      boolean          NOT NULL,
  main_consent_id          uuid             NOT NULL,
  external_ai_consent_id   uuid,
  written_consent_id       uuid,
  consent_ok               boolean          NOT NULL DEFAULT true,
  status                   session_status   NOT NULL DEFAULT 'in_progress',
  started_at               timestamptz      NOT NULL DEFAULT now(),
  last_event_at            timestamptz      NOT NULL DEFAULT now(),
  ended_at                 timestamptz,
  break_stage              text,
  break_turn               integer,
  reopened_count           integer          NOT NULL DEFAULT 0,
  participant_turns        integer          NOT NULL DEFAULT 0,
  incidents                jsonb            NOT NULL DEFAULT '[]'::jsonb,
  simplified               boolean          NOT NULL DEFAULT false,
  stub_used                boolean          NOT NULL DEFAULT false,
  opponent_violation_count integer          NOT NULL DEFAULT 0,
  reviewed_at              timestamptz,
  reviewed_by              uuid             REFERENCES portal_users (id),
  reviewed_violation_count integer,
  final_id                 text,
  final_title              text,
  result_rank              final_rank,
  result_number            smallint,
  result_max               smallint,
  process_status           process_status   NOT NULL DEFAULT 'pending',
  process_number           smallint,
  criteria_bands           jsonb,
  scoring_profile          scoring_profile,
  lucky                    boolean          NOT NULL DEFAULT false,
  scores                   jsonb,
  scored_at                timestamptz,
  judge_answer_enc         bytea,
  judge_attempts           integer          NOT NULL DEFAULT 0,
  CONSTRAINT sessions_counters CHECK (reopened_count >= 0 AND participant_turns >= 0
                                      AND opponent_violation_count >= 0 AND judge_attempts >= 0),
  CONSTRAINT sessions_break_turn CHECK (break_turn >= 0),
  CONSTRAINT sessions_result_number CHECK (result_number BETWEEN 0 AND 100),
  CONSTRAINT sessions_result_max CHECK (result_max BETWEEN 0 AND 100),
  CONSTRAINT sessions_process_range CHECK (process_number BETWEEN 0 AND 100),
  CONSTRAINT sessions_json_shapes CHECK (jsonb_typeof(profile_snapshot) = 'object'
                                         AND jsonb_typeof(incidents) = 'array'),
  CONSTRAINT sessions_scores_object CHECK (jsonb_typeof(scores) = 'object'),
  CONSTRAINT sessions_bands_object CHECK (jsonb_typeof(criteria_bands) = 'object'),
  -- FR-AC-10: демо — без назначения, без группы, только тренировка; гость различается своим номером
  CONSTRAINT sessions_demo_no_assignment CHECK (is_demo = (assignment_id IS NULL)),
  CONSTRAINT sessions_demo_no_group CHECK (is_demo = (group_id IS NULL)),
  CONSTRAINT sessions_demo_training CHECK (NOT is_demo OR mode = 'training'),
  CONSTRAINT sessions_demo_guest CHECK (is_demo = (demo_guest_id IS NOT NULL)),
  -- FR-AC-06, FR-AC-07, FR-AC-13: без записи ответа сессия не создаётся — ни в работе, ни в демо.
  -- Три составных ключа: запись того же участника и не отказ (consent_records.usable).
  CONSTRAINT sessions_consent_ok CHECK (consent_ok),
  CONSTRAINT sessions_written_consent CHECK (mode = 'training' OR written_consent_id IS NOT NULL),
  CONSTRAINT sessions_external_ai CHECK (NOT external_ai_allowed OR external_ai_consent_id IS NOT NULL),
  CONSTRAINT sessions_main_consent FOREIGN KEY (main_consent_id, subject_id, consent_ok)
    REFERENCES consent_records (id, subject_id, usable),
  CONSTRAINT sessions_external_ai_consent FOREIGN KEY (external_ai_consent_id, subject_id, consent_ok)
    REFERENCES consent_records (id, subject_id, usable),
  CONSTRAINT sessions_written_consent_ref FOREIGN KEY (written_consent_id, subject_id, consent_ok)
    REFERENCES consent_records (id, subject_id, usable),
  -- FR-ST-03: идущая сессия не имеет времени окончания; закрытая сервером — без оценок
  CONSTRAINT sessions_open_state CHECK ((status = 'in_progress') = (ended_at IS NULL)),
  CONSTRAINT sessions_abandoned_no_grade CHECK (status <> 'abandoned' OR (result_rank IS NULL AND process_number IS NULL)),
  -- FR-RS-04: буква только вместе с финалом; «процесс» — число только при полученном разборе
  CONSTRAINT sessions_rank_with_final CHECK ((result_rank IS NULL) = (final_id IS NULL)),
  CONSTRAINT sessions_process_number CHECK ((process_number IS NOT NULL) = (process_status = 'ok')),
  CONSTRAINT sessions_bands_with_process CHECK ((criteria_bands IS NULL) = (process_number IS NULL)),
  -- FR-RS-13, NFR-R-02: «просмотрено» — с именем, числом просмотренных нарушений,
  -- только у закрытой сессии и только там, где есть что смотреть
  CONSTRAINT sessions_review_pair CHECK ((reviewed_at IS NULL) = (reviewed_by IS NULL)
                                         AND (reviewed_at IS NULL) = (reviewed_violation_count IS NULL)),
  CONSTRAINT sessions_review_count CHECK (reviewed_violation_count BETWEEN 1 AND opponent_violation_count),
  CONSTRAINT sessions_review_needed CHECK (reviewed_at IS NULL OR (opponent_violation_count > 0 AND NOT simplified)),
  CONSTRAINT sessions_review_closed CHECK (reviewed_at IS NULL OR status NOT IN ('in_progress', 'abandoned')),
  -- версия принадлежит этому сценарию и имеет тот же режим; режим сценария держит scenario_versions (FR-MD-01)
  CONSTRAINT sessions_version_matches FOREIGN KEY (scenario_version_id, scenario_id, mode)
    REFERENCES scenario_versions (id, scenario_id, mode),
  CONSTRAINT sessions_id_mode_key UNIQUE (id, mode)
);
-- FR-AC-03: по назначению одновременно идёт не больше одной сессии
CREATE UNIQUE INDEX sessions_one_running_per_assignment ON sessions (assignment_id) WHERE status = 'in_progress';
-- оценочное назначение — одна попытка; новая попытка — новое назначение
CREATE UNIQUE INDEX sessions_one_assessment_per_assignment ON sessions (assignment_id) WHERE mode = 'assessment';
-- один ответ на согласие — одна попытка (arena-api.md, 4)
CREATE UNIQUE INDEX sessions_one_per_consent ON sessions (main_consent_id);
CREATE INDEX sessions_assignment_idx ON sessions (assignment_id);
CREATE INDEX sessions_scenario_idx ON sessions (scenario_id);
CREATE INDEX sessions_group_started_idx ON sessions (group_id, started_at DESC) WHERE NOT is_demo;
CREATE INDEX sessions_subject_started_idx ON sessions (subject_id, started_at DESC);
CREATE INDEX sessions_version_conditions_idx ON sessions (scenario_version_id, difficulty, scoring_profile) WHERE NOT is_demo;
CREATE INDEX sessions_running_idx ON sessions (last_event_at) WHERE status = 'in_progress';
CREATE INDEX sessions_demo_guest_idx ON sessions (demo_guest_id, started_at DESC) WHERE is_demo;
COMMENT ON TABLE sessions IS 'Запись сессии (FR-RS-01): условия (версия, сложность, режим, профиль тренажёра с ревизией и снимком, версия движка, набор критериев), статус и точка обрыва (FR-ST-03), пометки (NFR-R-02, FR-RS-13), обе оценки отдельными столбцами — общего числа нет (FR-RS-04).';
COMMENT ON COLUMN sessions.scores IS 'Блок оценок из arena-scoring.md, раздел 9, в расчёте портала. Без цитат: эпизоды указывают на номер реплики.';
COMMENT ON COLUMN sessions.judge_answer_enc IS 'Ответ судьи участника после разговора как пришёл (эпизоды с цитатами, резюме), зашифрован ключом участника.';
COMMENT ON COLUMN sessions.profile_snapshot IS 'Настройки профиля тренажёра, с которыми прошла сессия, без ключей (FR-PF-03).';
COMMENT ON COLUMN sessions.demo_guest_id IS 'Номер гостя демо из гостевого токена. Все демо-сессии пишутся на одного демо-участника; гость видит только сессии со своим номером.';
COMMENT ON COLUMN sessions.consent_ok IS 'Всегда true. Нужен только как третий столбец составных внешних ключей на consent_records (id, subject_id, usable).';
COMMENT ON COLUMN sessions.reviewed_violation_count IS 'Сколько нарушений оппонента было на момент отметки «просмотрено». Новые нарушения из поздних ходов снова делают сессию непригодной для решения, пока HR не посмотрит ещё раз.';

CREATE TABLE turns (
  session_id                 uuid         NOT NULL REFERENCES sessions (id),
  seq                        integer      NOT NULL,
  speaker                    speaker_role NOT NULL,
  reply_no                   integer      NOT NULL,
  text_enc                   bytea,
  at_ms                      integer      NOT NULL,
  duration_ms                integer,
  pause_ms                   integer,
  model_route                model_route,
  stage                      text         NOT NULL,
  transition_to              text,
  move_type                  text,
  move_source                move_source,
  judge_confidence           real,
  evidence                   text,
  interest                   text,
  violations                 text[]       NOT NULL DEFAULT '{}',
  judge                      jsonb,
  terms                      jsonb,
  trust                      smallint,
  pressure                   smallint,
  credibility                smallint,
  patience                   smallint,
  credit                     integer,
  revealed_facts             text[]       NOT NULL DEFAULT '{}',
  engine_step                jsonb,
  intent                     text,
  offer                      jsonb,
  opponent_judge             jsonb,
  opponent_violation         boolean      NOT NULL DEFAULT false,
  interrupted                boolean      NOT NULL DEFAULT false,
  opponent_judge_comment_enc bytea,
  received_at                timestamptz  NOT NULL DEFAULT now(),
  PRIMARY KEY (session_id, seq),
  CONSTRAINT turns_reply_key UNIQUE (session_id, speaker, reply_no),
  CONSTRAINT turns_seq CHECK (seq >= 0),
  CONSTRAINT turns_reply_no CHECK (reply_no >= 0 AND (speaker = 'opponent' OR reply_no >= 1)),
  CONSTRAINT turns_timing CHECK (at_ms >= 0 AND duration_ms >= 0 AND pause_ms >= 0),
  CONSTRAINT turns_confidence CHECK (judge_confidence BETWEEN 0 AND 1),
  CONSTRAINT turns_participant_only CHECK (speaker = 'participant'
    OR (move_type IS NULL AND move_source IS NULL AND judge IS NULL AND terms IS NULL)),
  CONSTRAINT turns_opponent_only CHECK (speaker = 'opponent'
    OR (intent IS NULL AND offer IS NULL AND opponent_judge IS NULL AND NOT opponent_violation AND NOT interrupted)),
  -- пояснение судьи оппонента может цитировать разговор: только шифром в opponent_judge_comment_enc
  CONSTRAINT turns_opponent_judge_no_comment CHECK (NOT (opponent_judge ? 'comment')),
  CONSTRAINT turns_move_has_source CHECK ((move_type IS NULL) = (move_source IS NULL))
);
COMMENT ON TABLE turns IS 'Лог ходов (FR-RS-01, FR-ST-08): каждая реплика с решением судьи, шкалами, предложениями, этапом и таймингами. Только добавление; повторная доставка из буфера клиента отбрасывается по первичному ключу (FR-AC-11). Поля для метки эмоции нет (FR-RS-02).';
COMMENT ON COLUMN turns.text_enc IS 'Текст реплики, зашифрованный ключом участника. NULL — ход пришёл после уничтожения ключа, текст не сохранён.';
COMMENT ON COLUMN turns.interrupted IS 'Реплику оппонента перебили (или нажали «Завершить», пока она звучала). Перебитый ответ-согласие сделку не заключает (arena-scoring 3.4) — без этого признака пересчёт засчитал бы сделку, которой не было.';
COMMENT ON COLUMN turns.engine_step IS 'Пошаговый разбор хода от движка (NFR-M-02) без текста запроса к оппоненту: в нём метка эмоции.';

-- ---------------------------------------------------------------------
-- Возражения и кадровые решения
-- ---------------------------------------------------------------------

CREATE TABLE objections (
  id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id   uuid        NOT NULL REFERENCES sessions (id),
  subject_id   uuid        NOT NULL REFERENCES subjects (id),
  target       jsonb,
  text_enc     bytea       NOT NULL,
  submitted_at timestamptz NOT NULL DEFAULT now(),
  response_enc bytea,
  responded_by uuid        REFERENCES portal_users (id),
  responded_at timestamptz,
  CONSTRAINT objections_response_parts CHECK ((response_enc IS NULL) = (responded_at IS NULL)
                                              AND (responded_at IS NULL) = (responded_by IS NULL))
);
CREATE INDEX objections_session_idx ON objections (session_id);
CREATE INDEX objections_open_idx ON objections (submitted_at) WHERE responded_at IS NULL;
COMMENT ON TABLE objections IS 'Возражения участника к результату и ответ HR (FR-RS-09, UC-P-10, UC-H-12). Тексты зашифрованы ключом участника.';

CREATE TABLE hr_decisions (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_id    uuid        NOT NULL REFERENCES subjects (id),
  decision      text        NOT NULL,
  grounds       text        NOT NULL,
  decided_by    uuid        NOT NULL REFERENCES portal_users (id),
  decided_at    timestamptz NOT NULL DEFAULT now(),
  supersedes_id uuid        REFERENCES hr_decisions (id),
  CONSTRAINT hr_decisions_decision_filled CHECK (decision ~ '[A-Za-zА-Яа-яЁё]'),
  CONSTRAINT hr_decisions_grounds_filled CHECK (grounds ~ '[A-Za-zА-Яа-яЁё]')
);
CREATE INDEX hr_decisions_subject_idx ON hr_decisions (subject_id, decided_at DESC);
COMMENT ON TABLE hr_decisions IS 'Кадровое решение (FR-RS-08): кто, когда, что решено и собственные основания текстом. Не правится: пересмотр — новая запись со ссылкой supersedes_id.';
COMMENT ON COLUMN hr_decisions.grounds IS 'Основания принимающего лица своими словами. Обязательны: в тексте должна быть хотя бы одна буква. Никогда не предзаполняются текстом резюме.';

CREATE TABLE hr_decision_sessions (
  decision_id  uuid          NOT NULL REFERENCES hr_decisions (id),
  session_id   uuid          NOT NULL,
  session_mode scenario_mode NOT NULL DEFAULT 'assessment',
  PRIMARY KEY (decision_id, session_id),
  CONSTRAINT hr_decision_sessions_assessment_only CHECK (session_mode = 'assessment'),
  CONSTRAINT hr_decision_sessions_session FOREIGN KEY (session_id, session_mode)
    REFERENCES sessions (id, mode)
);
CREATE INDEX hr_decision_sessions_session_idx ON hr_decision_sessions (session_id);
COMMENT ON TABLE hr_decision_sessions IS 'На какие сессии опирается решение. Ключ (session_id, session_mode) с постоянным режимом «оценка»: ссылка на тренировочную сессию падает на внешнем ключе (FR-MD-02).';

-- FR-RS-13, NFR-R-02: сессия пригодна для решения, только если она завершена,
-- не в упрощённом режиме, а нарушения оппонента просмотрены человеком.
CREATE FUNCTION check_decision_session() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  s                arena.sessions%ROWTYPE;
  decision_subject uuid;
BEGIN
  SELECT * INTO s FROM arena.sessions WHERE id = NEW.session_id;
  IF NOT FOUND THEN
    RETURN NEW;  -- об отсутствии сессии скажет внешний ключ
  END IF;
  SELECT subject_id INTO decision_subject FROM arena.hr_decisions WHERE id = NEW.decision_id;
  IF NOT FOUND THEN
    RETURN NEW;  -- об отсутствии решения скажет внешний ключ
  END IF;
  IF decision_subject <> s.subject_id THEN
    RAISE EXCEPTION 'Сессия относится к другому сотруднику'
      USING ERRCODE = 'check_violation';
  END IF;
  IF s.status IN ('in_progress', 'abandoned') THEN
    RAISE EXCEPTION 'Сессия не завершена — на неё нельзя опереться в кадровом решении'
      USING ERRCODE = 'check_violation';
  END IF;
  IF s.simplified THEN
    RAISE EXCEPTION 'Сессия прошла в упрощённом режиме и непригодна для кадрового решения'
      USING ERRCODE = 'check_violation';
  END IF;
  IF s.opponent_violation_count > 0
     AND s.opponent_violation_count IS DISTINCT FROM s.reviewed_violation_count THEN
    RAISE EXCEPTION 'Оппонент нарушил правила: сначала сессию должен просмотреть HR'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER hr_decision_sessions_check
  BEFORE INSERT ON hr_decision_sessions
  FOR EACH ROW EXECUTE FUNCTION check_decision_session();

-- FR-RS-08: у решения есть хотя бы одна сессия. Проверка в конце транзакции,
-- потому что решение и его ссылки вставляются одной транзакцией.
CREATE FUNCTION check_decision_has_session() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM arena.hr_decision_sessions WHERE decision_id = NEW.id) THEN
    RAISE EXCEPTION 'Кадровое решение должно ссылаться хотя бы на одну оценочную сессию'
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER hr_decisions_need_session
  AFTER INSERT ON hr_decisions
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION check_decision_has_session();

-- ---------------------------------------------------------------------
-- Журнал (NFR-S-03): только добавление, без внешних ключей
-- ---------------------------------------------------------------------

CREATE TABLE audit_log (
  id            uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  occurred_at   timestamptz  NOT NULL DEFAULT clock_timestamp(),
  actor_kind    text         NOT NULL,
  actor_user_id uuid,
  action        audit_action NOT NULL,
  outcome       text         NOT NULL DEFAULT 'ok',
  subject_id    uuid,
  session_id    uuid,
  group_id      uuid,
  rows_count    integer,
  details       jsonb        NOT NULL DEFAULT '{}'::jsonb,
  CONSTRAINT audit_log_actor_kind CHECK (actor_kind IN ('user', 'participant', 'system')),
  CONSTRAINT audit_log_actor_user CHECK ((actor_kind = 'user') = (actor_user_id IS NOT NULL)),
  CONSTRAINT audit_log_outcome CHECK (outcome IN ('ok', 'denied')),
  CONSTRAINT audit_log_rows CHECK (rows_count >= 0),
  CONSTRAINT audit_log_details_object CHECK (jsonb_typeof(details) = 'object')
);
CREATE INDEX audit_log_time_idx ON audit_log (occurred_at DESC);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_user_id, occurred_at DESC) WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_log_group_idx ON audit_log (group_id, occurred_at DESC) WHERE group_id IS NOT NULL;
CREATE INDEX audit_log_subject_idx ON audit_log (subject_id, occurred_at DESC) WHERE subject_id IS NOT NULL;
COMMENT ON TABLE audit_log IS 'Журнал доступа и действий (NFR-S-03, FR-RS-10, FR-PF-04): кто, когда, что, чью сессию или какую группу, сколько строк. Участник — только обезличенным номером. У роли приложения нет UPDATE, DELETE и TRUNCATE.';

-- ---------------------------------------------------------------------
-- Права роли приложения
-- ---------------------------------------------------------------------

GRANT USAGE ON SCHEMA arena TO arena_app;

-- обычная работа: читать, добавлять, менять
GRANT SELECT, INSERT, UPDATE ON
  portal_users, employee_groups, subjects,
  scenarios, assignments, access_codes, sessions
TO arena_app;

-- репетиции: отпечаток, уровень, профиль, ссылка и кто её получил не меняются;
-- дописываются только погашение ссылки и запись от тренажёра
GRANT SELECT, INSERT ON rehearsals TO arena_app;
GRANT UPDATE (redeemed_at, profile_revision, uploaded_at, engine_version,
              record, result, final_id, result_rank)
  ON rehearsals TO arena_app;

-- профили: is_default не меняется никогда — профиль по умолчанию ровно один (FR-PF-05)
GRANT SELECT, INSERT ON trainer_profiles TO arena_app;
GRANT UPDATE (name, settings, revision,
              openrouter_key_enc, openrouter_key_last4,
              model_server_token_enc, model_server_token_last4,
              keys_changed_at, keys_changed_by, updated_at, updated_by, archived_at)
  ON trainer_profiles TO arena_app;

-- возражения: текст участника не правится, дописывается только ответ HR
GRANT SELECT, INSERT ON objections TO arena_app;
GRANT UPDATE (response_enc, responded_by, responded_at) ON objections TO arena_app;

-- удаление только там, где его требует вариант использования:
-- снятие доступа к группе (UC-A-06) и уничтожение связи «номер → человек» (UC-A-08)
GRANT SELECT, INSERT, UPDATE, DELETE ON user_group_access, people TO arena_app;

-- одна строка настроек: только читать и менять
GRANT SELECT, UPDATE ON portal_settings TO arena_app;

-- только добавление: версии, согласия, лог ходов, решения, журнал
GRANT SELECT, INSERT ON
  consent_texts, consent_records, scenario_versions, turns,
  hr_decisions, hr_decision_sessions, audit_log
TO arena_app;

GRANT SELECT ON trainer_profiles_public TO arena_app;

ALTER ROLE arena_app SET search_path TO arena;

COMMIT;
