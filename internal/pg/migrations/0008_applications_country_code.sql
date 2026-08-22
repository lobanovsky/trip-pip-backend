ALTER TABLE applications
    ADD COLUMN country_code text REFERENCES countries (code) ON DELETE SET NULL;

-- Разовая привязка существующих текстовых значений country к коду по точному
-- совпадению названия (без учёта регистра/пробелов). То, что не совпало,
-- остаётся как было — country_code NULL, country — старый текст, ждёт, пока
-- сотрудник выберет страну заново через справочник.
UPDATE applications a
SET country_code = c.code
FROM countries c
WHERE lower(btrim(c.name)) = lower(btrim(a.country));
