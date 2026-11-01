package reference_test

// Independently reasoned REL_18_6 UDT_PRIVILEGES presentation.
// The matching domain arm of the existing pinned usage-privilege fixture supplies
// formatting only; release SQL supplies the composite filter and seven outputs.
// Complete strings are confirmed against the pinned PostgreSQL 18.6 oracle.

const ivUdtDefinition = ` SELECT (u_grantor.rolname)::information_schema.sql_identifier AS grantor,
    (grantee.rolname)::information_schema.sql_identifier AS grantee,
    (current_database())::information_schema.sql_identifier AS udt_catalog,
    (n.nspname)::information_schema.sql_identifier AS udt_schema,
    (t.typname)::information_schema.sql_identifier AS udt_name,
    ('TYPE USAGE'::character varying)::information_schema.character_data AS privilege_type,
    (
        CASE
            WHEN (pg_has_role(grantee.oid, t.typowner, 'USAGE'::text) OR t.grantable) THEN 'YES'::text
            ELSE 'NO'::text
        END)::information_schema.yes_or_no AS is_grantable
   FROM ( SELECT pg_type.oid,
            pg_type.typname,
            pg_type.typnamespace,
            pg_type.typtype,
            pg_type.typowner,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).grantor AS grantor,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).grantee AS grantee,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).privilege_type AS privilege_type,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).is_grantable AS is_grantable
           FROM pg_type) t(oid, typname, typnamespace, typtype, typowner, grantor, grantee, prtype, grantable),
    pg_namespace n,
    pg_authid u_grantor,
    ( SELECT pg_authid.oid,
            pg_authid.rolname
           FROM pg_authid
        UNION ALL
         SELECT (0)::oid AS oid,
            'PUBLIC'::name) grantee(oid, rolname)
  WHERE ((t.typnamespace = n.oid) AND (t.typtype = 'c'::"char") AND (t.grantee = grantee.oid) AND (t.grantor = u_grantor.oid) AND (t.prtype = 'USAGE'::text) AND (pg_has_role(u_grantor.oid, 'USAGE'::text) OR pg_has_role(grantee.oid, 'USAGE'::text) OR (grantee.rolname = 'PUBLIC'::name)));`

const ivUdtQuotedDefinition = ` SELECT ("u_grantor"."rolname")::"information_schema"."sql_identifier" AS "grantor",
    ("grantee"."rolname")::"information_schema"."sql_identifier" AS "grantee",
    ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("n"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("t"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    ('TYPE USAGE'::character varying)::"information_schema"."character_data" AS "privilege_type",
    (
        CASE
            WHEN ("pg_has_role"("grantee"."oid", "t"."typowner", 'USAGE'::"text") OR "t"."grantable") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"information_schema"."yes_or_no" AS "is_grantable"
   FROM ( SELECT "pg_type"."oid",
            "pg_type"."typname",
            "pg_type"."typnamespace",
            "pg_type"."typtype",
            "pg_type"."typowner",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."grantor" AS "grantor",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."grantee" AS "grantee",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."privilege_type" AS "privilege_type",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."is_grantable" AS "is_grantable"
           FROM "pg_type") "t"("oid", "typname", "typnamespace", "typtype", "typowner", "grantor", "grantee", "prtype", "grantable"),
    "pg_namespace" "n",
    "pg_authid" "u_grantor",
    ( SELECT "pg_authid"."oid",
            "pg_authid"."rolname"
           FROM "pg_authid"
        UNION ALL
         SELECT (0)::"oid" AS "oid",
            'PUBLIC'::"name") "grantee"("oid", "rolname")
  WHERE (("t"."typnamespace" = "n"."oid") AND ("t"."typtype" = 'c'::"char") AND ("t"."grantee" = "grantee"."oid") AND ("t"."grantor" = "u_grantor"."oid") AND ("t"."prtype" = 'USAGE'::"text") AND ("pg_has_role"("u_grantor"."oid", 'USAGE'::"text") OR "pg_has_role"("grantee"."oid", 'USAGE'::"text") OR ("grantee"."rolname" = 'PUBLIC'::"name")));`

const ivUdtPathDefinition = ` SELECT (u_grantor.rolname)::sql_identifier AS grantor,
    (grantee.rolname)::sql_identifier AS grantee,
    (current_database())::sql_identifier AS udt_catalog,
    (n.nspname)::sql_identifier AS udt_schema,
    (t.typname)::sql_identifier AS udt_name,
    ('TYPE USAGE'::character varying)::character_data AS privilege_type,
    (
        CASE
            WHEN (pg_has_role(grantee.oid, t.typowner, 'USAGE'::text) OR t.grantable) THEN 'YES'::text
            ELSE 'NO'::text
        END)::yes_or_no AS is_grantable
   FROM ( SELECT pg_type.oid,
            pg_type.typname,
            pg_type.typnamespace,
            pg_type.typtype,
            pg_type.typowner,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).grantor AS grantor,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).grantee AS grantee,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).privilege_type AS privilege_type,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).is_grantable AS is_grantable
           FROM pg_type) t(oid, typname, typnamespace, typtype, typowner, grantor, grantee, prtype, grantable),
    pg_namespace n,
    pg_authid u_grantor,
    ( SELECT pg_authid.oid,
            pg_authid.rolname
           FROM pg_authid
        UNION ALL
         SELECT (0)::oid AS oid,
            'PUBLIC'::name) grantee(oid, rolname)
  WHERE ((t.typnamespace = n.oid) AND (t.typtype = 'c'::"char") AND (t.grantee = grantee.oid) AND (t.grantor = u_grantor.oid) AND (t.prtype = 'USAGE'::text) AND (pg_has_role(u_grantor.oid, 'USAGE'::text) OR pg_has_role(grantee.oid, 'USAGE'::text) OR (grantee.rolname = 'PUBLIC'::name)));`

const ivUdtQuotedPathDefinition = ` SELECT ("u_grantor"."rolname")::"sql_identifier" AS "grantor",
    ("grantee"."rolname")::"sql_identifier" AS "grantee",
    ("current_database"())::"sql_identifier" AS "udt_catalog",
    ("n"."nspname")::"sql_identifier" AS "udt_schema",
    ("t"."typname")::"sql_identifier" AS "udt_name",
    ('TYPE USAGE'::character varying)::"character_data" AS "privilege_type",
    (
        CASE
            WHEN ("pg_has_role"("grantee"."oid", "t"."typowner", 'USAGE'::"text") OR "t"."grantable") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"yes_or_no" AS "is_grantable"
   FROM ( SELECT "pg_type"."oid",
            "pg_type"."typname",
            "pg_type"."typnamespace",
            "pg_type"."typtype",
            "pg_type"."typowner",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."grantor" AS "grantor",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."grantee" AS "grantee",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."privilege_type" AS "privilege_type",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."is_grantable" AS "is_grantable"
           FROM "pg_type") "t"("oid", "typname", "typnamespace", "typtype", "typowner", "grantor", "grantee", "prtype", "grantable"),
    "pg_namespace" "n",
    "pg_authid" "u_grantor",
    ( SELECT "pg_authid"."oid",
            "pg_authid"."rolname"
           FROM "pg_authid"
        UNION ALL
         SELECT (0)::"oid" AS "oid",
            'PUBLIC'::"name") "grantee"("oid", "rolname")
  WHERE (("t"."typnamespace" = "n"."oid") AND ("t"."typtype" = 'c'::"char") AND ("t"."grantee" = "grantee"."oid") AND ("t"."grantor" = "u_grantor"."oid") AND ("t"."prtype" = 'USAGE'::"text") AND ("pg_has_role"("u_grantor"."oid", 'USAGE'::"text") OR "pg_has_role"("grantee"."oid", 'USAGE'::"text") OR ("grantee"."rolname" = 'PUBLIC'::"name")));`

const ivUdtShadowDefinition = ` SELECT (u_grantor.rolname)::information_schema.sql_identifier AS grantor,
    (grantee.rolname)::information_schema.sql_identifier AS grantee,
    (current_database())::information_schema.sql_identifier AS udt_catalog,
    (n.nspname)::information_schema.sql_identifier AS udt_schema,
    (t.typname)::information_schema.sql_identifier AS udt_name,
    ('TYPE USAGE'::character varying)::information_schema.character_data AS privilege_type,
    (
        CASE
            WHEN (pg_has_role(grantee.oid, t.typowner, 'USAGE'::text) OR t.grantable) THEN 'YES'::text
            ELSE 'NO'::text
        END)::information_schema.yes_or_no AS is_grantable
   FROM ( SELECT pg_type.oid,
            pg_type.typname,
            pg_type.typnamespace,
            pg_type.typtype,
            pg_type.typowner,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).grantor AS grantor,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).grantee AS grantee,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).privilege_type AS privilege_type,
            (aclexplode(COALESCE(pg_type.typacl, acldefault('T'::"char", pg_type.typowner)))).is_grantable AS is_grantable
           FROM pg_type) t(oid, typname, typnamespace, typtype, typowner, grantor, grantee, prtype, grantable),
    pg_catalog.pg_namespace n,
    pg_catalog.pg_authid u_grantor,
    ( SELECT pg_authid.oid,
            pg_authid.rolname
           FROM pg_catalog.pg_authid
        UNION ALL
         SELECT (0)::oid AS oid,
            'PUBLIC'::name) grantee(oid, rolname)
  WHERE ((t.typnamespace = n.oid) AND (t.typtype = 'c'::"char") AND (t.grantee = grantee.oid) AND (t.grantor = u_grantor.oid) AND (t.prtype = 'USAGE'::text) AND (pg_has_role(u_grantor.oid, 'USAGE'::text) OR pg_has_role(grantee.oid, 'USAGE'::text) OR (grantee.rolname = 'PUBLIC'::name)));`

const ivUdtQuotedShadowDefinition = ` SELECT ("u_grantor"."rolname")::"information_schema"."sql_identifier" AS "grantor",
    ("grantee"."rolname")::"information_schema"."sql_identifier" AS "grantee",
    ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("n"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("t"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    ('TYPE USAGE'::character varying)::"information_schema"."character_data" AS "privilege_type",
    (
        CASE
            WHEN ("pg_has_role"("grantee"."oid", "t"."typowner", 'USAGE'::"text") OR "t"."grantable") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"information_schema"."yes_or_no" AS "is_grantable"
   FROM ( SELECT "pg_type"."oid",
            "pg_type"."typname",
            "pg_type"."typnamespace",
            "pg_type"."typtype",
            "pg_type"."typowner",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."grantor" AS "grantor",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."grantee" AS "grantee",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."privilege_type" AS "privilege_type",
            ("aclexplode"(COALESCE("pg_type"."typacl", "acldefault"('T'::"char", "pg_type"."typowner"))))."is_grantable" AS "is_grantable"
           FROM "pg_type") "t"("oid", "typname", "typnamespace", "typtype", "typowner", "grantor", "grantee", "prtype", "grantable"),
    "pg_catalog"."pg_namespace" "n",
    "pg_catalog"."pg_authid" "u_grantor",
    ( SELECT "pg_authid"."oid",
            "pg_authid"."rolname"
           FROM "pg_catalog"."pg_authid"
        UNION ALL
         SELECT (0)::"oid" AS "oid",
            'PUBLIC'::"name") "grantee"("oid", "rolname")
  WHERE (("t"."typnamespace" = "n"."oid") AND ("t"."typtype" = 'c'::"char") AND ("t"."grantee" = "grantee"."oid") AND ("t"."grantor" = "u_grantor"."oid") AND ("t"."prtype" = 'USAGE'::"text") AND ("pg_has_role"("u_grantor"."oid", 'USAGE'::"text") OR "pg_has_role"("grantee"."oid", 'USAGE'::"text") OR ("grantee"."rolname" = 'PUBLIC'::"name")));`
