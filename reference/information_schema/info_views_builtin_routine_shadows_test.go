package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// These six complete definitions were observed independently on the pinned
// PostgreSQL 18.6 oracle. Overload shadows must affect only the matching call;
// function qualification must never qualify its range-table alias.
var ivBuiltinRoutineShadowDefinitions = map[string]string{
	"three_argument_role.false": ` SELECT (u_grantor.rolname)::information_schema.sql_identifier AS grantor,
    (grantee.rolname)::information_schema.sql_identifier AS grantee,
    (current_database())::information_schema.sql_identifier AS table_catalog,
    (nc.nspname)::information_schema.sql_identifier AS table_schema,
    (c.relname)::information_schema.sql_identifier AS table_name,
    (c.prtype)::information_schema.character_data AS privilege_type,
    (
        CASE
            WHEN (pg_catalog.pg_has_role(grantee.oid, c.relowner, 'USAGE'::text) OR c.grantable) THEN 'YES'::text
            ELSE 'NO'::text
        END)::information_schema.yes_or_no AS is_grantable,
    (
        CASE
            WHEN (c.prtype = 'SELECT'::text) THEN 'YES'::text
            ELSE 'NO'::text
        END)::information_schema.yes_or_no AS with_hierarchy
   FROM ( SELECT pg_class.oid,
            pg_class.relname,
            pg_class.relnamespace,
            pg_class.relkind,
            pg_class.relowner,
            (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantor AS grantor,
            (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantee AS grantee,
            (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).privilege_type AS privilege_type,
            (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).is_grantable AS is_grantable
           FROM pg_class) c(oid, relname, relnamespace, relkind, relowner, grantor, grantee, prtype, grantable),
    pg_namespace nc,
    pg_authid u_grantor,
    ( SELECT pg_authid.oid,
            pg_authid.rolname
           FROM pg_authid
        UNION ALL
         SELECT (0)::oid AS oid,
            'PUBLIC'::name) grantee(oid, rolname)
  WHERE ((c.relnamespace = nc.oid) AND (c.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])) AND (c.grantee = grantee.oid) AND (c.grantor = u_grantor.oid) AND (c.prtype = ANY (ARRAY['INSERT'::text, 'SELECT'::text, 'UPDATE'::text, 'DELETE'::text, 'TRUNCATE'::text, 'REFERENCES'::text, 'TRIGGER'::text])) AND (pg_has_role(u_grantor.oid, 'USAGE'::text) OR pg_has_role(grantee.oid, 'USAGE'::text) OR (grantee.rolname = 'PUBLIC'::name)));`,
	"three_argument_role.true": ` SELECT ("u_grantor"."rolname")::"information_schema"."sql_identifier" AS "grantor",
    ("grantee"."rolname")::"information_schema"."sql_identifier" AS "grantee",
    ("current_database"())::"information_schema"."sql_identifier" AS "table_catalog",
    ("nc"."nspname")::"information_schema"."sql_identifier" AS "table_schema",
    ("c"."relname")::"information_schema"."sql_identifier" AS "table_name",
    ("c"."prtype")::"information_schema"."character_data" AS "privilege_type",
    (
        CASE
            WHEN ("pg_catalog"."pg_has_role"("grantee"."oid", "c"."relowner", 'USAGE'::"text") OR "c"."grantable") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"information_schema"."yes_or_no" AS "is_grantable",
    (
        CASE
            WHEN ("c"."prtype" = 'SELECT'::"text") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"information_schema"."yes_or_no" AS "with_hierarchy"
   FROM ( SELECT "pg_class"."oid",
            "pg_class"."relname",
            "pg_class"."relnamespace",
            "pg_class"."relkind",
            "pg_class"."relowner",
            ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantor" AS "grantor",
            ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantee" AS "grantee",
            ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."privilege_type" AS "privilege_type",
            ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."is_grantable" AS "is_grantable"
           FROM "pg_class") "c"("oid", "relname", "relnamespace", "relkind", "relowner", "grantor", "grantee", "prtype", "grantable"),
    "pg_namespace" "nc",
    "pg_authid" "u_grantor",
    ( SELECT "pg_authid"."oid",
            "pg_authid"."rolname"
           FROM "pg_authid"
        UNION ALL
         SELECT (0)::"oid" AS "oid",
            'PUBLIC'::"name") "grantee"("oid", "rolname")
  WHERE (("c"."relnamespace" = "nc"."oid") AND ("c"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])) AND ("c"."grantee" = "grantee"."oid") AND ("c"."grantor" = "u_grantor"."oid") AND ("c"."prtype" = ANY (ARRAY['INSERT'::"text", 'SELECT'::"text", 'UPDATE'::"text", 'DELETE'::"text", 'TRUNCATE'::"text", 'REFERENCES'::"text", 'TRIGGER'::"text"])) AND ("pg_has_role"("u_grantor"."oid", 'USAGE'::"text") OR "pg_has_role"("grantee"."oid", 'USAGE'::"text") OR ("grantee"."rolname" = 'PUBLIC'::"name")));`,
	"two_argument_control.false": ` SELECT (u_grantor.rolname)::information_schema.sql_identifier AS grantor,
    (grantee.rolname)::information_schema.sql_identifier AS grantee,
    (current_database())::information_schema.sql_identifier AS table_catalog,
    (nc.nspname)::information_schema.sql_identifier AS table_schema,
    (c.relname)::information_schema.sql_identifier AS table_name,
    (c.prtype)::information_schema.character_data AS privilege_type,
    (
        CASE
            WHEN (pg_has_role(grantee.oid, c.relowner, 'USAGE'::text) OR c.grantable) THEN 'YES'::text
            ELSE 'NO'::text
        END)::information_schema.yes_or_no AS is_grantable,
    (
        CASE
            WHEN (c.prtype = 'SELECT'::text) THEN 'YES'::text
            ELSE 'NO'::text
        END)::information_schema.yes_or_no AS with_hierarchy
   FROM ( SELECT pg_class.oid,
            pg_class.relname,
            pg_class.relnamespace,
            pg_class.relkind,
            pg_class.relowner,
            (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantor AS grantor,
            (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).grantee AS grantee,
            (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).privilege_type AS privilege_type,
            (aclexplode(COALESCE(pg_class.relacl, acldefault('r'::"char", pg_class.relowner)))).is_grantable AS is_grantable
           FROM pg_class) c(oid, relname, relnamespace, relkind, relowner, grantor, grantee, prtype, grantable),
    pg_namespace nc,
    pg_authid u_grantor,
    ( SELECT pg_authid.oid,
            pg_authid.rolname
           FROM pg_authid
        UNION ALL
         SELECT (0)::oid AS oid,
            'PUBLIC'::name) grantee(oid, rolname)
  WHERE ((c.relnamespace = nc.oid) AND (c.relkind = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])) AND (c.grantee = grantee.oid) AND (c.grantor = u_grantor.oid) AND (c.prtype = ANY (ARRAY['INSERT'::text, 'SELECT'::text, 'UPDATE'::text, 'DELETE'::text, 'TRUNCATE'::text, 'REFERENCES'::text, 'TRIGGER'::text])) AND (pg_catalog.pg_has_role(u_grantor.oid, 'USAGE'::text) OR pg_catalog.pg_has_role(grantee.oid, 'USAGE'::text) OR (grantee.rolname = 'PUBLIC'::name)));`,
	"two_argument_control.true": ` SELECT ("u_grantor"."rolname")::"information_schema"."sql_identifier" AS "grantor",
    ("grantee"."rolname")::"information_schema"."sql_identifier" AS "grantee",
    ("current_database"())::"information_schema"."sql_identifier" AS "table_catalog",
    ("nc"."nspname")::"information_schema"."sql_identifier" AS "table_schema",
    ("c"."relname")::"information_schema"."sql_identifier" AS "table_name",
    ("c"."prtype")::"information_schema"."character_data" AS "privilege_type",
    (
        CASE
            WHEN ("pg_has_role"("grantee"."oid", "c"."relowner", 'USAGE'::"text") OR "c"."grantable") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"information_schema"."yes_or_no" AS "is_grantable",
    (
        CASE
            WHEN ("c"."prtype" = 'SELECT'::"text") THEN 'YES'::"text"
            ELSE 'NO'::"text"
        END)::"information_schema"."yes_or_no" AS "with_hierarchy"
   FROM ( SELECT "pg_class"."oid",
            "pg_class"."relname",
            "pg_class"."relnamespace",
            "pg_class"."relkind",
            "pg_class"."relowner",
            ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantor" AS "grantor",
            ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."grantee" AS "grantee",
            ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."privilege_type" AS "privilege_type",
            ("aclexplode"(COALESCE("pg_class"."relacl", "acldefault"('r'::"char", "pg_class"."relowner"))))."is_grantable" AS "is_grantable"
           FROM "pg_class") "c"("oid", "relname", "relnamespace", "relkind", "relowner", "grantor", "grantee", "prtype", "grantable"),
    "pg_namespace" "nc",
    "pg_authid" "u_grantor",
    ( SELECT "pg_authid"."oid",
            "pg_authid"."rolname"
           FROM "pg_authid"
        UNION ALL
         SELECT (0)::"oid" AS "oid",
            'PUBLIC'::"name") "grantee"("oid", "rolname")
  WHERE (("c"."relnamespace" = "nc"."oid") AND ("c"."relkind" = ANY (ARRAY['r'::"char", 'v'::"char", 'f'::"char", 'p'::"char"])) AND ("c"."grantee" = "grantee"."oid") AND ("c"."grantor" = "u_grantor"."oid") AND ("c"."prtype" = ANY (ARRAY['INSERT'::"text", 'SELECT'::"text", 'UPDATE'::"text", 'DELETE'::"text", 'TRUNCATE'::"text", 'REFERENCES'::"text", 'TRIGGER'::"text"])) AND ("pg_catalog"."pg_has_role"("u_grantor"."oid", 'USAGE'::"text") OR "pg_catalog"."pg_has_role"("grantee"."oid", 'USAGE'::"text") OR ("grantee"."rolname" = 'PUBLIC'::"name")));`,
	"timezone_alias.false": ` SELECT name,
    abbrev,
    utc_offset,
    is_dst
   FROM pg_catalog.pg_timezone_names() pg_timezone_names(name, abbrev, utc_offset, is_dst);`,
	"timezone_alias.true": ` SELECT "name",
    "abbrev",
    "utc_offset",
    "is_dst"
   FROM "pg_catalog"."pg_timezone_names"() "pg_timezone_names"("name", "abbrev", "utc_offset", "is_dst");`,
}

func TestReferenceInfoViewsBuiltinRoutineShadows(t *testing.T) {
	for _, tc := range []struct{ name, ddl, schema, view string }{
		{"three_argument_role", "CREATE FUNCTION public.pg_has_role(oid,oid,text) RETURNS boolean BEGIN ATOMIC SELECT true; END", "information_schema", "table_privileges"},
		{"two_argument_control", "CREATE FUNCTION public.pg_has_role(oid,text) RETURNS boolean BEGIN ATOMIC SELECT true; END", "information_schema", "table_privileges"},
		{"timezone_alias", "CREATE FUNCTION public.pg_timezone_names() RETURNS integer BEGIN ATOMIC SELECT 1; END", "pg_catalog", "pg_timezone_names"},
	} {
		for _, quote := range []struct{ key, setting string }{{"false", "off"}, {"true", "on"}} {
			t.Run(tc.name+"/quote_"+quote.setting, func(t *testing.T) {
				ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec(tc.ddl, 0))
					definition := ivBuiltinRoutineShadowDefinitions[tc.name+"."+quote.key]
					for _, consumer := range []string{"information_schema_views", "pg_views"} {
						for _, route := range []string{"direct", "prepared", "held"} {
							t.Run(consumer+"/"+route, func(t *testing.T) {
								// Prepare held consumers before changing display settings, so
								// their output must reflect execution-time path and quoting.
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public", 0))
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers=off", 0))
								s := ivStep(b.PG, ivSelect, [][]any{ivRow(database, tc.schema, tc.view, definition, "NONE", "NO", "NO")}, tc.schema, tc.view)
								if consumer == "pg_views" {
									s = fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname=$1 AND viewname=$2", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}, tc.schema, tc.view)
								}
								execute := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
								if route == "held" {
									execute = ivHeld(t, p, b, c, s.Query)
								}
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public,pg_catalog", 0))
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers="+quote.setting, 0))
								if route == "prepared" {
									s = fixture.Prepared(s)
								}
								execute(s)
							})
						}
					}
				})
			})
		}
	}
}
