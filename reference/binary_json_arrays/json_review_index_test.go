package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_0 src/backend/commands/indexcmds.c ResolveOpClass owns missing-default
// diagnostics for the final key type, including reconstructed expression keys.
func TestReferenceJSONReviewIndexedAlterType(t *testing.T) {
	for _, key := range []struct{ name, sql, definition string }{
		{"column", "v", "v"},
		{"expression", "(coalesce(v,v))", "COALESCE(v, v)"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(key.name+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE json_review_alter(id INTEGER,v TEXT)", 0))
					run(fixture.Exec("INSERT INTO json_review_alter VALUES(1,$1),(2,'null'),(3,NULL)", 3, ` {"z":1,"a":2,"a":3} `))
					run(fixture.Exec("CREATE INDEX json_review_alter_idx ON json_review_alter("+key.sql+")", 0))
					assertState := func() {
						t.Helper()
						s := fixture.Select("SELECT id,v FROM json_review_alter ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), ` {"z":1,"a":2,"a":3} `}, {int64(2), "null"}, {int64(3), nil}})
						run(s)
						run(fixture.Prepared(s))
						d := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('json_review_alter_idx'::regclass) AS definition", "CREATE INDEX json_review_alter_idx ON public.json_review_alter USING btree ("+key.definition+")")
						run(d)
						run(fixture.Prepared(d))
					}
					assertState()
					var before bytes.Buffer
					if !b.PG {
						if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
					}
					s := fixture.ImplicitError("ALTER TABLE json_review_alter ALTER COLUMN v TYPE json USING v::json", fixture.Diagnostic{
						Code: "42704", Message: `data type json has no default operator class for access method "btree"`,
						Hint: "You must specify an operator class for the index or define a default operator class for the data type.",
					})
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					assertState()
					if !b.PG {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
							t.Fatal("failed JSON TYPE changed catalog, rows or index", err)
						}
					}
					// The retained TEXT index remains usable after failed publication.
					run(fixture.Prepared(fixture.Exec("INSERT INTO json_review_alter VALUES(4,'after')", 1)))
					run(fixture.Select("SELECT id FROM json_review_alter WHERE v='after'", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
				}
			})
		}
	}
}

func TestReferenceJSONReviewExpressionClass(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE json_review_expression(v TEXT)", 0))
		run(fixture.Exec("INSERT INTO json_review_expression VALUES('null')", 1))
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				q := "CREATE INDEX json_review_expression_idx ON json_review_expression((v::json)"
				want := fixture.Diagnostic{Code: "42704", Message: `data type json has no default operator class for access method "btree"`, Hint: "You must specify an operator class for the index or define a default operator class for the data type."}
				if explicit {
					q += " text_ops"
					want = fixture.Diagnostic{Code: "42804", Message: `operator class "text_ops" does not accept data type json`}
				}
				s := fixture.ImplicitError(q+")", want)
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='json_review_expression_idx'", []string{"relname"}, []string{"NAME"}, nil))
			}
		}
	}
}

func TestReferenceJSONReviewIncludeMaintenanceReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	seed := func(run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE json_review_include(id INTEGER,v JSON)", 0))
		run(fixture.Exec("CREATE INDEX json_review_include_idx ON json_review_include(id) INCLUDE(v)", 0))
		run(fixture.Exec("INSERT INTO json_review_include VALUES(1,$1),(2,'null'),(3,NULL)", 3, ` {"k":1,"k":2} `))
		run(fixture.Prepared(fixture.Exec("UPDATE json_review_include SET v=$1 WHERE id=1", 1, ` {"z":2,"a":1,"a":0} `)))
		run(fixture.Exec("UPDATE json_review_include SET id=4 WHERE id=2", 1))
		run(fixture.Prepared(fixture.Exec("DELETE FROM json_review_include WHERE id=3", 1)))
		run(fixture.Prepared(fixture.Exec("INSERT INTO json_review_include VALUES(5,NULL)", 1)))
	}
	assertRows := func(b fixture.Backend) {
		t.Helper()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		s := fixture.Select("SELECT id,v FROM json_review_include ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), ` {"z":2,"a":1,"a":0} `}, {int64(4), "null"}, {int64(5), nil}})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Select("SELECT v FROM json_review_include WHERE id=4", []string{"v"}, []string{"JSON"}, [][]any{{"null"}}))
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('json_review_include_idx'::regclass) AS definition", "CREATE INDEX json_review_include_idx ON public.json_review_include USING btree (id) INCLUDE (v)"))
	}
	for _, b := range p.Backends() {
		seed(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) })
		assertRows(b)
	}
	jsonReviewIncludeReplay(t, p, assertRows)
}

// Controls have no PG API counterpart: replay the actual native dump, never a
// separately authored SQL facsimile.
func jsonReviewIncludeReplay(t *testing.T, p *fixture.Pair, assertRows func(fixture.Backend)) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `INCLUDE ("v")`) {
		t.Fatalf("JSON INCLUDE missing from actual dump: %s", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	assertRows(fixture.Backend{"memory", p.Memory, p.MemoryRecord, false})
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || dump.String() != redump.String() {
		t.Fatal("JSON INCLUDE redump changed", err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE json_review_include"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		assertRows(fixture.Backend{"postgres", p.Postgres, p.PostgresRecord, true})
	}
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Prepared(fixture.Exec("UPDATE json_review_include SET v=$1 WHERE id=4", 1, `"after replay"`)))
		run(fixture.Exec("DELETE FROM json_review_include WHERE id=1", 1))
		run(fixture.Select("SELECT id,v FROM json_review_include ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(4), `"after replay"`}, {int64(5), nil}}))
	}
}
