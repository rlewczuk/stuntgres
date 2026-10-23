package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Complete definitions independently observed on pinned PostgreSQL 18.6.
// Keep the release strings independent of either native definition provider.
var infoViewsBuiltinIdentityDefinitions = map[string]string{
	"renamed.off": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    iv_identity_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
	"renamed.on": ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "iv_identity_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`,
	"old_name_competitor.off": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    iv_identity_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
	"old_name_competitor.on": ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "iv_identity_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`,
	"moved_hidden.off": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    iv_identity.iv_identity_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
	"moved_hidden.on": ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "iv_identity"."iv_identity_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`,
	"moved_visible.off": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    iv_identity_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
	"moved_visible.on": ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "iv_identity_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`,
	"moved_shadow.off": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    iv_identity.iv_identity_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
	"moved_shadow.on": ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "iv_identity"."iv_identity_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`,
	"pure_moved_hidden.off": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    iv_identity.pg_get_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
	"pure_moved_hidden.on": ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "iv_identity"."pg_get_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`,
	"pure_moved_visible.off": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    pg_get_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
	"pure_moved_visible.on": ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "pg_get_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`,
	"pure_moved_shadow.off": ` SELECT n.nspname AS schemaname,
    c.relname AS viewname,
    iv_identity.pg_get_userbyid(c.relowner) AS viewowner,
    pg_get_viewdef(c.oid) AS definition
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'v'::"char");`,
	"pure_moved_shadow.on": ` SELECT "n"."nspname" AS "schemaname",
    "c"."relname" AS "viewname",
    "iv_identity"."pg_get_userbyid"("c"."relowner") AS "viewowner",
    "pg_get_viewdef"("c"."oid") AS "definition"
   FROM ("pg_class" "c"
     LEFT JOIN "pg_namespace" "n" ON (("n"."oid" = "c"."relnamespace")))
  WHERE ("c"."relkind" = 'v'::"char");`,
}

func TestReferenceInfoViewsBuiltinRoutineIdentity(t *testing.T) {
	for _, scenario := range []string{"rename_and_move", "pure_move"} {
		t.Run(scenario, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "iv_identity")
			routineBuiltinCaptureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				restore := routineAlterBuiltinCleanup(t, p, c, "pg_catalog.pg_get_userbyid(oid)")
				defer restore()
				var originalOID int64
				var database string
				if err := c.QueryRowContext(p.Ctx, "SELECT p.oid,current_database() FROM pg_proc p WHERE p.oid=$1::regprocedure", "pg_catalog.pg_get_userbyid(oid)").Scan(&originalOID, &database); err != nil {
					t.Fatal(err)
				}
				routineExec(t, p, b, c, "CREATE SCHEMA iv_identity", "SET search_path=public,pg_catalog", "SET quote_all_identifiers=off")
				queries := []string{
					"SELECT * FROM information_schema.views WHERE table_schema=$1 AND table_name=$2",
					"SELECT definition FROM pg_catalog.pg_views WHERE schemaname=$1 AND viewname=$2",
				}
				held := make([]fixture.HeldStatement, len(queries))
				for i, query := range queries {
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				}
				check := func(t *testing.T, definition string) {
					t.Helper()
					for i, query := range queries {
						step := fixture.Select(query,
							[]string{"table_catalog", "table_schema", "table_name", "view_definition", "check_option", "is_updatable", "is_insertable_into", "is_trigger_updatable", "is_trigger_deletable", "is_trigger_insertable_into"},
							[]string{"NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"},
							[][]any{{database, "pg_catalog", "pg_views", definition, "NONE", "NO", "NO", "NO", "NO", "NO"}}, "pg_catalog", "pg_views")
						if i == 1 {
							step = fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}, "pg_catalog", "pg_views")
						} else {
							step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
								columns, err := rows.ColumnTypes()
								if err != nil {
									t.Fatal(err)
								}
								for j, column := range columns {
									nullable, known := column.Nullable()
									if nullable != !b.PG || known != !b.PG {
										t.Fatalf("column %d nullability=%t/%t", j, nullable, known)
									}
								}
							}
						}
						t.Run([]string{"information_schema_views", "pg_views"}[i], func(t *testing.T) {
							t.Run("direct", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) })
							t.Run("prepared", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step)) })
							t.Run("held", func(t *testing.T) { fixture.RunStep(t, p.Ctx, held[i], b.Recorder, step) })
						})
					}
				}
				// Baseline executes the same statements retained through all mutations.
				check(t, infoViewsBuiltinIdentityDefinitions["pure_moved_visible.off"])
				type state struct {
					name, ddl, path, schema, routine, competitor string
				}
				states := []state{
					{"renamed", "ALTER FUNCTION pg_catalog.pg_get_userbyid(oid) RENAME TO iv_identity_userbyid", "public,pg_catalog", "pg_catalog", "iv_identity_userbyid", ""},
					{"old_name_competitor", "CREATE FUNCTION public.pg_get_userbyid(oid) RETURNS name BEGIN ATOMIC SELECT 'replacement'::name; END", "public,pg_catalog", "pg_catalog", "iv_identity_userbyid", "pg_get_userbyid"},
					{"moved_hidden", "ALTER FUNCTION pg_catalog.iv_identity_userbyid(oid) SET SCHEMA iv_identity", "public,pg_catalog", "iv_identity", "iv_identity_userbyid", "pg_get_userbyid"},
					{"moved_visible", "", "public,iv_identity,pg_catalog", "iv_identity", "iv_identity_userbyid", "pg_get_userbyid"},
					{"moved_shadow", "CREATE FUNCTION public.iv_identity_userbyid(oid) RETURNS name BEGIN ATOMIC SELECT 'shadow'::name; END", "public,iv_identity,pg_catalog", "iv_identity", "iv_identity_userbyid", "iv_identity_userbyid"},
				}
				if scenario == "pure_move" {
					states = []state{
						{"pure_moved_hidden", "ALTER FUNCTION pg_catalog.pg_get_userbyid(oid) SET SCHEMA iv_identity", "public,pg_catalog", "iv_identity", "pg_get_userbyid", ""},
						{"pure_moved_visible", "", "public,iv_identity,pg_catalog", "iv_identity", "pg_get_userbyid", ""},
						{"pure_moved_shadow", "CREATE FUNCTION public.pg_get_userbyid(oid) RETURNS name BEGIN ATOMIC SELECT 'shadow'::name; END", "public,iv_identity,pg_catalog", "iv_identity", "pg_get_userbyid", "pg_get_userbyid"},
					}
				}
				for _, state := range states {
					if state.ddl != "" {
						routineExec(t, p, b, c, state.ddl)
					}
					routineExec(t, p, b, c, "SET search_path="+state.path)
					var currentOID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1::regprocedure AND n.nspname=$2 AND p.proname=$3", state.schema+"."+state.routine+"(oid)", state.schema, state.routine).Scan(&currentOID); err != nil {
						t.Fatal(err)
					}
					if currentOID != originalOID {
						t.Fatalf("%s original OID=%d current OID=%d", state.name, originalOID, currentOID)
					}
					if state.competitor != "" {
						var competitorOID int64
						if err := c.QueryRowContext(p.Ctx, "SELECT p.oid FROM pg_proc p WHERE p.oid=$1::regprocedure", "public."+state.competitor+"(oid)").Scan(&competitorOID); err != nil {
							t.Fatal(err)
						}
						if competitorOID == originalOID {
							t.Fatal("competitor replaced original routine identity")
						}
					}
					for _, quote := range []string{"off", "on"} {
						routineExec(t, p, b, c, "SET quote_all_identifiers="+quote)
						t.Run(state.name+"/quote_"+quote, func(t *testing.T) {
							check(t, infoViewsBuiltinIdentityDefinitions[state.name+"."+quote])
						})
					}
				}
			})
		})
	}
}
