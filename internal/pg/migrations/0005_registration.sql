-- Открытая саморегистрация агентств: email_verified_at — отдельная от
-- users.is_active колонка (тот флаг уже занят под отключение коллеги
-- администратором, internal/store/accounts.go SetUserActive). NULL —
-- регистрация ещё не подтверждена по почте.
--
-- Бэкфилл обязателен: без него единственный существующий продуктовый
-- пользователь из 0002_seed_data.sql внезапно окажется «неподтверждённым» и
-- не сможет войти. Начиная с этой миграции CreateUser (bootstrap и
-- POST /api/users — доверенные, уже аутентифицированные создатели) сам
-- проставляет email_verified_at = now(); NULL остаётся только у только что
-- самостоятельно зарегистрировавшихся пользователей.
ALTER TABLE users ADD COLUMN email_verified_at timestamptz;
UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL;

CREATE TABLE email_verification_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL,
    agency_id  uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    FOREIGN KEY (user_id, agency_id) REFERENCES users (id, agency_id) ON DELETE CASCADE
);

CREATE INDEX email_verification_tokens_user_idx ON email_verification_tokens (user_id);
