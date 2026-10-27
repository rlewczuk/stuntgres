package reference_test

// Released UNION definition and display-mode expectations; no executable dependencies.
const trDefinition = ` SELECT (current_database())::information_schema.sql_identifier AS udt_catalog,
    (nt.nspname)::information_schema.sql_identifier AS udt_schema,
    (t.typname)::information_schema.sql_identifier AS udt_name,
    (current_database())::information_schema.sql_identifier AS specific_catalog,
    (np.nspname)::information_schema.sql_identifier AS specific_schema,
    (nameconcatoid(p.proname, p.oid))::information_schema.sql_identifier AS specific_name,
    (l.lanname)::information_schema.sql_identifier AS group_name,
    ('FROM SQL'::character varying)::information_schema.character_data AS transform_type
   FROM (((((pg_type t
     JOIN pg_transform x ON ((t.oid = x.trftype)))
     JOIN pg_language l ON ((x.trflang = l.oid)))
     JOIN pg_proc p ON (((x.trffromsql)::oid = p.oid)))
     JOIN pg_namespace nt ON ((t.typnamespace = nt.oid)))
     JOIN pg_namespace np ON ((p.pronamespace = np.oid)))
UNION
 SELECT (current_database())::information_schema.sql_identifier AS udt_catalog,
    (nt.nspname)::information_schema.sql_identifier AS udt_schema,
    (t.typname)::information_schema.sql_identifier AS udt_name,
    (current_database())::information_schema.sql_identifier AS specific_catalog,
    (np.nspname)::information_schema.sql_identifier AS specific_schema,
    (nameconcatoid(p.proname, p.oid))::information_schema.sql_identifier AS specific_name,
    (l.lanname)::information_schema.sql_identifier AS group_name,
    ('TO SQL'::character varying)::information_schema.character_data AS transform_type
   FROM (((((pg_type t
     JOIN pg_transform x ON ((t.oid = x.trftype)))
     JOIN pg_language l ON ((x.trflang = l.oid)))
     JOIN pg_proc p ON (((x.trftosql)::oid = p.oid)))
     JOIN pg_namespace nt ON ((t.typnamespace = nt.oid)))
     JOIN pg_namespace np ON ((p.pronamespace = np.oid)))
  ORDER BY 1, 2, 3, 7, 8;`

const trPathDefinition = ` SELECT (current_database())::sql_identifier AS udt_catalog,
    (nt.nspname)::sql_identifier AS udt_schema,
    (t.typname)::sql_identifier AS udt_name,
    (current_database())::sql_identifier AS specific_catalog,
    (np.nspname)::sql_identifier AS specific_schema,
    (nameconcatoid(p.proname, p.oid))::sql_identifier AS specific_name,
    (l.lanname)::sql_identifier AS group_name,
    ('FROM SQL'::character varying)::character_data AS transform_type
   FROM (((((pg_type t
     JOIN pg_transform x ON ((t.oid = x.trftype)))
     JOIN pg_language l ON ((x.trflang = l.oid)))
     JOIN pg_proc p ON (((x.trffromsql)::oid = p.oid)))
     JOIN pg_namespace nt ON ((t.typnamespace = nt.oid)))
     JOIN pg_namespace np ON ((p.pronamespace = np.oid)))
UNION
 SELECT (current_database())::sql_identifier AS udt_catalog,
    (nt.nspname)::sql_identifier AS udt_schema,
    (t.typname)::sql_identifier AS udt_name,
    (current_database())::sql_identifier AS specific_catalog,
    (np.nspname)::sql_identifier AS specific_schema,
    (nameconcatoid(p.proname, p.oid))::sql_identifier AS specific_name,
    (l.lanname)::sql_identifier AS group_name,
    ('TO SQL'::character varying)::character_data AS transform_type
   FROM (((((pg_type t
     JOIN pg_transform x ON ((t.oid = x.trftype)))
     JOIN pg_language l ON ((x.trflang = l.oid)))
     JOIN pg_proc p ON (((x.trftosql)::oid = p.oid)))
     JOIN pg_namespace nt ON ((t.typnamespace = nt.oid)))
     JOIN pg_namespace np ON ((p.pronamespace = np.oid)))
  ORDER BY 1, 2, 3, 7, 8;`

const trShadowDefinition = ` SELECT (current_database())::information_schema.sql_identifier AS udt_catalog,
    (nt.nspname)::information_schema.sql_identifier AS udt_schema,
    (t.typname)::information_schema.sql_identifier AS udt_name,
    (current_database())::information_schema.sql_identifier AS specific_catalog,
    (np.nspname)::information_schema.sql_identifier AS specific_schema,
    (nameconcatoid(p.proname, p.oid))::information_schema.sql_identifier AS specific_name,
    (l.lanname)::information_schema.sql_identifier AS group_name,
    ('FROM SQL'::character varying)::information_schema.character_data AS transform_type
   FROM (((((pg_type t
     JOIN pg_transform x ON ((t.oid = x.trftype)))
     JOIN pg_language l ON ((x.trflang = l.oid)))
     JOIN pg_proc p ON (((x.trffromsql)::oid = p.oid)))
     JOIN pg_catalog.pg_namespace nt ON ((t.typnamespace = nt.oid)))
     JOIN pg_catalog.pg_namespace np ON ((p.pronamespace = np.oid)))
UNION
 SELECT (current_database())::information_schema.sql_identifier AS udt_catalog,
    (nt.nspname)::information_schema.sql_identifier AS udt_schema,
    (t.typname)::information_schema.sql_identifier AS udt_name,
    (current_database())::information_schema.sql_identifier AS specific_catalog,
    (np.nspname)::information_schema.sql_identifier AS specific_schema,
    (nameconcatoid(p.proname, p.oid))::information_schema.sql_identifier AS specific_name,
    (l.lanname)::information_schema.sql_identifier AS group_name,
    ('TO SQL'::character varying)::information_schema.character_data AS transform_type
   FROM (((((pg_type t
     JOIN pg_transform x ON ((t.oid = x.trftype)))
     JOIN pg_language l ON ((x.trflang = l.oid)))
     JOIN pg_proc p ON (((x.trftosql)::oid = p.oid)))
     JOIN pg_catalog.pg_namespace nt ON ((t.typnamespace = nt.oid)))
     JOIN pg_catalog.pg_namespace np ON ((p.pronamespace = np.oid)))
  ORDER BY 1, 2, 3, 7, 8;`

const trQuoteDefinition = ` SELECT ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("t"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    ("current_database"())::"information_schema"."sql_identifier" AS "specific_catalog",
    ("np"."nspname")::"information_schema"."sql_identifier" AS "specific_schema",
    ("nameconcatoid"("p"."proname", "p"."oid"))::"information_schema"."sql_identifier" AS "specific_name",
    ("l"."lanname")::"information_schema"."sql_identifier" AS "group_name",
    ('FROM SQL'::character varying)::"information_schema"."character_data" AS "transform_type"
   FROM ((((("pg_type" "t"
     JOIN "pg_transform" "x" ON (("t"."oid" = "x"."trftype")))
     JOIN "pg_language" "l" ON (("x"."trflang" = "l"."oid")))
     JOIN "pg_proc" "p" ON ((("x"."trffromsql")::"oid" = "p"."oid")))
     JOIN "pg_namespace" "nt" ON (("t"."typnamespace" = "nt"."oid")))
     JOIN "pg_namespace" "np" ON (("p"."pronamespace" = "np"."oid")))
UNION
 SELECT ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("t"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    ("current_database"())::"information_schema"."sql_identifier" AS "specific_catalog",
    ("np"."nspname")::"information_schema"."sql_identifier" AS "specific_schema",
    ("nameconcatoid"("p"."proname", "p"."oid"))::"information_schema"."sql_identifier" AS "specific_name",
    ("l"."lanname")::"information_schema"."sql_identifier" AS "group_name",
    ('TO SQL'::character varying)::"information_schema"."character_data" AS "transform_type"
   FROM ((((("pg_type" "t"
     JOIN "pg_transform" "x" ON (("t"."oid" = "x"."trftype")))
     JOIN "pg_language" "l" ON (("x"."trflang" = "l"."oid")))
     JOIN "pg_proc" "p" ON ((("x"."trftosql")::"oid" = "p"."oid")))
     JOIN "pg_namespace" "nt" ON (("t"."typnamespace" = "nt"."oid")))
     JOIN "pg_namespace" "np" ON (("p"."pronamespace" = "np"."oid")))
  ORDER BY 1, 2, 3, 7, 8;`

const trQuotePathDefinition = ` SELECT ("current_database"())::"sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"sql_identifier" AS "udt_schema",
    ("t"."typname")::"sql_identifier" AS "udt_name",
    ("current_database"())::"sql_identifier" AS "specific_catalog",
    ("np"."nspname")::"sql_identifier" AS "specific_schema",
    ("nameconcatoid"("p"."proname", "p"."oid"))::"sql_identifier" AS "specific_name",
    ("l"."lanname")::"sql_identifier" AS "group_name",
    ('FROM SQL'::character varying)::"character_data" AS "transform_type"
   FROM ((((("pg_type" "t"
     JOIN "pg_transform" "x" ON (("t"."oid" = "x"."trftype")))
     JOIN "pg_language" "l" ON (("x"."trflang" = "l"."oid")))
     JOIN "pg_proc" "p" ON ((("x"."trffromsql")::"oid" = "p"."oid")))
     JOIN "pg_namespace" "nt" ON (("t"."typnamespace" = "nt"."oid")))
     JOIN "pg_namespace" "np" ON (("p"."pronamespace" = "np"."oid")))
UNION
 SELECT ("current_database"())::"sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"sql_identifier" AS "udt_schema",
    ("t"."typname")::"sql_identifier" AS "udt_name",
    ("current_database"())::"sql_identifier" AS "specific_catalog",
    ("np"."nspname")::"sql_identifier" AS "specific_schema",
    ("nameconcatoid"("p"."proname", "p"."oid"))::"sql_identifier" AS "specific_name",
    ("l"."lanname")::"sql_identifier" AS "group_name",
    ('TO SQL'::character varying)::"character_data" AS "transform_type"
   FROM ((((("pg_type" "t"
     JOIN "pg_transform" "x" ON (("t"."oid" = "x"."trftype")))
     JOIN "pg_language" "l" ON (("x"."trflang" = "l"."oid")))
     JOIN "pg_proc" "p" ON ((("x"."trftosql")::"oid" = "p"."oid")))
     JOIN "pg_namespace" "nt" ON (("t"."typnamespace" = "nt"."oid")))
     JOIN "pg_namespace" "np" ON (("p"."pronamespace" = "np"."oid")))
  ORDER BY 1, 2, 3, 7, 8;`

const trQuoteShadowDefinition = ` SELECT ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("t"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    ("current_database"())::"information_schema"."sql_identifier" AS "specific_catalog",
    ("np"."nspname")::"information_schema"."sql_identifier" AS "specific_schema",
    ("nameconcatoid"("p"."proname", "p"."oid"))::"information_schema"."sql_identifier" AS "specific_name",
    ("l"."lanname")::"information_schema"."sql_identifier" AS "group_name",
    ('FROM SQL'::character varying)::"information_schema"."character_data" AS "transform_type"
   FROM ((((("pg_type" "t"
     JOIN "pg_transform" "x" ON (("t"."oid" = "x"."trftype")))
     JOIN "pg_language" "l" ON (("x"."trflang" = "l"."oid")))
     JOIN "pg_proc" "p" ON ((("x"."trffromsql")::"oid" = "p"."oid")))
     JOIN "pg_catalog"."pg_namespace" "nt" ON (("t"."typnamespace" = "nt"."oid")))
     JOIN "pg_catalog"."pg_namespace" "np" ON (("p"."pronamespace" = "np"."oid")))
UNION
 SELECT ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("nt"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("t"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    ("current_database"())::"information_schema"."sql_identifier" AS "specific_catalog",
    ("np"."nspname")::"information_schema"."sql_identifier" AS "specific_schema",
    ("nameconcatoid"("p"."proname", "p"."oid"))::"information_schema"."sql_identifier" AS "specific_name",
    ("l"."lanname")::"information_schema"."sql_identifier" AS "group_name",
    ('TO SQL'::character varying)::"information_schema"."character_data" AS "transform_type"
   FROM ((((("pg_type" "t"
     JOIN "pg_transform" "x" ON (("t"."oid" = "x"."trftype")))
     JOIN "pg_language" "l" ON (("x"."trflang" = "l"."oid")))
     JOIN "pg_proc" "p" ON ((("x"."trftosql")::"oid" = "p"."oid")))
     JOIN "pg_catalog"."pg_namespace" "nt" ON (("t"."typnamespace" = "nt"."oid")))
     JOIN "pg_catalog"."pg_namespace" "np" ON (("p"."pronamespace" = "np"."oid")))
  ORDER BY 1, 2, 3, 7, 8;`
