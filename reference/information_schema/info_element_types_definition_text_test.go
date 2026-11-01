package reference_test

// Independent released-view expectations, corrected against the pinned
// PostgreSQL 18.6 UTF8/C oracle at checkpoint A; never derived from native output.
const etDefinition = ` SELECT (current_database())::information_schema.sql_identifier AS object_catalog,
    (n.nspname)::information_schema.sql_identifier AS object_schema,
    x.objname AS object_name,
    (x.objtype)::information_schema.character_data AS object_type,
    (x.objdtdid)::information_schema.sql_identifier AS collection_type_identifier,
    (
        CASE
            WHEN (nbt.nspname = 'pg_catalog'::name) THEN format_type(bt.oid, NULL::integer)
            ELSE 'USER-DEFINED'::text
        END)::information_schema.character_data AS data_type,
    (NULL::integer)::information_schema.cardinal_number AS character_maximum_length,
    (NULL::integer)::information_schema.cardinal_number AS character_octet_length,
    (NULL::name)::information_schema.sql_identifier AS character_set_catalog,
    (NULL::name)::information_schema.sql_identifier AS character_set_schema,
    (NULL::name)::information_schema.sql_identifier AS character_set_name,
    (
        CASE
            WHEN (nco.nspname IS NOT NULL) THEN current_database()
            ELSE NULL::name
        END)::information_schema.sql_identifier AS collation_catalog,
    (nco.nspname)::information_schema.sql_identifier AS collation_schema,
    (co.collname)::information_schema.sql_identifier AS collation_name,
    (NULL::integer)::information_schema.cardinal_number AS numeric_precision,
    (NULL::integer)::information_schema.cardinal_number AS numeric_precision_radix,
    (NULL::integer)::information_schema.cardinal_number AS numeric_scale,
    (NULL::integer)::information_schema.cardinal_number AS datetime_precision,
    (NULL::character varying)::information_schema.character_data AS interval_type,
    (NULL::integer)::information_schema.cardinal_number AS interval_precision,
    (current_database())::information_schema.sql_identifier AS udt_catalog,
    (nbt.nspname)::information_schema.sql_identifier AS udt_schema,
    (bt.typname)::information_schema.sql_identifier AS udt_name,
    (NULL::name)::information_schema.sql_identifier AS scope_catalog,
    (NULL::name)::information_schema.sql_identifier AS scope_schema,
    (NULL::name)::information_schema.sql_identifier AS scope_name,
    (NULL::integer)::information_schema.cardinal_number AS maximum_cardinality,
    (('a'::text || (x.objdtdid)::text))::information_schema.sql_identifier AS dtd_identifier
   FROM pg_namespace n,
    pg_type at,
    pg_namespace nbt,
    pg_type bt,
    (( SELECT c.relnamespace,
            (c.relname)::information_schema.sql_identifier AS relname,
                CASE
                    WHEN (c.relkind = 'c'::"char") THEN 'USER-DEFINED TYPE'::text
                    ELSE 'TABLE'::text
                END AS "case",
            a.attnum,
            a.atttypid,
            a.attcollation
           FROM pg_class c,
            pg_attribute a
          WHERE ((c.oid = a.attrelid) AND (c.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'c'::"char", 'p'::"char"])) AND (a.attnum > 0) AND (NOT a.attisdropped))
        UNION ALL
         SELECT t.typnamespace,
            (t.typname)::information_schema.sql_identifier AS typname,
            'DOMAIN'::text AS text,
            1,
            t.typbasetype,
            t.typcollation
           FROM pg_type t
          WHERE (t.typtype = 'd'::"char")
        UNION ALL
         SELECT ss.pronamespace,
            (nameconcatoid(ss.proname, ss.oid))::information_schema.sql_identifier AS nameconcatoid,
            'ROUTINE'::text AS text,
            (ss.x).n AS n,
            (ss.x).x AS x,
            0
           FROM ( SELECT p.pronamespace,
                    p.proname,
                    p.oid,
                    information_schema._pg_expandarray(COALESCE(p.proallargtypes, (p.proargtypes)::oid[])) AS x
                   FROM pg_proc p) ss
        UNION ALL
         SELECT p.pronamespace,
            (nameconcatoid(p.proname, p.oid))::information_schema.sql_identifier AS nameconcatoid,
            'ROUTINE'::text AS text,
            0,
            p.prorettype,
            0
           FROM pg_proc p) x(objschema, objname, objtype, objdtdid, objtypeid, objcollation)
     LEFT JOIN (pg_collation co
     JOIN pg_namespace nco ON ((co.collnamespace = nco.oid))) ON (((x.objcollation = co.oid) AND ((nco.nspname <> 'pg_catalog'::name) OR (co.collname <> 'default'::name)))))
  WHERE ((n.oid = x.objschema) AND (at.oid = x.objtypeid) AND ((at.typelem <> (0)::oid) AND (at.typlen = '-1'::integer)) AND (at.typelem = bt.oid) AND (nbt.oid = bt.typnamespace) AND ((n.nspname, (x.objname)::name, x.objtype, ((x.objdtdid)::information_schema.sql_identifier)::name) IN ( SELECT data_type_privileges.object_schema,
            data_type_privileges.object_name,
            data_type_privileges.object_type,
            data_type_privileges.dtd_identifier
           FROM information_schema.data_type_privileges)));`

const etQuoteDefinition = ` SELECT ("current_database"())::"information_schema"."sql_identifier" AS "object_catalog",
    ("n"."nspname")::"information_schema"."sql_identifier" AS "object_schema",
    "x"."objname" AS "object_name",
    ("x"."objtype")::"information_schema"."character_data" AS "object_type",
    ("x"."objdtdid")::"information_schema"."sql_identifier" AS "collection_type_identifier",
    (
        CASE
            WHEN ("nbt"."nspname" = 'pg_catalog'::"name") THEN "format_type"("bt"."oid", NULL::integer)
            ELSE 'USER-DEFINED'::"text"
        END)::"information_schema"."character_data" AS "data_type",
    (NULL::integer)::"information_schema"."cardinal_number" AS "character_maximum_length",
    (NULL::integer)::"information_schema"."cardinal_number" AS "character_octet_length",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_catalog",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_schema",
    (NULL::"name")::"information_schema"."sql_identifier" AS "character_set_name",
    (
        CASE
            WHEN ("nco"."nspname" IS NOT NULL) THEN "current_database"()
            ELSE NULL::"name"
        END)::"information_schema"."sql_identifier" AS "collation_catalog",
    ("nco"."nspname")::"information_schema"."sql_identifier" AS "collation_schema",
    ("co"."collname")::"information_schema"."sql_identifier" AS "collation_name",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_precision",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_precision_radix",
    (NULL::integer)::"information_schema"."cardinal_number" AS "numeric_scale",
    (NULL::integer)::"information_schema"."cardinal_number" AS "datetime_precision",
    (NULL::character varying)::"information_schema"."character_data" AS "interval_type",
    (NULL::integer)::"information_schema"."cardinal_number" AS "interval_precision",
    ("current_database"())::"information_schema"."sql_identifier" AS "udt_catalog",
    ("nbt"."nspname")::"information_schema"."sql_identifier" AS "udt_schema",
    ("bt"."typname")::"information_schema"."sql_identifier" AS "udt_name",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_catalog",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_schema",
    (NULL::"name")::"information_schema"."sql_identifier" AS "scope_name",
    (NULL::integer)::"information_schema"."cardinal_number" AS "maximum_cardinality",
    (('a'::"text" || ("x"."objdtdid")::"text"))::"information_schema"."sql_identifier" AS "dtd_identifier"
   FROM "pg_namespace" "n",
    "pg_type" "at",
    "pg_namespace" "nbt",
    "pg_type" "bt",
    (( SELECT "c"."relnamespace",
            ("c"."relname")::"information_schema"."sql_identifier" AS "relname",
                CASE
                    WHEN ("c"."relkind" = 'c'::"char") THEN 'USER-DEFINED TYPE'::"text"
                    ELSE 'TABLE'::"text"
                END AS "case",
            "a"."attnum",
            "a"."atttypid",
            "a"."attcollation"
           FROM "pg_class" "c",
            "pg_attribute" "a"
          WHERE (("c"."oid" = "a"."attrelid") AND ("c"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'c'::"char", 'p'::"char"])) AND ("a"."attnum" > 0) AND (NOT "a"."attisdropped"))
        UNION ALL
         SELECT "t"."typnamespace",
            ("t"."typname")::"information_schema"."sql_identifier" AS "typname",
            'DOMAIN'::"text" AS "text",
            1,
            "t"."typbasetype",
            "t"."typcollation"
           FROM "pg_type" "t"
          WHERE ("t"."typtype" = 'd'::"char")
        UNION ALL
         SELECT "ss"."pronamespace",
            ("nameconcatoid"("ss"."proname", "ss"."oid"))::"information_schema"."sql_identifier" AS "nameconcatoid",
            'ROUTINE'::"text" AS "text",
            ("ss"."x")."n" AS "n",
            ("ss"."x")."x" AS "x",
            0
           FROM ( SELECT "p"."pronamespace",
                    "p"."proname",
                    "p"."oid",
                    "information_schema"."_pg_expandarray"(COALESCE("p"."proallargtypes", ("p"."proargtypes")::"oid"[])) AS "x"
                   FROM "pg_proc" "p") "ss"
        UNION ALL
         SELECT "p"."pronamespace",
            ("nameconcatoid"("p"."proname", "p"."oid"))::"information_schema"."sql_identifier" AS "nameconcatoid",
            'ROUTINE'::"text" AS "text",
            0,
            "p"."prorettype",
            0
           FROM "pg_proc" "p") "x"("objschema", "objname", "objtype", "objdtdid", "objtypeid", "objcollation")
     LEFT JOIN ("pg_collation" "co"
     JOIN "pg_namespace" "nco" ON (("co"."collnamespace" = "nco"."oid"))) ON ((("x"."objcollation" = "co"."oid") AND (("nco"."nspname" <> 'pg_catalog'::"name") OR ("co"."collname" <> 'default'::"name")))))
  WHERE (("n"."oid" = "x"."objschema") AND ("at"."oid" = "x"."objtypeid") AND (("at"."typelem" <> (0)::"oid") AND ("at"."typlen" = '-1'::integer)) AND ("at"."typelem" = "bt"."oid") AND ("nbt"."oid" = "bt"."typnamespace") AND (("n"."nspname", ("x"."objname")::"name", "x"."objtype", (("x"."objdtdid")::"information_schema"."sql_identifier")::"name") IN ( SELECT "data_type_privileges"."object_schema",
            "data_type_privileges"."object_name",
            "data_type_privileges"."object_type",
            "data_type_privileges"."dtd_identifier"
           FROM "information_schema"."data_type_privileges")));`
