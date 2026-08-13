-- Схема первого этапа: агентства, учётные записи сотрудников, туристы и заявки.
--
-- Изоляция между агентствами обеспечена двумя способами. Каждая доменная
-- таблица несёт agency_id, и каждый запрос добавляет его в WHERE, но запросы
-- пишутся руками, и забытое условие незаметно при код-ревью. Поэтому каждая
-- таблица дополнительно объявляет UNIQUE (id, agency_id), а каждая ссылка
-- между таблицами — составной внешний ключ (child_id, agency_id) -> parent
-- (id, agency_id). Тогда привязать туриста одного агентства к заявке другого
-- не получится независимо от того, что делает Go-код.
--
-- Оба расширения — trusted в PostgreSQL 13+, поэтому владелец базы данных
-- может создать их без прав суперпользователя.
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Поддерживает updated_at в актуальном состоянии, не заставляя каждый UPDATE
-- помнить об этом самостоятельно.
CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END
$$;

-- Агентства ------------------------------------------------------------------

CREATE TABLE agencies (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text        NOT NULL CHECK (length(btrim(name)) > 0 AND length(name) <= 200),
    inn         text        CHECK (inn ~ '^[0-9]{10}([0-9]{2})?$'),
    timezone    text        NOT NULL DEFAULT 'Europe/Moscow',
    is_active   boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

CREATE TRIGGER agencies_updated_at BEFORE UPDATE ON agencies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Счётчики на каждое агентство. Номера заявок начинаются заново с 1 для
-- каждого агентства: общая последовательность выдала бы, сколько дел ведут
-- соседи.
CREATE TABLE agency_sequences (
    agency_id  uuid   NOT NULL REFERENCES agencies (id) ON DELETE CASCADE,
    name       text   NOT NULL,
    last_value bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (agency_id, name)
);

-- Учётные записи сотрудников ---------------------------------------------------

-- email уникален глобально, поэтому для входа достаточно адреса и пароля —
-- без выбора агентства в форме входа.
CREATE TABLE users (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    agency_id     uuid        NOT NULL REFERENCES agencies (id) ON DELETE RESTRICT,
    email         citext      NOT NULL UNIQUE CHECK (position('@' IN email) > 1 AND length(email) <= 254),
    password_hash text        NOT NULL,
    full_name     text        NOT NULL CHECK (length(btrim(full_name)) > 0 AND length(full_name) <= 200),
    is_active     boolean     NOT NULL DEFAULT true,
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, agency_id)
);

CREATE INDEX users_agency_idx ON users (agency_id);

CREATE TRIGGER users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Хранится только SHA-256 от токена сессии, поэтому дамп базы данных не
-- отдаёт действующие сессии.
CREATE TABLE sessions (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid        NOT NULL,
    agency_id    uuid        NOT NULL,
    token_hash   bytea       NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    -- Нет столбца с IP-адресом: IP — персональные данные по 152-ФЗ, а на
    -- первом этапе нет политики их хранения. User agent достаточно, чтобы
    -- в будущем узнавать устройство на экране «выйти на всех устройствах».
    user_agent   text CHECK (length(user_agent) <= 255),
    FOREIGN KEY (user_id, agency_id) REFERENCES users (id, agency_id) ON DELETE CASCADE
);

CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

-- Справочники ------------------------------------------------------------------

-- Таблица, а не enum: в описании продукта перечислены сайт, реклама,
-- Telegram, VK, повторное обращение «и другие источники», поэтому агентства
-- должны иметь возможность добавлять свои.
CREATE TABLE acquisition_channels (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    agency_id  uuid        NOT NULL REFERENCES agencies (id) ON DELETE CASCADE,
    code       text        NOT NULL CHECK (code ~ '^[a-z0-9_]{1,32}$'),
    name       text        NOT NULL CHECK (length(btrim(name)) > 0),
    is_active  boolean     NOT NULL DEFAULT true,
    sort_order integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (agency_id, code),
    UNIQUE (id, agency_id)
);

CREATE TRIGGER acquisition_channels_updated_at BEFORE UPDATE ON acquisition_channels
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE partners (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    agency_id   uuid        NOT NULL REFERENCES agencies (id) ON DELETE CASCADE,
    kind        text        NOT NULL DEFAULT 'person' CHECK (kind IN ('person', 'company')),
    name        text        NOT NULL CHECK (length(btrim(name)) > 0 AND length(name) <= 200),
    phone       text,
    email       citext,
    note        text,
    is_active   boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,
    UNIQUE (id, agency_id)
);

CREATE UNIQUE INDEX partners_name_uk ON partners (agency_id, lower(name)) WHERE archived_at IS NULL;

CREATE TRIGGER partners_updated_at BEFORE UPDATE ON partners
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE tour_operators (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    agency_id     uuid        NOT NULL REFERENCES agencies (id) ON DELETE CASCADE,
    name          text        NOT NULL CHECK (length(btrim(name)) > 0 AND length(name) <= 200),
    inn           text        CHECK (inn ~ '^[0-9]{10}([0-9]{2})?$'),
    contact_phone text,
    contact_email citext,
    website       text,
    note          text,
    is_active     boolean     NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    archived_at   timestamptz,
    UNIQUE (id, agency_id)
);

CREATE UNIQUE INDEX tour_operators_name_uk ON tour_operators (agency_id, lower(name)) WHERE archived_at IS NULL;

CREATE TRIGGER tour_operators_updated_at BEFORE UPDATE ON tour_operators
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Туристы ------------------------------------------------------------------

-- Номера паспортов хранятся в открытом виде. Шифрование столбцов сломало бы
-- и поиск дублей, и обычный поиск, а сделать это правильно (отдельный ключ,
-- ротация, хранение ключа) — отдельная большая задача. Пока защита — это
-- контроль доступа к базе данных, шифрование тома и правило, что эти
-- значения никогда не попадают в логи.
--
-- У внутреннего российского паспорта нарочно нет срока действия: он
-- меняется в 20 и 45 лет, поэтому напоминание вычисляется из birth_date в
-- коде на Go.
CREATE TABLE tourists (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    agency_id   uuid        NOT NULL REFERENCES agencies (id) ON DELETE RESTRICT,

    last_name   text        NOT NULL CHECK (length(btrim(last_name)) > 0 AND length(last_name) <= 100),
    first_name  text        NOT NULL CHECK (length(btrim(first_name)) > 0 AND length(first_name) <= 100),
    middle_name text        CHECK (length(middle_name) <= 100),
    birth_date  date        CHECK (birth_date > date '1900-01-01'),
    gender      text        CHECK (gender IN ('male', 'female')),
    phone       text        CHECK (length(phone) <= 32),
    email       citext      CHECK (length(email) <= 254),

    -- Внутренний российский паспорт: серия из 4 цифр, номер из 6 цифр.
    passport_series        text CHECK (passport_series ~ '^[0-9]{4}$'),
    passport_number        text CHECK (passport_number ~ '^[0-9]{6}$'),
    passport_issued_by     text,
    passport_issue_date    date,
    passport_division_code text CHECK (passport_division_code ~ '^[0-9]{3}-[0-9]{3}$'),

    -- Загранпаспорт: номер из 9 цифр и транслитерация латиницей.
    intl_passport_number      text CHECK (intl_passport_number ~ '^[0-9]{9}$'),
    intl_passport_last_name   text CHECK (intl_passport_last_name ~ '^[A-Z][A-Z ''-]*$'),
    intl_passport_first_name  text CHECK (intl_passport_first_name ~ '^[A-Z][A-Z ''-]*$'),
    intl_passport_authority   text,
    intl_passport_issue_date  date,
    intl_passport_expiry_date date,

    acquisition_channel_id uuid,

    -- Рекомендатель — либо партнёр, либо другой турист, но не оба сразу.
    -- Два nullable-столбца вместо одного снимают циклическую зависимость
    -- с таблицей partners.
    referrer_partner_id uuid,
    referrer_tourist_id uuid,

    note        text,
    version     integer     NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    archived_at timestamptz,

    -- Триграммный поиск по имени и цифрам телефона. email намеренно не
    -- включён: это citext, и приведение типа здесь сделало бы выражение
    -- не-immutable, а генерируемый столбец этого не допускает.
    search_text text GENERATED ALWAYS AS (
        lower(last_name || ' ' || first_name || ' ' || coalesce(middle_name, ''))
        || ' ' || regexp_replace(coalesce(phone, ''), '[^0-9]', '', 'g')
    ) STORED,

    UNIQUE (id, agency_id),

    CONSTRAINT tourists_passport_pair_ck CHECK (
        (passport_series IS NULL) = (passport_number IS NULL)
    ),
    CONSTRAINT tourists_intl_dates_ck CHECK (
        intl_passport_expiry_date IS NULL
        OR intl_passport_issue_date IS NULL
        OR intl_passport_expiry_date > intl_passport_issue_date
    ),
    CONSTRAINT tourists_single_referrer_ck CHECK (
        referrer_partner_id IS NULL OR referrer_tourist_id IS NULL
    ),
    CONSTRAINT tourists_not_self_referrer_ck CHECK (referrer_tourist_id IS DISTINCT FROM id),

    FOREIGN KEY (acquisition_channel_id, agency_id)
        REFERENCES acquisition_channels (id, agency_id) ON DELETE SET NULL,
    FOREIGN KEY (referrer_partner_id, agency_id)
        REFERENCES partners (id, agency_id) ON DELETE SET NULL,
    FOREIGN KEY (referrer_tourist_id, agency_id)
        REFERENCES tourists (id, agency_id) ON DELETE SET NULL
);

-- Один и тот же человек не может быть заведён дважды в одном агентстве.
-- Архивные строки исключены, чтобы удалённого туриста можно было завести
-- заново.
CREATE UNIQUE INDEX tourists_passport_uk
    ON tourists (agency_id, passport_series, passport_number)
    WHERE passport_number IS NOT NULL AND archived_at IS NULL;

CREATE UNIQUE INDEX tourists_intl_passport_uk
    ON tourists (agency_id, intl_passport_number)
    WHERE intl_passport_number IS NOT NULL AND archived_at IS NULL;

CREATE INDEX tourists_name_idx ON tourists (agency_id, last_name, first_name) WHERE archived_at IS NULL;
CREATE INDEX tourists_search_idx ON tourists USING gin (search_text gin_trgm_ops);
CREATE INDEX tourists_email_idx ON tourists (agency_id, email) WHERE email IS NOT NULL;
CREATE INDEX tourists_channel_idx ON tourists (agency_id, acquisition_channel_id);
CREATE INDEX tourists_birth_idx ON tourists (agency_id, birth_date) WHERE archived_at IS NULL;

-- Обслуживает напоминания об истечении срока документов.
CREATE INDEX tourists_intl_expiry_idx
    ON tourists (agency_id, intl_passport_expiry_date)
    WHERE intl_passport_expiry_date IS NOT NULL AND archived_at IS NULL;

CREATE TRIGGER tourists_updated_at BEFORE UPDATE ON tourists
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Плательщики -----------------------------------------------------------------

-- Плательщик — это физическое лицо (часто один из туристов) либо юрлицо.
CREATE TABLE payers (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    agency_id       uuid        NOT NULL REFERENCES agencies (id) ON DELETE CASCADE,
    kind            text        NOT NULL CHECK (kind IN ('individual', 'company')),

    tourist_id      uuid,
    individual_name text,

    company_name    text,
    inn             text CHECK (inn ~ '^[0-9]{10}([0-9]{2})?$'),
    kpp             text CHECK (kpp ~ '^[0-9]{9}$'),
    ogrn            text CHECK (ogrn ~ '^[0-9]{13}([0-9]{2})?$'),
    legal_address   text,
    bank_details    text,

    contact_phone   text,
    contact_email   citext,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    archived_at     timestamptz,

    UNIQUE (id, agency_id),

    CONSTRAINT payers_identity_ck CHECK (
        (kind = 'company'
            AND company_name IS NOT NULL AND length(btrim(company_name)) > 0
            AND tourist_id IS NULL AND individual_name IS NULL)
        OR (kind = 'individual'
            AND company_name IS NULL AND inn IS NULL
            AND (tourist_id IS NOT NULL OR (individual_name IS NOT NULL AND length(btrim(individual_name)) > 0)))
    ),

    FOREIGN KEY (tourist_id, agency_id) REFERENCES tourists (id, agency_id) ON DELETE SET NULL
);

CREATE INDEX payers_agency_idx ON payers (agency_id, kind);
CREATE INDEX payers_tourist_idx ON payers (tourist_id) WHERE tourist_id IS NOT NULL;

CREATE TRIGGER payers_updated_at BEFORE UPDATE ON payers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Заявки ------------------------------------------------------------------

CREATE TABLE applications (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agency_id uuid NOT NULL REFERENCES agencies (id) ON DELETE RESTRICT,
    number    text NOT NULL,

    -- Семь стадий жизненного цикла из описания продукта. CHECK по тексту,
    -- а не enum PostgreSQL: добавить стадию позже — это миграция в одну
    -- строку без перезаписи таблицы, а ALTER TYPE ... ADD VALUE нельзя
    -- выполнить в той же транзакции, где значение используется.
    status text NOT NULL DEFAULT 'inquiry' CHECK (status IN (
        'inquiry', 'selection', 'approval', 'booked', 'preparation', 'completed', 'cancelled'
    )),
    status_changed_at timestamptz NOT NULL DEFAULT now(),

    customer_tourist_id    uuid NOT NULL,
    manager_user_id        uuid,
    payer_id               uuid,
    tour_operator_id       uuid,
    acquisition_channel_id uuid,

    country     text CHECK (length(country) <= 100),
    city        text,
    resort      text,
    hotel       text,
    depart_date date,
    return_date date,
    adults      smallint CHECK (adults BETWEEN 0 AND 30),
    children    smallint CHECK (children BETWEEN 0 AND 30),
    price_total numeric(14, 2) CHECK (price_total >= 0),
    currency    char(3) NOT NULL DEFAULT 'RUB' CHECK (currency ~ '^[A-Z]{3}$'),

    note          text,
    cancel_reason text,

    version     integer     NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    archived_at timestamptz,

    search_text text GENERATED ALWAYS AS (
        lower(number || ' ' || coalesce(country, '') || ' ' || coalesce(city, '')
              || ' ' || coalesce(resort, '') || ' ' || coalesce(hotel, ''))
    ) STORED,

    UNIQUE (id, agency_id),
    CONSTRAINT applications_number_uk UNIQUE (agency_id, number),
    CONSTRAINT applications_dates_ck CHECK (
        return_date IS NULL OR depart_date IS NULL OR return_date >= depart_date
    ),
    CONSTRAINT applications_cancel_reason_ck CHECK (
        status <> 'cancelled' OR cancel_reason IS NOT NULL
    ),

    FOREIGN KEY (customer_tourist_id, agency_id) REFERENCES tourists (id, agency_id),
    FOREIGN KEY (manager_user_id, agency_id) REFERENCES users (id, agency_id) ON DELETE SET NULL,
    FOREIGN KEY (payer_id, agency_id) REFERENCES payers (id, agency_id) ON DELETE SET NULL,
    FOREIGN KEY (tour_operator_id, agency_id) REFERENCES tour_operators (id, agency_id) ON DELETE SET NULL,
    FOREIGN KEY (acquisition_channel_id, agency_id)
        REFERENCES acquisition_channels (id, agency_id) ON DELETE SET NULL
);

CREATE INDEX applications_status_idx ON applications (agency_id, status, created_at DESC) WHERE archived_at IS NULL;
CREATE INDEX applications_depart_idx ON applications (agency_id, depart_date) WHERE archived_at IS NULL;
CREATE INDEX applications_customer_idx ON applications (agency_id, customer_tourist_id);
CREATE INDEX applications_operator_idx ON applications (agency_id, tour_operator_id);
CREATE INDEX applications_updated_idx ON applications (agency_id, updated_at DESC) WHERE archived_at IS NULL;
CREATE INDEX applications_search_idx ON applications USING gin (search_text gin_trgm_ops);

CREATE TRIGGER applications_updated_at BEFORE UPDATE ON applications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Сторона туриста — RESTRICT: турист, который уже поехал, не должен исчезать
-- из заявки. Удаление туриста — это мягкий архив, а не физическое удаление.
CREATE TABLE application_tourists (
    application_id uuid     NOT NULL,
    tourist_id     uuid     NOT NULL,
    agency_id      uuid     NOT NULL,
    role           text     NOT NULL DEFAULT 'tourist' CHECK (role IN ('customer', 'tourist')),
    position       smallint NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, tourist_id),
    FOREIGN KEY (application_id, agency_id) REFERENCES applications (id, agency_id) ON DELETE CASCADE,
    FOREIGN KEY (tourist_id, agency_id) REFERENCES tourists (id, agency_id) ON DELETE RESTRICT
);

-- Обслуживает список «связанных заявок» в карточке туриста.
CREATE INDEX application_tourists_tourist_idx ON application_tourists (tourist_id);

-- «Важные сроки» — строками, а не тремя столбцами, чтобы третий этап
-- (напоминания) мог вырасти из этой таблицы без структурной миграции.
CREATE TABLE application_deadlines (
    id             uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    application_id uuid NOT NULL,
    agency_id      uuid NOT NULL,
    kind           text NOT NULL CHECK (kind IN (
        'booking', 'payment', 'documents', 'visa', 'departure', 'return', 'other'
    )),
    due_date     date        NOT NULL,
    note         text,
    completed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (application_id, agency_id) REFERENCES applications (id, agency_id) ON DELETE CASCADE
);

CREATE INDEX application_deadlines_application_idx ON application_deadlines (application_id, due_date);
CREATE INDEX application_deadlines_due_idx ON application_deadlines (agency_id, due_date) WHERE completed_at IS NULL;

CREATE TRIGGER application_deadlines_updated_at BEFORE UPDATE ON application_deadlines
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- История изменений ------------------------------------------------------------

-- Записывается в той же транзакции, что и само изменение.
--
-- changes хранит {"field": {"from": ..., "to": ...}}. Персональные данные
-- никогда не попадают сюда значением: для полей, помеченных приложением как
-- чувствительные, в диффе с обеих сторон стоит "***" — факт правки виден,
-- а номер паспорта в журнал не попадает. То же правило, по которому
-- логгер запросов никогда не пишет query-строку.
--
-- actor_label намеренно денормализован: история должна оставаться читаемой
-- и после того, как пользователь, совершивший изменение, будет удалён.
CREATE TABLE entity_changes (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agency_id   uuid        NOT NULL REFERENCES agencies (id) ON DELETE CASCADE,
    entity_type text        NOT NULL CHECK (entity_type IN (
        'tourist', 'application', 'payer', 'partner', 'tour_operator', 'acquisition_channel', 'user', 'session'
    )),
    entity_id   uuid        NOT NULL,
    action      text        NOT NULL CHECK (action IN (
        'create', 'update', 'archive', 'restore', 'status_change', 'login', 'logout', 'login_failed'
    )),
    changes     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    summary     text,
    actor_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    actor_label text        NOT NULL DEFAULT '',
    request_id  text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX entity_changes_entity_idx
    ON entity_changes (agency_id, entity_type, entity_id, created_at DESC);
CREATE INDEX entity_changes_agency_idx
    ON entity_changes (agency_id, created_at DESC);
