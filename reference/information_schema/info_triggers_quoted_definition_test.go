package reference_test

import "strings"

const itQuoteDefinition = ` SELECT ("current_database"())::"information_schema"."sql_identifier" AS "trigger_catalog",
    ("n"."nspname")::"information_schema"."sql_identifier" AS "trigger_schema",
    ("t"."tgname")::"information_schema"."sql_identifier" AS "trigger_name",
    ("em"."text")::"information_schema"."character_data" AS "event_manipulation",
    ("current_database"())::"information_schema"."sql_identifier" AS "event_object_catalog",
    ("n"."nspname")::"information_schema"."sql_identifier" AS "event_object_schema",
    ("c"."relname")::"information_schema"."sql_identifier" AS "event_object_table",
    ("rank"() OVER (PARTITION BY ("n"."nspname")::"information_schema"."sql_identifier", ("c"."relname")::"information_schema"."sql_identifier", "em"."num", (("t"."tgtype")::integer & 1), (("t"."tgtype")::integer & 66) ORDER BY "t"."tgname"))::"information_schema"."cardinal_number" AS "action_order",
    (
        CASE
            WHEN "pg_has_role"("c"."relowner", 'USAGE'::"text") THEN ("regexp_match"("pg_get_triggerdef"("t"."oid"), '.{35,} WHEN \((.+)\) EXECUTE FUNCTION'::"text"))[1]
            ELSE NULL::"text"
        END)::"information_schema"."character_data" AS "action_condition",
    (SUBSTRING("pg_get_triggerdef"("t"."oid") FROM (POSITION(('EXECUTE FUNCTION'::"text") IN (SUBSTRING("pg_get_triggerdef"("t"."oid") FROM 48))) + 47)))::"information_schema"."character_data" AS "action_statement",
    (
        CASE (("t"."tgtype")::integer & 1)
            WHEN 1 THEN 'ROW'::"text"
            ELSE 'STATEMENT'::"text"
        END)::"information_schema"."character_data" AS "action_orientation",
    (
        CASE (("t"."tgtype")::integer & 66)
            WHEN 2 THEN 'BEFORE'::"text"
            WHEN 64 THEN 'INSTEAD OF'::"text"
            ELSE 'AFTER'::"text"
        END)::"information_schema"."character_data" AS "action_timing",
    ("t"."tgoldtable")::"information_schema"."sql_identifier" AS "action_reference_old_table",
    ("t"."tgnewtable")::"information_schema"."sql_identifier" AS "action_reference_new_table",
    (NULL::"name")::"information_schema"."sql_identifier" AS "action_reference_old_row",
    (NULL::"name")::"information_schema"."sql_identifier" AS "action_reference_new_row",
    (NULL::timestamp with time zone)::"information_schema"."time_stamp" AS "created"
   FROM "pg_namespace" "n",
    "pg_class" "c",
    "pg_trigger" "t",
    ( VALUES (4,'INSERT'::"text"), (8,'DELETE'::"text"), (16,'UPDATE'::"text")) "em"("num", "text")
  WHERE (("n"."oid" = "c"."relnamespace") AND ("c"."oid" = "t"."tgrelid") AND ((("t"."tgtype")::integer & "em"."num") <> 0) AND (NOT "t"."tgisinternal") AND (NOT "pg_is_other_temp_schema"("n"."oid")) AND ("pg_has_role"("c"."relowner", 'USAGE'::"text") OR "has_table_privilege"("c"."oid", 'INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER'::"text") OR "has_any_column_privilege"("c"."oid", 'INSERT, UPDATE, REFERENCES'::"text")));`

// These path variants prescribe only the independently specified lookup effects:
// domains become unqualified, or the two actually shadowed relations qualify.
var itPathDefinition = strings.ReplaceAll(itDefinition, "information_schema.", "")
var itShadowDefinition = strings.NewReplacer("FROM pg_namespace n", "FROM pg_catalog.pg_namespace n", "    pg_class c", "    pg_catalog.pg_class c").Replace(itDefinition)
var itQuotePathDefinition = strings.ReplaceAll(itQuoteDefinition, `"information_schema".`, "")
var itQuoteShadowDefinition = strings.NewReplacer(`FROM "pg_namespace" "n"`, `FROM "pg_catalog"."pg_namespace" "n"`, `    "pg_class" "c"`, `    "pg_catalog"."pg_class" "c"`).Replace(itQuoteDefinition)
