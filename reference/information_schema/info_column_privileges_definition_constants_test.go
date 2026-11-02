package reference_test

// Independent REL_18_6 column_privileges expression and settings-sensitive
// presentation expectations; the pinned oracle resolves remaining spelling facts.

const cpDefaultDefinition = ` SELECT (u_grantor.rolname)::information_schema.sql_identifier AS grantor,
    (grantee.rolname)::information_schema.sql_identifier AS grantee,
    (current_database())::information_schema.sql_identifier AS table_catalog,
    (nc.nspname)::information_schema.sql_identifier AS table_schema,
    (x.relname)::information_schema.sql_identifier AS table_name,
    (x.attname)::information_schema.sql_identifier AS column_name,
    (x.prtype)::information_schema.character_data AS privilege_type,
    (
        CASE
            WHEN (pg_has_role(x.grantee, x.relowner, 'USAGE'::text) OR x.grantable) THEN 'YES'::text
            ELSE 'NO'::text
        END)::information_schema.yes_or_no AS is_grantable
   FROM ( SELECT pr_c.grantor,
            pr_c.grantee,
            a.attname,
            pr_c.relname,
            pr_c.relnamespace,
            pr_c.prtype,
            pr_c.grantable,
            pr_c.relowner
           FROM ( SELECT pg_class.oid,
                    pg_class.relname,
                    pg_class.relnamespace,
                    pg_class.relowner,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantor AS grantor,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantee AS grantee,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).privilege_type AS privilege_type,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).is_grantable AS is_grantable
                   FROM pg_class
                  WHERE (pg_class.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"]))) pr_c(oid, relname, relnamespace, relowner, grantor, grantee, prtype, grantable),
            pg_attribute a
          WHERE ((a.attrelid = pr_c.oid) AND (a.attnum > 0) AND (NOT a.attisdropped))
        UNION
         SELECT pr_a.grantor,
            pr_a.grantee,
            pr_a.attname,
            c.relname,
            c.relnamespace,
            pr_a.prtype,
            pr_a.grantable,
            c.relowner
           FROM ( SELECT a.attrelid,
                    a.attname,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).grantor AS grantor,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).grantee AS grantee,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).privilege_type AS privilege_type,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).is_grantable AS is_grantable
                   FROM (pg_attribute a
                     JOIN pg_class cc ON ((a.attrelid = cc.oid)))
                  WHERE ((a.attnum > 0) AND (NOT a.attisdropped))) pr_a(attrelid, attname, grantor, grantee, prtype, grantable),
            pg_class c
          WHERE ((pr_a.attrelid = c.oid) AND (c.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])))) x,
    pg_namespace nc,
    pg_authid u_grantor,
    ( SELECT pg_authid.oid,
            pg_authid.rolname
           FROM pg_authid
        UNION ALL
         SELECT (0)::oid AS oid,
            'PUBLIC'::name) grantee(oid, rolname)
  WHERE ((x.relnamespace = nc.oid) AND (x.grantee = grantee.oid) AND (x.grantor = u_grantor.oid) AND (x.prtype = ANY (ARRAY['INSERT'::text, 'SELECT'::text, 'UPDATE'::text, 'REFERENCES'::text])) AND (pg_has_role(u_grantor.oid, 'USAGE'::text) OR pg_has_role(grantee.oid, 'USAGE'::text) OR (grantee.rolname = 'PUBLIC'::name)));`

const cpPathDefinition = ` SELECT (u_grantor.rolname)::sql_identifier AS grantor,
    (grantee.rolname)::sql_identifier AS grantee,
    (current_database())::sql_identifier AS table_catalog,
    (nc.nspname)::sql_identifier AS table_schema,
    (x.relname)::sql_identifier AS table_name,
    (x.attname)::sql_identifier AS column_name,
    (x.prtype)::character_data AS privilege_type,
    (
        CASE
            WHEN (pg_has_role(x.grantee, x.relowner, 'USAGE'::text) OR x.grantable) THEN 'YES'::text
            ELSE 'NO'::text
        END)::yes_or_no AS is_grantable
   FROM ( SELECT pr_c.grantor,
            pr_c.grantee,
            a.attname,
            pr_c.relname,
            pr_c.relnamespace,
            pr_c.prtype,
            pr_c.grantable,
            pr_c.relowner
           FROM ( SELECT pg_class.oid,
                    pg_class.relname,
                    pg_class.relnamespace,
                    pg_class.relowner,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantor AS grantor,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantee AS grantee,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).privilege_type AS privilege_type,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).is_grantable AS is_grantable
                   FROM pg_class
                  WHERE (pg_class.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"]))) pr_c(oid, relname, relnamespace, relowner, grantor, grantee, prtype, grantable),
            pg_attribute a
          WHERE ((a.attrelid = pr_c.oid) AND (a.attnum > 0) AND (NOT a.attisdropped))
        UNION
         SELECT pr_a.grantor,
            pr_a.grantee,
            pr_a.attname,
            c.relname,
            c.relnamespace,
            pr_a.prtype,
            pr_a.grantable,
            c.relowner
           FROM ( SELECT a.attrelid,
                    a.attname,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).grantor AS grantor,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).grantee AS grantee,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).privilege_type AS privilege_type,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).is_grantable AS is_grantable
                   FROM (pg_attribute a
                     JOIN pg_class cc ON ((a.attrelid = cc.oid)))
                  WHERE ((a.attnum > 0) AND (NOT a.attisdropped))) pr_a(attrelid, attname, grantor, grantee, prtype, grantable),
            pg_class c
          WHERE ((pr_a.attrelid = c.oid) AND (c.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])))) x,
    pg_namespace nc,
    pg_authid u_grantor,
    ( SELECT pg_authid.oid,
            pg_authid.rolname
           FROM pg_authid
        UNION ALL
         SELECT (0)::oid AS oid,
            'PUBLIC'::name) grantee(oid, rolname)
  WHERE ((x.relnamespace = nc.oid) AND (x.grantee = grantee.oid) AND (x.grantor = u_grantor.oid) AND (x.prtype = ANY (ARRAY['INSERT'::text, 'SELECT'::text, 'UPDATE'::text, 'REFERENCES'::text])) AND (pg_has_role(u_grantor.oid, 'USAGE'::text) OR pg_has_role(grantee.oid, 'USAGE'::text) OR (grantee.rolname = 'PUBLIC'::name)));`

const cpShadowDefinition = ` SELECT (u_grantor.rolname)::information_schema.sql_identifier AS grantor,
    (grantee.rolname)::information_schema.sql_identifier AS grantee,
    (current_database())::information_schema.sql_identifier AS table_catalog,
    (nc.nspname)::information_schema.sql_identifier AS table_schema,
    (x.relname)::information_schema.sql_identifier AS table_name,
    (x.attname)::information_schema.sql_identifier AS column_name,
    (x.prtype)::information_schema.character_data AS privilege_type,
    (
        CASE
            WHEN (pg_has_role(x.grantee, x.relowner, 'USAGE'::text) OR x.grantable) THEN 'YES'::text
            ELSE 'NO'::text
        END)::information_schema.yes_or_no AS is_grantable
   FROM ( SELECT pr_c.grantor,
            pr_c.grantee,
            a.attname,
            pr_c.relname,
            pr_c.relnamespace,
            pr_c.prtype,
            pr_c.grantable,
            pr_c.relowner
           FROM ( SELECT pg_class.oid,
                    pg_class.relname,
                    pg_class.relnamespace,
                    pg_class.relowner,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantor AS grantor,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantee AS grantee,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).privilege_type AS privilege_type,
                    (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).is_grantable AS is_grantable
                   FROM pg_catalog.pg_class
                  WHERE (pg_class.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"]))) pr_c(oid, relname, relnamespace, relowner, grantor, grantee, prtype, grantable),
            pg_catalog.pg_attribute a
          WHERE ((a.attrelid = pr_c.oid) AND (a.attnum > 0) AND (NOT a.attisdropped))
        UNION
         SELECT pr_a.grantor,
            pr_a.grantee,
            pr_a.attname,
            c.relname,
            c.relnamespace,
            pr_a.prtype,
            pr_a.grantable,
            c.relowner
           FROM ( SELECT a.attrelid,
                    a.attname,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).grantor AS grantor,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).grantee AS grantee,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).privilege_type AS privilege_type,
                    (aclexplode(COALESCE(a.attacl, acldefault('c'::"char", cc.relowner)))).is_grantable AS is_grantable
                   FROM (pg_catalog.pg_attribute a
                     JOIN pg_catalog.pg_class cc ON ((a.attrelid = cc.oid)))
                  WHERE ((a.attnum > 0) AND (NOT a.attisdropped))) pr_a(attrelid, attname, grantor, grantee, prtype, grantable),
            pg_catalog.pg_class c
          WHERE ((pr_a.attrelid = c.oid) AND (c.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])))) x,
    pg_catalog.pg_namespace nc,
    pg_catalog.pg_authid u_grantor,
    ( SELECT pg_authid.oid,
            pg_authid.rolname
           FROM pg_catalog.pg_authid
        UNION ALL
         SELECT (0)::oid AS oid,
            'PUBLIC'::name) grantee(oid, rolname)
  WHERE ((x.relnamespace = nc.oid) AND (x.grantee = grantee.oid) AND (x.grantor = u_grantor.oid) AND (x.prtype = ANY (ARRAY['INSERT'::text, 'SELECT'::text, 'UPDATE'::text, 'REFERENCES'::text])) AND (pg_has_role(u_grantor.oid, 'USAGE'::text) OR pg_has_role(grantee.oid, 'USAGE'::text) OR (grantee.rolname = 'PUBLIC'::name)));`

const cpQuoteDefaultDefinition = ` SELECT ("u_grantor"."rolname")::"information_schema"."sql_identifier" AS "grantor",
    ("grantee"."rolname")::"information_schema"."sql_identifier" AS "grantee",
    ("current_database"())::"information_schema"."sql_identifier" AS "table_catalog",
    ("nc"."nspname")::"information_schema"."sql_identifier" AS "table_schema",
    ("x"."relname")::"information_schema"."sql_identifier" AS "table_name",
    ("x"."attname")::"information_schema"."sql_identifier" AS "column_name",
    ("x"."prtype")::"information_schema"."character_data" AS "privilege_type",
    (
        CASE
            WHEN ("pg_has_role"("x"."grantee", "x"."relowner", 'USAGE'::"text") OR "x"."grantable") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"information_schema"."yes_or_no" AS "is_grantable"
   FROM ( SELECT "pr_c"."grantor",
            "pr_c"."grantee",
            "a"."attname",
            "pr_c"."relname",
            "pr_c"."relnamespace",
            "pr_c"."prtype",
            "pr_c"."grantable",
            "pr_c"."relowner"
           FROM ( SELECT "pg_class"."oid",
                    "pg_class"."relname",
                    "pg_class"."relnamespace",
                    "pg_class"."relowner",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantor" AS "grantor",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantee" AS "grantee",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."privilege_type" AS "privilege_type",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."is_grantable" AS "is_grantable"
                   FROM "pg_class"
                  WHERE ("pg_class"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"]))) "pr_c"("oid", "relname", "relnamespace", "relowner", "grantor", "grantee", "prtype", "grantable"),
            "pg_attribute" "a"
          WHERE (("a"."attrelid" = "pr_c"."oid") AND ("a"."attnum" > 0) AND (NOT "a"."attisdropped"))
        UNION
         SELECT "pr_a"."grantor",
            "pr_a"."grantee",
            "pr_a"."attname",
            "c"."relname",
            "c"."relnamespace",
            "pr_a"."prtype",
            "pr_a"."grantable",
            "c"."relowner"
           FROM ( SELECT "a"."attrelid",
                    "a"."attname",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."grantor" AS "grantor",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."grantee" AS "grantee",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."privilege_type" AS "privilege_type",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."is_grantable" AS "is_grantable"
                   FROM ("pg_attribute" "a"
                     JOIN "pg_class" "cc" ON (("a"."attrelid" = "cc"."oid")))
                  WHERE (("a"."attnum" > 0) AND (NOT "a"."attisdropped"))) "pr_a"("attrelid", "attname", "grantor", "grantee", "prtype", "grantable"),
            "pg_class" "c"
          WHERE (("pr_a"."attrelid" = "c"."oid") AND ("c"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])))) "x",
    "pg_namespace" "nc",
    "pg_authid" "u_grantor",
    ( SELECT "pg_authid"."oid",
            "pg_authid"."rolname"
           FROM "pg_authid"
        UNION ALL
         SELECT (0)::"oid" AS "oid",
            'PUBLIC'::"name") "grantee"("oid", "rolname")
  WHERE (("x"."relnamespace" = "nc"."oid") AND ("x"."grantee" = "grantee"."oid") AND ("x"."grantor" = "u_grantor"."oid") AND ("x"."prtype" = ANY (ARRAY['INSERT'::"text", 'SELECT'::"text", 'UPDATE'::"text", 'REFERENCES'::"text"])) AND ("pg_has_role"("u_grantor"."oid", 'USAGE'::"text") OR "pg_has_role"("grantee"."oid", 'USAGE'::"text") OR ("grantee"."rolname" = 'PUBLIC'::"name")));`

const cpQuotePathDefinition = ` SELECT ("u_grantor"."rolname")::"sql_identifier" AS "grantor",
    ("grantee"."rolname")::"sql_identifier" AS "grantee",
    ("current_database"())::"sql_identifier" AS "table_catalog",
    ("nc"."nspname")::"sql_identifier" AS "table_schema",
    ("x"."relname")::"sql_identifier" AS "table_name",
    ("x"."attname")::"sql_identifier" AS "column_name",
    ("x"."prtype")::"character_data" AS "privilege_type",
    (
        CASE
            WHEN ("pg_has_role"("x"."grantee", "x"."relowner", 'USAGE'::"text") OR "x"."grantable") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"yes_or_no" AS "is_grantable"
   FROM ( SELECT "pr_c"."grantor",
            "pr_c"."grantee",
            "a"."attname",
            "pr_c"."relname",
            "pr_c"."relnamespace",
            "pr_c"."prtype",
            "pr_c"."grantable",
            "pr_c"."relowner"
           FROM ( SELECT "pg_class"."oid",
                    "pg_class"."relname",
                    "pg_class"."relnamespace",
                    "pg_class"."relowner",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantor" AS "grantor",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantee" AS "grantee",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."privilege_type" AS "privilege_type",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."is_grantable" AS "is_grantable"
                   FROM "pg_class"
                  WHERE ("pg_class"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"]))) "pr_c"("oid", "relname", "relnamespace", "relowner", "grantor", "grantee", "prtype", "grantable"),
            "pg_attribute" "a"
          WHERE (("a"."attrelid" = "pr_c"."oid") AND ("a"."attnum" > 0) AND (NOT "a"."attisdropped"))
        UNION
         SELECT "pr_a"."grantor",
            "pr_a"."grantee",
            "pr_a"."attname",
            "c"."relname",
            "c"."relnamespace",
            "pr_a"."prtype",
            "pr_a"."grantable",
            "c"."relowner"
           FROM ( SELECT "a"."attrelid",
                    "a"."attname",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."grantor" AS "grantor",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."grantee" AS "grantee",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."privilege_type" AS "privilege_type",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."is_grantable" AS "is_grantable"
                   FROM ("pg_attribute" "a"
                     JOIN "pg_class" "cc" ON (("a"."attrelid" = "cc"."oid")))
                  WHERE (("a"."attnum" > 0) AND (NOT "a"."attisdropped"))) "pr_a"("attrelid", "attname", "grantor", "grantee", "prtype", "grantable"),
            "pg_class" "c"
          WHERE (("pr_a"."attrelid" = "c"."oid") AND ("c"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])))) "x",
    "pg_namespace" "nc",
    "pg_authid" "u_grantor",
    ( SELECT "pg_authid"."oid",
            "pg_authid"."rolname"
           FROM "pg_authid"
        UNION ALL
         SELECT (0)::"oid" AS "oid",
            'PUBLIC'::"name") "grantee"("oid", "rolname")
  WHERE (("x"."relnamespace" = "nc"."oid") AND ("x"."grantee" = "grantee"."oid") AND ("x"."grantor" = "u_grantor"."oid") AND ("x"."prtype" = ANY (ARRAY['INSERT'::"text", 'SELECT'::"text", 'UPDATE'::"text", 'REFERENCES'::"text"])) AND ("pg_has_role"("u_grantor"."oid", 'USAGE'::"text") OR "pg_has_role"("grantee"."oid", 'USAGE'::"text") OR ("grantee"."rolname" = 'PUBLIC'::"name")));`

const cpQuoteShadowDefinition = ` SELECT ("u_grantor"."rolname")::"information_schema"."sql_identifier" AS "grantor",
    ("grantee"."rolname")::"information_schema"."sql_identifier" AS "grantee",
    ("current_database"())::"information_schema"."sql_identifier" AS "table_catalog",
    ("nc"."nspname")::"information_schema"."sql_identifier" AS "table_schema",
    ("x"."relname")::"information_schema"."sql_identifier" AS "table_name",
    ("x"."attname")::"information_schema"."sql_identifier" AS "column_name",
    ("x"."prtype")::"information_schema"."character_data" AS "privilege_type",
    (
        CASE
            WHEN ("pg_has_role"("x"."grantee", "x"."relowner", 'USAGE'::"text") OR "x"."grantable") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"information_schema"."yes_or_no" AS "is_grantable"
   FROM ( SELECT "pr_c"."grantor",
            "pr_c"."grantee",
            "a"."attname",
            "pr_c"."relname",
            "pr_c"."relnamespace",
            "pr_c"."prtype",
            "pr_c"."grantable",
            "pr_c"."relowner"
           FROM ( SELECT "pg_class"."oid",
                    "pg_class"."relname",
                    "pg_class"."relnamespace",
                    "pg_class"."relowner",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantor" AS "grantor",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantee" AS "grantee",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."privilege_type" AS "privilege_type",
                    ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."is_grantable" AS "is_grantable"
                   FROM "pg_catalog"."pg_class"
                  WHERE ("pg_class"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"]))) "pr_c"("oid", "relname", "relnamespace", "relowner", "grantor", "grantee", "prtype", "grantable"),
            "pg_catalog"."pg_attribute" "a"
          WHERE (("a"."attrelid" = "pr_c"."oid") AND ("a"."attnum" > 0) AND (NOT "a"."attisdropped"))
        UNION
         SELECT "pr_a"."grantor",
            "pr_a"."grantee",
            "pr_a"."attname",
            "c"."relname",
            "c"."relnamespace",
            "pr_a"."prtype",
            "pr_a"."grantable",
            "c"."relowner"
           FROM ( SELECT "a"."attrelid",
                    "a"."attname",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."grantor" AS "grantor",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."grantee" AS "grantee",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."privilege_type" AS "privilege_type",
                    ("aclexplode"(COALESCE("a"."attacl", "acldefault"('c'::"char", "cc"."relowner"))))."is_grantable" AS "is_grantable"
                   FROM ("pg_catalog"."pg_attribute" "a"
                     JOIN "pg_catalog"."pg_class" "cc" ON (("a"."attrelid" = "cc"."oid")))
                  WHERE (("a"."attnum" > 0) AND (NOT "a"."attisdropped"))) "pr_a"("attrelid", "attname", "grantor", "grantee", "prtype", "grantable"),
            "pg_catalog"."pg_class" "c"
          WHERE (("pr_a"."attrelid" = "c"."oid") AND ("c"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])))) "x",
    "pg_catalog"."pg_namespace" "nc",
    "pg_catalog"."pg_authid" "u_grantor",
    ( SELECT "pg_authid"."oid",
            "pg_authid"."rolname"
           FROM "pg_catalog"."pg_authid"
        UNION ALL
         SELECT (0)::"oid" AS "oid",
            'PUBLIC'::"name") "grantee"("oid", "rolname")
  WHERE (("x"."relnamespace" = "nc"."oid") AND ("x"."grantee" = "grantee"."oid") AND ("x"."grantor" = "u_grantor"."oid") AND ("x"."prtype" = ANY (ARRAY['INSERT'::"text", 'SELECT'::"text", 'UPDATE'::"text", 'REFERENCES'::"text"])) AND ("pg_has_role"("u_grantor"."oid", 'USAGE'::"text") OR "pg_has_role"("grantee"."oid", 'USAGE'::"text") OR ("grantee"."rolname" = 'PUBLIC'::"name")));`
