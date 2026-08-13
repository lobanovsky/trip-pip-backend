-- Начальные данные: первое (и пока единственное) агентство со всем, что
-- нужно, чтобы сразу начать работать — учётной записью, стандартными
-- каналами привлечения и списком туроператоров.
--
-- В отличие от переменных BOOTSTRAP_* (см. internal/store/bootstrap.go),
-- эти данные применяются вместе со схемой на любой базе данных: и в
-- проде, и локально, и в тестовой базе, которую поднимает go test.
-- BOOTSTRAP_* остаётся рабочим резервным путём — EnsureBootstrap
-- проверяет, есть ли уже пользователи, и ничего не делает, если эта
-- миграция уже всё создала.
WITH new_agency AS (
    INSERT INTO agencies (name, inn, timezone)
    VALUES ('ИП Фомина Валерия Александровна', '332708743406', 'Europe/Moscow')
    RETURNING id
), new_channels AS (
    INSERT INTO acquisition_channels (agency_id, code, name, sort_order)
    SELECT new_agency.id, c.code, c.name, c.sort_order
    FROM new_agency, (VALUES
        ('site',     'Сайт',                 10),
        ('ads',      'Реклама',               20),
        ('telegram', 'Telegram',              30),
        ('vk',       'VK',                    40),
        ('repeat',   'Повторное обращение',   50),
        ('referral', 'Рекомендация',          60),
        ('other',    'Другое',                70)
    ) AS c(code, name, sort_order)
    RETURNING 1
), new_user AS (
    INSERT INTO users (agency_id, email, password_hash, full_name)
    SELECT new_agency.id,
           'e.lobanovsky@ya.ru',
           '$argon2id$v=19$m=19456,t=2,p=1$pWdoCQIPo1BOwpFsjY2stQ$SF2I0oDlWruLQExMGYrryKL7XL3K20ia1SOxQjRqr8U',
           'Администратор'
    FROM new_agency
    RETURNING id
), new_operators AS (
    INSERT INTO tour_operators (agency_id, name, inn, contact_phone, contact_email, website, note)
    SELECT new_agency.id, o.name, o.inn, o.contact_phone, o.contact_email, o.website, o.note
    FROM new_agency, (VALUES (
        'ООО «Библио-Глобус Туроператор»',
        '7731447686',
        '+74951092500',
        'agent@bgoperator.com',
        'https://www.bgoperator.ru/',
        E'График работы колл-центра:\n10:00-21:00 — понедельник-пятница\n10:00-20:00 — суббота\n10:00-18:00 — воскресенье'
    ), (
        'ООО «Пегас Туристик»',
        '7714126996',
        '+74954199294',
        'pegast@pegast.ru',
        'https://pegast.ru/',
        E'График работы ресепшна (офис Москва): 10:00-19:00 — понедельник-пятница\nПрием и выдача документов: 10:00-18:00 — понедельник-пятница'
    ), (
        'ООО «АНЕКС ТУРИЗМ»',
        '7743184470',
        '+74950095000',
        'support@anextour.com',
        'https://anextour.ru',
        E'График работы колл-центра:\nКруглосуточно — 24/7'
    ), (
        'ООО «ТТ-Трэвел»',
        '7714775020',
        '+74957750725',
        'fs24@fstravel.com',
        'https://fstravel.com',
        E'График работы центрального офиса (Москва):\n09:00-18:00 — понедельник-пятница'
    )) AS o(name, inn, contact_phone, contact_email, website, note)
    RETURNING 1
)
SELECT 1;
