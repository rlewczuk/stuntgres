package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
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
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE json_review_alter(id INTEGER,v TEXT)", 0))
					run(referenceExec("INSERT INTO json_review_alter VALUES(1,$1),(2,'null'),(3,NULL)", 3, ` {"z":1,"a":2,"a":3} `))
					run(referenceExec("CREATE INDEX json_review_alter_idx ON json_review_alter("+key.sql+")", 0))
					assertState := func() {
						t.Helper()
						s := referenceSelect("SELECT id,v FROM json_review_alter ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), ` {"z":1,"a":2,"a":3} `}, {int64(2), "null"}, {int64(3), nil}})
						run(s)
						run(referencePrepared(s))
						d := deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('json_review_alter_idx'::regclass) AS definition", "CREATE INDEX json_review_alter_idx ON public.json_review_alter USING btree ("+key.definition+")")
						run(d)
						run(referencePrepared(d))
					}
					assertState()
					var before bytes.Buffer
					if !b.pg {
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
					}
					s := referenceImplicitError("ALTER TABLE json_review_alter ALTER COLUMN v TYPE json USING v::json", referenceDiagnostic{
						Code: "42704", Message: `data type json has no default operator class for access method "btree"`,
						Hint: "You must specify an operator class for the index or define a default operator class for the data type.",
					})
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					assertState()
					if !b.pg {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
							t.Fatal("failed JSON TYPE changed catalog, rows or index", err)
						}
					}
					// The retained TEXT index remains usable after failed publication.
					run(referencePrepared(referenceExec("INSERT INTO json_review_alter VALUES(4,'after')", 1)))
					run(referenceSelect("SELECT id FROM json_review_alter WHERE v='after'", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
				}
			})
		}
	}
}

func TestReferenceJSONReviewExpressionClass(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE json_review_expression(v TEXT)", 0))
		run(referenceExec("INSERT INTO json_review_expression VALUES('null')", 1))
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				q := "CREATE INDEX json_review_expression_idx ON json_review_expression((v::json)"
				want := referenceDiagnostic{Code: "42704", Message: `data type json has no default operator class for access method "btree"`, Hint: "You must specify an operator class for the index or define a default operator class for the data type."}
				if explicit {
					q += " text_ops"
					want = referenceDiagnostic{Code: "42804", Message: `operator class "text_ops" does not accept data type json`}
				}
				s := referenceImplicitError(q+")", want)
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='json_review_expression_idx'", []string{"relname"}, []string{"NAME"}, nil))
			}
		}
	}
}

func TestReferenceJSONReviewIncludeMaintenanceReplay(t *testing.T) {
	p := newReferenceScenario(t)
	seed := func(run func(referenceStep)) {
		run(referenceExec("CREATE TABLE json_review_include(id INTEGER,v JSON)", 0))
		run(referenceExec("CREATE INDEX json_review_include_idx ON json_review_include(id) INCLUDE(v)", 0))
		run(referenceExec("INSERT INTO json_review_include VALUES(1,$1),(2,'null'),(3,NULL)", 3, ` {"k":1,"k":2} `))
		run(referencePrepared(referenceExec("UPDATE json_review_include SET v=$1 WHERE id=1", 1, ` {"z":2,"a":1,"a":0} `)))
		run(referenceExec("UPDATE json_review_include SET id=4 WHERE id=2", 1))
		run(referencePrepared(referenceExec("DELETE FROM json_review_include WHERE id=3", 1)))
		run(referencePrepared(referenceExec("INSERT INTO json_review_include VALUES(5,NULL)", 1)))
	}
	assertRows := func(b referenceBackend) {
		t.Helper()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		s := referenceSelect("SELECT id,v FROM json_review_include ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), ` {"z":2,"a":1,"a":0} `}, {int64(4), "null"}, {int64(5), nil}})
		run(s)
		run(referencePrepared(s))
		run(referenceSelect("SELECT v FROM json_review_include WHERE id=4", []string{"v"}, []string{"JSON"}, [][]any{{"null"}}))
		run(deparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('json_review_include_idx'::regclass) AS definition", "CREATE INDEX json_review_include_idx ON public.json_review_include USING btree (id) INCLUDE (v)"))
	}
	for _, b := range p.backends() {
		seed(func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) })
		assertRows(b)
	}
	jsonReviewIncludeReplay(t, p, assertRows)
}

// Controls have no PG API counterpart: replay the actual native dump, never a
// separately authored SQL facsimile.
func jsonReviewIncludeReplay(t *testing.T, p *referencePair, assertRows func(referenceBackend)) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `INCLUDE ("v")`) {
		t.Fatalf("JSON INCLUDE missing from actual dump: %s", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	assertRows(referenceBackend{"memory", p.memory, p.memoryRecord, false})
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || dump.String() != redump.String() {
		t.Fatal("JSON INCLUDE redump changed", err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE json_review_include"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		assertRows(referenceBackend{"postgres", p.postgres, p.postgresRecord, true})
	}
	for _, b := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referencePrepared(referenceExec("UPDATE json_review_include SET v=$1 WHERE id=4", 1, `"after replay"`)))
		run(referenceExec("DELETE FROM json_review_include WHERE id=1", 1))
		run(referenceSelect("SELECT id,v FROM json_review_include ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(4), `"after replay"`}, {int64(5), nil}}))
	}
}
