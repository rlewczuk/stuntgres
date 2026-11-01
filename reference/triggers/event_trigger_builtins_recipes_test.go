package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow REL_18_6 objectaddress.c and event_trigger.c; missing-object
// diagnostics are confirmed by the pinned PostgreSQL 18.6 oracle.
type builtinRecipe struct {
	name, tags, drop, message string
	setup, recreate           []string
	records                   []droppedRecord
	target                    int
}

func builtinRecipes() []builtinRecipe {
	return []builtinRecipe{
		{
			name: "cast", tags: "'DROP CAST'", drop: "DROP CAST (public.et_do_cast_source AS integer)",
			message: "cast from type et_do_cast_source to type integer does not exist",
			setup: []string{
				"CREATE TYPE public.et_do_cast_source AS ENUM ('red')",
				"CREATE FUNCTION public.et_do_cast_convert(public.et_do_cast_source) RETURNS integer LANGUAGE SQL AS $$SELECT 51$$",
			},
			recreate: []string{"CREATE CAST (public.et_do_cast_source AS integer) WITH FUNCTION public.et_do_cast_convert(public.et_do_cast_source)"},
			records: []droppedRecord{{
				class: 2605, original: true, kind: "cast", identity: "(public.et_do_cast_source AS integer)",
				names: []string{"public.et_do_cast_source"}, args: []string{"integer"},
				lookup: "pg_cast WHERE castsource='public.et_do_cast_source'::regtype AND casttarget='integer'::regtype",
			}},
		},
		{
			name: "configuration", tags: "'DROP TEXT SEARCH DICTIONARY'", drop: "DROP TEXT SEARCH DICTIONARY public.et_do_ts_dict CASCADE",
			message: `text search configuration "public.et_do_ts_cfg" does not exist`, target: 1,
			recreate: []string{
				"CREATE TEXT SEARCH DICTIONARY public.et_do_ts_dict(TEMPLATE=pg_catalog.simple)",
				"CREATE TEXT SEARCH CONFIGURATION public.et_do_ts_cfg(PARSER=pg_catalog.default)",
				"ALTER TEXT SEARCH CONFIGURATION public.et_do_ts_cfg ADD MAPPING FOR asciiword WITH public.et_do_ts_dict",
			},
			records: []droppedRecord{
				{class: 3600, original: true, kind: "text search dictionary", schema: "public", name: "et_do_ts_dict", identity: "public.et_do_ts_dict", names: []string{"public", "et_do_ts_dict"}, lookup: "pg_ts_dict WHERE dictname='et_do_ts_dict' AND dictnamespace='public'::regnamespace"},
				{class: 3602, normal: true, kind: "text search configuration", schema: "public", name: "et_do_ts_cfg", identity: "public.et_do_ts_cfg", names: []string{"public", "et_do_ts_cfg"}, lookup: "pg_ts_config WHERE cfgname='et_do_ts_cfg' AND cfgnamespace='public'::regnamespace"},
			},
		},
	}
}

func builtinRecipePredicate(r droppedRecord) string {
	// Configuration is a normal dependent, not an original root. Do not add
	// an original predicate: that would silently discard the regression.
	return "a.pass=1 AND a.object_type=" + droppedLiteral(r.kind) + " AND a.object_identity=" + droppedLiteral(r.identity)
}

func builtinRecipeSQL(source string, r droppedRecord) string {
	return "SELECT r.classid,r.objid,r.objsubid FROM " + source + " a CROSS JOIN LATERAL pg_get_object_address(a.object_type,a.address_names,a.address_args) r WHERE " + builtinRecipePredicate(r)
}

func builtinRecipeCells(d droppedCase, source string, r droppedRecord) []any {
	d.t.Helper()
	var kind, names, args string
	q := "SELECT a.object_type,a.address_names::text,a.address_args::text FROM " + source + " a WHERE " + builtinRecipePredicate(r)
	if err := d.c.QueryRowContext(d.p.Ctx, q).Scan(&kind, &names, &args); err != nil {
		d.t.Fatal("actual saved cells", err)
	}
	return []any{kind, names, args}
}

func builtinAddressRows(d droppedCase, executor fixture.Executor, s fixture.Step) {
	d.t.Helper()
	// OidReferenceRows checks raw carriers and OID descriptors; the scalar
	// wrapper adds computed nullability, INT4 ScanType and length assertions.
	metadata := &fixture.ScalarMetadataExecutor{Executor: executor, T: d.t, PG: d.b.PG, Computed: []int{0, 1, 2}, UnknownLength: []int{0, 1, 2}}
	fixture.OidReferenceRows(d.t, d.p.Ctx, d.b, metadata, s)
}

func builtinAddressStep(d droppedCase, q string, r droppedRecord, args ...any) fixture.Step {
	return fixture.Select(q, []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"}, [][]any{{r.class, currentDroppedOID(d, r), int64(0)}}, args...)
}

// The only extra inverse consumers are the explicitly scoped cast/configuration
// recipes. Existing table/routine reconstruction predicates remain untouched.
func builtinRecipeCheck(d droppedCase, source string, roots []droppedRecord) {
	d.t.Helper()
	for _, r := range roots {
		q := builtinRecipeSQL(source, r)
		for _, empty := range []bool{false, true} {
			query := q
			if empty {
				query += " LIMIT 0"
			}
			s := builtinAddressStep(d, query, r)
			if empty {
				s.Rows = nil
			}
			builtinAddressRows(d, d.c, s)
			stmt, err := d.c.PrepareContext(d.p.Ctx, query)
			if err != nil {
				d.t.Fatal(err)
			}
			builtinAddressRows(d, fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: query}, s)
			if err := stmt.Close(); err != nil {
				d.t.Fatal(err)
			}
		}
		cells := builtinRecipeCells(d, source, r)
		stmt, err := d.c.PrepareContext(d.p.Ctx, droppedHeldAddressSQL)
		if err != nil {
			d.t.Fatal(err)
		}
		builtinAddressRows(d, fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: droppedHeldAddressSQL}, builtinAddressStep(d, droppedHeldAddressSQL, r, cells...))
		if err := stmt.Close(); err != nil {
			d.t.Fatal(err)
		}
	}
}

func builtinRecipeData(d droppedCase, name string) {
	d.t.Helper()
	if name == "cast" {
		d.run(fixture.Select("SELECT public.et_do_cast_convert('red'::public.et_do_cast_source) AS converter,('red'::public.et_do_cast_source)::integer AS converted", []string{"converter", "converted"}, []string{"INT4", "INT4"}, [][]any{{int64(51), int64(51)}}))
		return
	}
	var dict int64
	if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid::bigint FROM pg_ts_dict WHERE dictname='et_do_ts_dict' AND dictnamespace='public'::regnamespace").Scan(&dict); err != nil || dict <= 0 {
		d.t.Fatal("current dictionary", dict, err)
	}
	d.run(fixture.Select("SELECT cfgparser::bigint FROM pg_ts_config WHERE cfgname='et_do_ts_cfg' AND cfgnamespace='public'::regnamespace", []string{"cfgparser"}, []string{"INT8"}, [][]any{{int64(3722)}}))
	d.run(fixture.Select("SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='public.et_do_ts_cfg'::regconfig", []string{"maptokentype", "mapseqno", "mapdict"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), dict}}))
	d.run(fixture.Select("SELECT ts_lexize('public.et_do_ts_dict','WORD') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{word}"}}))
}

func builtinSavedBag(d droppedCase, source string, records []droppedRecord) {
	d.t.Helper()
	rows := make([][]any, len(records))
	for i, r := range records {
		rows[i] = r.row(d.b.PG)
	}
	s := fixture.Select("SELECT "+strings.Join(droppedColumns, ",")+" FROM "+source, droppedColumns, droppedTypes, rows)
	s.Bag = true
	d.run(s)
	d.run(fixture.Prepared(s))
}

func TestReferenceEventTriggerBuiltinsRecipes(t *testing.T) {
	for _, recipe := range builtinRecipes() {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", recipe.name, prepared), func(t *testing.T) {
				droppedScenario(t, func(d droppedCase) {
					t := d.t
					var notices []fixture.Diagnostic
					_, d.c, d.b.Recorder = fixture.NoticeReferencePool(t, d.p, d.b.PG, &notices)
					d.setup()
					d.exec(recipe.setup...)
					d.exec(recipe.recreate...)
					d.absenceCallback()
					d.install(recipe.tags)
					stmt, err := d.c.PrepareContext(d.p.Ctx, droppedHeldAddressSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: droppedHeldAddressSQL}
					expected := d.capture(recipe.records)
					root := expected[recipe.target]
					quiet := func() {
						d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_audit", 0))
						d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_do_phase", 0))
						builtinRecipeData(d, recipe.name)
						fixture.RegtypeAssertNotices(t, notices, nil)
					}
					quiet()
					if prepared {
						utility, err := d.c.PrepareContext(d.p.Ctx, recipe.drop)
						if err != nil {
							t.Fatal(err)
						}
						quiet()
						fixture.RunStep(t, d.p.Ctx, fixture.HeldStatement{Executor: d.c, Statement: utility, SQL: recipe.drop}, d.b.Recorder, fixture.Exec(recipe.drop, 0))
						if err := utility.Close(); err != nil {
							t.Fatal(err)
						}
					} else {
						d.drop(recipe.drop, false)
					}
					wantNotices := []fixture.Diagnostic(nil)
					if recipe.name == "configuration" {
						wantNotices = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to text search configuration et_do_ts_cfg"}}
					}
					fixture.RegtypeAssertNotices(t, notices, wantNotices)
					d.bag(1, expected)
					d.bag(2, expected)
					d.phases(len(expected))
					cells := builtinRecipeCells(d, "public.et_do_audit", root)
					missing := func() {
						q := builtinRecipeSQL("public.et_do_audit", root)
						s := fixture.ErrorDiagnostic(q, eventTriggerDiagnostic("42704", recipe.message))
						s.SelectRows, s.ErrorStage = true, "execute"
						d.run(s)
						d.run(fixture.Prepared(s))
						hm := fixture.ErrorDiagnostic(droppedHeldAddressSQL, eventTriggerDiagnostic("42704", recipe.message), cells...)
						hm.SelectRows, hm.ErrorStage = true, "execute"
						fixture.RunStep(t, d.p.Ctx, held, d.b.Recorder, hm)
					}
					missing()
					if recipe.name == "cast" {
						d.run(fixture.Select("SELECT public.et_do_cast_convert('red'::public.et_do_cast_source) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(51)}}))
					}
					d.exec(recipe.recreate...)
					check := func() {
						builtinRecipeCheck(d, "public.et_do_audit", []droppedRecord{root})
						builtinAddressRows(d, held, builtinAddressStep(d, droppedHeldAddressSQL, root, cells...))
						builtinRecipeData(d, recipe.name)
						d.bag(1, expected)
						d.bag(2, expected)
					}
					check()
					d.exec("ALTER EVENT TRIGGER et_do_drop DISABLE", "ALTER EVENT TRIGGER et_do_end DISABLE", recipe.drop)
					missing()
					d.exec(recipe.recreate...)
					check()
				})
			})
		}
	}
}
