package reference_test

// Independent pg_get_viewdef strings observed on the pinned PostgreSQL 18.6 oracle.

const ipView_default = ` SELECT (current_database())::information_schema.sql_identifier AS specific_catalog,
    (ss.n_nspname)::information_schema.sql_identifier AS specific_schema,
    (nameconcatoid(ss.proname, ss.p_oid))::information_schema.sql_identifier AS specific_name,
    ((ss.x).n)::information_schema.cardinal_number AS ordinal_position,
    (
        CASE
            WHEN (ss.proargmodes IS NULL) THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'i'::"char") THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'o'::"char") THEN 'OUT'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'b'::"char") THEN 'INOUT'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'v'::"char") THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 't'::"char") THEN 'OUT'::text
            ELSE NULL::text
        END)::information_schema.character_data AS parameter_mode,
    ('NO'::character varying)::information_schema.yes_or_no AS is_result,
    ('NO'::character varying)::information_schema.yes_or_no AS as_locator,
    (NULLIF(ss.proargnames[(ss.x).n], ''::text))::information_schema.sql_identifier AS parameter_name,
    (
        CASE
            WHEN ((t.typelem <> (0)::oid) AND (t.typlen = '-1'::integer)) THEN 'ARRAY'::text
            WHEN (nt.nspname = 'pg_catalog'::name) THEN format_type(t.oid, NULL::integer)
            ELSE 'USER-DEFINED'::text
        END)::information_schema.character_data AS data_type,
    (NULL::integer)::information_schema.cardinal_number AS character_maximum_length,
    (NULL::integer)::information_schema.cardinal_number AS character_octet_length,
    (NULL::name)::information_schema.sql_identifier AS character_set_catalog,
    (NULL::name)::information_schema.sql_identifier AS character_set_schema,
    (NULL::name)::information_schema.sql_identifier AS character_set_name,
    (NULL::name)::information_schema.sql_identifier AS collation_catalog,
    (NULL::name)::information_schema.sql_identifier AS collation_schema,
    (NULL::name)::information_schema.sql_identifier AS collation_name,
    (NULL::integer)::information_schema.cardinal_number AS numeric_precision,
    (NULL::integer)::information_schema.cardinal_number AS numeric_precision_radix,
    (NULL::integer)::information_schema.cardinal_number AS numeric_scale,
    (NULL::integer)::information_schema.cardinal_number AS datetime_precision,
    (NULL::character varying)::information_schema.character_data AS interval_type,
    (NULL::integer)::information_schema.cardinal_number AS interval_precision,
    (current_database())::information_schema.sql_identifier AS udt_catalog,
    (nt.nspname)::information_schema.sql_identifier AS udt_schema,
    (t.typname)::information_schema.sql_identifier AS udt_name,
    (NULL::name)::information_schema.sql_identifier AS scope_catalog,
    (NULL::name)::information_schema.sql_identifier AS scope_schema,
    (NULL::name)::information_schema.sql_identifier AS scope_name,
    (NULL::integer)::information_schema.cardinal_number AS maximum_cardinality,
    ((ss.x).n)::information_schema.sql_identifier AS dtd_identifier,
    (
        CASE
            WHEN pg_has_role(ss.proowner, 'USAGE'::text) THEN pg_get_function_arg_default(ss.p_oid, (ss.x).n)
            ELSE NULL::text
        END)::information_schema.character_data AS parameter_default
   FROM pg_type t,
    pg_namespace nt,
    ( SELECT n.nspname AS n_nspname,
            p.proname,
            p.oid AS p_oid,
            p.proowner,
            p.proargnames,
            p.proargmodes,
            information_schema._pg_expandarray(COALESCE(p.proallargtypes, (p.proargtypes)::oid[])) AS x
           FROM pg_namespace n,
            pg_proc p
          WHERE ((n.oid = p.pronamespace) AND (pg_has_role(p.proowner, 'USAGE'::text) OR has_function_privilege(p.oid, 'EXECUTE'::text)))) ss
  WHERE ((t.oid = (ss.x).x) AND (t.typnamespace = nt.oid));`

const ipView_information_schema_path = ` SELECT (current_database())::sql_identifier AS specific_catalog,
    (ss.n_nspname)::sql_identifier AS specific_schema,
    (nameconcatoid(ss.proname, ss.p_oid))::sql_identifier AS specific_name,
    ((ss.x).n)::cardinal_number AS ordinal_position,
    (
        CASE
            WHEN (ss.proargmodes IS NULL) THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'i'::"char") THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'o'::"char") THEN 'OUT'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'b'::"char") THEN 'INOUT'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'v'::"char") THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 't'::"char") THEN 'OUT'::text
            ELSE NULL::text
        END)::character_data AS parameter_mode,
    ('NO'::character varying)::yes_or_no AS is_result,
    ('NO'::character varying)::yes_or_no AS as_locator,
    (NULLIF(ss.proargnames[(ss.x).n], ''::text))::sql_identifier AS parameter_name,
    (
        CASE
            WHEN ((t.typelem <> (0)::oid) AND (t.typlen = '-1'::integer)) THEN 'ARRAY'::text
            WHEN (nt.nspname = 'pg_catalog'::name) THEN format_type(t.oid, NULL::integer)
            ELSE 'USER-DEFINED'::text
        END)::character_data AS data_type,
    (NULL::integer)::cardinal_number AS character_maximum_length,
    (NULL::integer)::cardinal_number AS character_octet_length,
    (NULL::name)::sql_identifier AS character_set_catalog,
    (NULL::name)::sql_identifier AS character_set_schema,
    (NULL::name)::sql_identifier AS character_set_name,
    (NULL::name)::sql_identifier AS collation_catalog,
    (NULL::name)::sql_identifier AS collation_schema,
    (NULL::name)::sql_identifier AS collation_name,
    (NULL::integer)::cardinal_number AS numeric_precision,
    (NULL::integer)::cardinal_number AS numeric_precision_radix,
    (NULL::integer)::cardinal_number AS numeric_scale,
    (NULL::integer)::cardinal_number AS datetime_precision,
    (NULL::character varying)::character_data AS interval_type,
    (NULL::integer)::cardinal_number AS interval_precision,
    (current_database())::sql_identifier AS udt_catalog,
    (nt.nspname)::sql_identifier AS udt_schema,
    (t.typname)::sql_identifier AS udt_name,
    (NULL::name)::sql_identifier AS scope_catalog,
    (NULL::name)::sql_identifier AS scope_schema,
    (NULL::name)::sql_identifier AS scope_name,
    (NULL::integer)::cardinal_number AS maximum_cardinality,
    ((ss.x).n)::sql_identifier AS dtd_identifier,
    (
        CASE
            WHEN pg_has_role(ss.proowner, 'USAGE'::text) THEN pg_get_function_arg_default(ss.p_oid, (ss.x).n)
            ELSE NULL::text
        END)::character_data AS parameter_default
   FROM pg_type t,
    pg_namespace nt,
    ( SELECT n.nspname AS n_nspname,
            p.proname,
            p.oid AS p_oid,
            p.proowner,
            p.proargnames,
            p.proargmodes,
            _pg_expandarray(COALESCE(p.proallargtypes, (p.proargtypes)::oid[])) AS x
           FROM pg_namespace n,
            pg_proc p
          WHERE ((n.oid = p.pronamespace) AND (pg_has_role(p.proowner, 'USAGE'::text) OR has_function_privilege(p.oid, 'EXECUTE'::text)))) ss
  WHERE ((t.oid = (ss.x).x) AND (t.typnamespace = nt.oid));`

const ipView_shadow = ` SELECT (current_database())::information_schema.sql_identifier AS specific_catalog,
    (ss.n_nspname)::information_schema.sql_identifier AS specific_schema,
    (nameconcatoid(ss.proname, ss.p_oid))::information_schema.sql_identifier AS specific_name,
    ((ss.x).n)::information_schema.cardinal_number AS ordinal_position,
    (
        CASE
            WHEN (ss.proargmodes IS NULL) THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'i'::"char") THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'o'::"char") THEN 'OUT'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'b'::"char") THEN 'INOUT'::text
            WHEN (ss.proargmodes[(ss.x).n] = 'v'::"char") THEN 'IN'::text
            WHEN (ss.proargmodes[(ss.x).n] = 't'::"char") THEN 'OUT'::text
            ELSE NULL::text
        END)::information_schema.character_data AS parameter_mode,
    ('NO'::character varying)::information_schema.yes_or_no AS is_result,
    ('NO'::character varying)::information_schema.yes_or_no AS as_locator,
    (NULLIF(ss.proargnames[(ss.x).n], ''::text))::information_schema.sql_identifier AS parameter_name,
    (
        CASE
            WHEN ((t.typelem <> (0)::oid) AND (t.typlen = '-1'::integer)) THEN 'ARRAY'::text
            WHEN (nt.nspname = 'pg_catalog'::name) THEN format_type(t.oid, NULL::integer)
            ELSE 'USER-DEFINED'::text
        END)::information_schema.character_data AS data_type,
    (NULL::integer)::information_schema.cardinal_number AS character_maximum_length,
    (NULL::integer)::information_schema.cardinal_number AS character_octet_length,
    (NULL::name)::information_schema.sql_identifier AS character_set_catalog,
    (NULL::name)::information_schema.sql_identifier AS character_set_schema,
    (NULL::name)::information_schema.sql_identifier AS character_set_name,
    (NULL::name)::information_schema.sql_identifier AS collation_catalog,
    (NULL::name)::information_schema.sql_identifier AS collation_schema,
    (NULL::name)::information_schema.sql_identifier AS collation_name,
    (NULL::integer)::information_schema.cardinal_number AS numeric_precision,
    (NULL::integer)::information_schema.cardinal_number AS numeric_precision_radix,
    (NULL::integer)::information_schema.cardinal_number AS numeric_scale,
    (NULL::integer)::information_schema.cardinal_number AS datetime_precision,
    (NULL::character varying)::information_schema.character_data AS interval_type,
    (NULL::integer)::information_schema.cardinal_number AS interval_precision,
    (current_database())::information_schema.sql_identifier AS udt_catalog,
    (nt.nspname)::information_schema.sql_identifier AS udt_schema,
    (t.typname)::information_schema.sql_identifier AS udt_name,
    (NULL::name)::information_schema.sql_identifier AS scope_catalog,
    (NULL::name)::information_schema.sql_identifier AS scope_schema,
    (NULL::name)::information_schema.sql_identifier AS scope_name,
    (NULL::integer)::information_schema.cardinal_number AS maximum_cardinality,
    ((ss.x).n)::information_schema.sql_identifier AS dtd_identifier,
    (
        CASE
            WHEN pg_has_role(ss.proowner, 'USAGE'::text) THEN pg_get_function_arg_default(ss.p_oid, (ss.x).n)
            ELSE NULL::text
        END)::information_schema.character_data AS parameter_default
   FROM pg_type t,
    pg_catalog.pg_namespace nt,
    ( SELECT n.nspname AS n_nspname,
            p.proname,
            p.oid AS p_oid,
            p.proowner,
            p.proargnames,
            p.proargmodes,
            information_schema._pg_expandarray(COALESCE(p.proallargtypes, (p.proargtypes)::oid[])) AS x
           FROM pg_catalog.pg_namespace n,
            pg_proc p
          WHERE ((n.oid = p.pronamespace) AND (pg_has_role(p.proowner, 'USAGE'::text) OR has_function_privilege(p.oid, 'EXECUTE'::text)))) ss
  WHERE ((t.oid = (ss.x).x) AND (t.typnamespace = nt.oid));`

const ipView_quote_default = ` SELECT ("current_database"())::"information_schema"."sql_identifier" AS "specific_catalog",
    ("ss"."n_nspname")::"information_schema"."sql_identifier" AS "specific_schema",
    ("nameconcatoid"("ss"."proname", "ss"."p_oid"))::"information_schema"."sql_identifier" AS "specific_name",
    (("ss"."x")."n")::"information_schema"."cardinal_number" AS "ordinal_position",
    (
        CASE
            WHEN ("ss"."proargmodes" IS NULL) THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'i'::"char") THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'o'::"char") THEN 'OUT'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'b'::"char") THEN 'INOUT'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'v'::"char") THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 't'::"char") THEN 'OUT'::"text"
            ELSE NULL::"text"
        END)::"information_schema"."character_data" AS "parameter_mode",
    ('NO'::character varying)::"information_schema"."yes_or_no" AS "is_result",
    ('NO'::character varying)::"information_schema"."yes_or_no" AS "as_locator",
    (NULLIF("ss"."proargnames"[("ss"."x")."n"], ''::"text"))::"information_schema"."sql_identifier" AS "parameter_name",
    (
        CASE
            WHEN (("t"."typelem" <> (0)::"oid") AND ("t"."typlen" = '-1'::integer)) THEN 'ARRAY'::"text"
            WHEN ("nt"."nspname" = 'pg_catalog'::"name") THEN "format_type"("t"."oid", NULL::integer)
            ELSE 'USER-DEFINED'::"text"
        END)::"information_schema"."character_data" AS "data_type",
    (NULL::integer)::"information_schema"."cardinal_number" AS "character_maximum_length",
    (NULL::integer)::"information_schema"."cardinal_number" AS "character_octet_length",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_catalog",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_schema",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_name",
    (NULL::"name")::"information_schema"."sql_identifier" AS "collation_catalog",
    (NULL::"name")::"information_schema"."sql_identifier" AS "collation_schema",
    (NULL::"name")::"information_schema"."sql_identifier" AS "collation_name",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_precision",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_precision_radix",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_scale",
    (NULL::integer)::"information_schema"."cardinal_number" AS "datetime_precision",
    (NULL::character varying)::"information_schema"."character_data" AS "interval_type",
    (NULL::integer)::"information_schema"."cardinal_number" AS "interval_precision",
    ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("t"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_catalog",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_schema",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_name",
    (NULL::integer)::"information_schema"."cardinal_number" AS "maximum_cardinality",
    (("ss"."x")."n")::"information_schema"."sql_identifier" AS "dtd_identifier",
    (
        CASE
            WHEN "pg_has_role"("ss"."proowner", 'USAGE'::"text") THEN "pg_get_function_arg_default"("ss"."p_oid", ("ss"."x")."n")
            ELSE NULL::"text"
        END)::"information_schema"."character_data" AS "parameter_default"
   FROM "pg_type" "t",
    "pg_namespace" "nt",
    ( SELECT "n"."nspname" AS "n_nspname",
            "p"."proname",
            "p"."oid" AS "p_oid",
            "p"."proowner",
            "p"."proargnames",
            "p"."proargmodes",
            "information_schema"."_pg_expandarray"(COALESCE("p"."proallargtypes", ("p"."proargtypes")::"oid"[])) AS "x"
           FROM "pg_namespace" "n",
            "pg_proc" "p"
          WHERE (("n"."oid" = "p"."pronamespace") AND ("pg_has_role"("p"."proowner", 'USAGE'::"text") OR "has_function_privilege"("p"."oid", 'EXECUTE'::"text")))) "ss"
  WHERE (("t"."oid" = ("ss"."x")."x") AND ("t"."typnamespace" = "nt"."oid"));`

const ipView_quote_information_schema_path = ` SELECT ("current_database"())::"sql_identifier" AS "specific_catalog",
    ("ss"."n_nspname")::"sql_identifier" AS "specific_schema",
    ("nameconcatoid"("ss"."proname", "ss"."p_oid"))::"sql_identifier" AS "specific_name",
    (("ss"."x")."n")::"cardinal_number" AS "ordinal_position",
    (
        CASE
            WHEN ("ss"."proargmodes" IS NULL) THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'i'::"char") THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'o'::"char") THEN 'OUT'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'b'::"char") THEN 'INOUT'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'v'::"char") THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 't'::"char") THEN 'OUT'::"text"
            ELSE NULL::"text"
        END)::"character_data" AS "parameter_mode",
    ('NO'::character varying)::"yes_or_no" AS "is_result",
    ('NO'::character varying)::"yes_or_no" AS "as_locator",
    (NULLIF("ss"."proargnames"[("ss"."x")."n"], ''::"text"))::"sql_identifier" AS "parameter_name",
    (
        CASE
            WHEN (("t"."typelem" <> (0)::"oid") AND ("t"."typlen" = '-1'::integer)) THEN 'ARRAY'::"text"
            WHEN ("nt"."nspname" = 'pg_catalog'::"name") THEN "format_type"("t"."oid", NULL::integer)
            ELSE 'USER-DEFINED'::"text"
        END)::"character_data" AS "data_type",
    (NULL::integer)::"cardinal_number" AS "character_maximum_length",
    (NULL::integer)::"cardinal_number" AS "character_octet_length",
    (NULL::"name")::"sql_identifier" AS "character_set_catalog",
    (NULL::"name")::"sql_identifier" AS "character_set_schema",
    (NULL::"name")::"sql_identifier" AS "character_set_name",
    (NULL::"name")::"sql_identifier" AS "collation_catalog",
    (NULL::"name")::"sql_identifier" AS "collation_schema",
    (NULL::"name")::"sql_identifier" AS "collation_name",
    (NULL::integer)::"cardinal_number" AS "numeric_precision",
    (NULL::integer)::"cardinal_number" AS "numeric_precision_radix",
    (NULL::integer)::"cardinal_number" AS "numeric_scale",
    (NULL::integer)::"cardinal_number" AS "datetime_precision",
    (NULL::character varying)::"character_data" AS "interval_type",
    (NULL::integer)::"cardinal_number" AS "interval_precision",
    ("current_database"())::"sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"sql_identifier" AS "udt_schema",
    ("t"."typname")::"sql_identifier" AS "udt_name",
    (NULL::"name")::"sql_identifier" AS "scope_catalog",
    (NULL::"name")::"sql_identifier" AS "scope_schema",
    (NULL::"name")::"sql_identifier" AS "scope_name",
    (NULL::integer)::"cardinal_number" AS "maximum_cardinality",
    (("ss"."x")."n")::"sql_identifier" AS "dtd_identifier",
    (
        CASE
            WHEN "pg_has_role"("ss"."proowner", 'USAGE'::"text") THEN "pg_get_function_arg_default"("ss"."p_oid", ("ss"."x")."n")
            ELSE NULL::"text"
        END)::"character_data" AS "parameter_default"
   FROM "pg_type" "t",
    "pg_namespace" "nt",
    ( SELECT "n"."nspname" AS "n_nspname",
            "p"."proname",
            "p"."oid" AS "p_oid",
            "p"."proowner",
            "p"."proargnames",
            "p"."proargmodes",
            "_pg_expandarray"(COALESCE("p"."proallargtypes", ("p"."proargtypes")::"oid"[])) AS "x"
           FROM "pg_namespace" "n",
            "pg_proc" "p"
          WHERE (("n"."oid" = "p"."pronamespace") AND ("pg_has_role"("p"."proowner", 'USAGE'::"text") OR "has_function_privilege"("p"."oid", 'EXECUTE'::"text")))) "ss"
  WHERE (("t"."oid" = ("ss"."x")."x") AND ("t"."typnamespace" = "nt"."oid"));`

const ipView_quote_shadow = ` SELECT ("current_database"())::"information_schema"."sql_identifier" AS "specific_catalog",
    ("ss"."n_nspname")::"information_schema"."sql_identifier" AS "specific_schema",
    ("nameconcatoid"("ss"."proname", "ss"."p_oid"))::"information_schema"."sql_identifier" AS "specific_name",
    (("ss"."x")."n")::"information_schema"."cardinal_number" AS "ordinal_position",
    (
        CASE
            WHEN ("ss"."proargmodes" IS NULL) THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'i'::"char") THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'o'::"char") THEN 'OUT'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'b'::"char") THEN 'INOUT'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 'v'::"char") THEN 'IN'::"text"
            WHEN ("ss"."proargmodes"[("ss"."x")."n"] = 't'::"char") THEN 'OUT'::"text"
            ELSE NULL::"text"
        END)::"information_schema"."character_data" AS "parameter_mode",
    ('NO'::character varying)::"information_schema"."yes_or_no" AS "is_result",
    ('NO'::character varying)::"information_schema"."yes_or_no" AS "as_locator",
    (NULLIF("ss"."proargnames"[("ss"."x")."n"], ''::"text"))::"information_schema"."sql_identifier" AS "parameter_name",
    (
        CASE
            WHEN (("t"."typelem" <> (0)::"oid") AND ("t"."typlen" = '-1'::integer)) THEN 'ARRAY'::"text"
            WHEN ("nt"."nspname" = 'pg_catalog'::"name") THEN "format_type"("t"."oid", NULL::integer)
            ELSE 'USER-DEFINED'::"text"
        END)::"information_schema"."character_data" AS "data_type",
    (NULL::integer)::"information_schema"."cardinal_number" AS "character_maximum_length",
    (NULL::integer)::"information_schema"."cardinal_number" AS "character_octet_length",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_catalog",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_schema",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_name",
    (NULL::"name")::"information_schema"."sql_identifier" AS "collation_catalog",
    (NULL::"name")::"information_schema"."sql_identifier" AS "collation_schema",
    (NULL::"name")::"information_schema"."sql_identifier" AS "collation_name",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_precision",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_precision_radix",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_scale",
    (NULL::integer)::"information_schema"."cardinal_number" AS "datetime_precision",
    (NULL::character varying)::"information_schema"."character_data" AS "interval_type",
    (NULL::integer)::"information_schema"."cardinal_number" AS "interval_precision",
    ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("t"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_catalog",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_schema",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_name",
    (NULL::integer)::"information_schema"."cardinal_number" AS "maximum_cardinality",
    (("ss"."x")."n")::"information_schema"."sql_identifier" AS "dtd_identifier",
    (
        CASE
            WHEN "pg_has_role"("ss"."proowner", 'USAGE'::"text") THEN "pg_get_function_arg_default"("ss"."p_oid", ("ss"."x")."n")
            ELSE NULL::"text"
        END)::"information_schema"."character_data" AS "parameter_default"
   FROM "pg_type" "t",
    "pg_catalog"."pg_namespace" "nt",
    ( SELECT "n"."nspname" AS "n_nspname",
            "p"."proname",
            "p"."oid" AS "p_oid",
            "p"."proowner",
            "p"."proargnames",
            "p"."proargmodes",
            "information_schema"."_pg_expandarray"(COALESCE("p"."proallargtypes", ("p"."proargtypes")::"oid"[])) AS "x"
           FROM "pg_catalog"."pg_namespace" "n",
            "pg_proc" "p"
          WHERE (("n"."oid" = "p"."pronamespace") AND ("pg_has_role"("p"."proowner", 'USAGE'::"text") OR "has_function_privilege"("p"."oid", 'EXECUTE'::"text")))) "ss"
  WHERE (("t"."oid" = ("ss"."x")."x") AND ("t"."typnamespace" = "nt"."oid"));`
