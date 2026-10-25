package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePathDomainReplay(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN path_review_one AS path CONSTRAINT singleton CHECK(VALUE=path '[0,0]')",
			"CREATE DOMAIN path_review_required AS path_review_one CONSTRAINT require_value CHECK(VALUE IS NOT NULL)",
			"CREATE TABLE path_review_rows(id integer,p path_review_one)",
			"CREATE VIEW path_review_view AS SELECT CAST(CAST(path '[(-NaN,11)]' AS path_review_one) AS path_review_one) AS p,CAST(NULL::path AS path_review_one) AS n,CAST(CAST(path '[(-NaN,22)]' AS path_review_one) AS path_review_required) AS required,CAST(CAST(path '[(-NaN,33)]' AS path_review_one) AS text) AS txt",
			"CREATE VIEW path_review_reject AS SELECT CAST(CAST(NULL::path AS path_review_one) AS path_review_required) AS p",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO path_review_rows VALUES(1,'[(-NaN,44)]'),(2,NULL)", 2))
		checks := []fixture.Step{
			pathSelect(b, "SELECT p,n,required,txt FROM path_review_view", []string{"p", "n", "required", "txt"}, []string{"PATH", "PATH", "PATH", "TEXT"}, [][]any{{"[(NaN,11)]", nil, "[(NaN,22)]", "[(NaN,33)]"}}),
			pathSelect(b, "SELECT id,p FROM path_review_rows ORDER BY id", []string{"id", "p"}, []string{"INT4", "PATH"}, [][]any{{int64(1), "[(NaN,44)]"}, {int64(2), nil}}),
			fixture.Select("SELECT typtype,typbasetype::bigint FROM pg_type WHERE oid='path_review_one'::regtype", []string{"typtype", "typbasetype"}, []string{"CHAR", "INT8"}, [][]any{{"d", int64(602)}}),
			fixture.Select("SELECT attname,atttypid='path_review_one'::regtype AS first_domain,atttypid='path_review_required'::regtype AS outer_domain,atttypid='text'::regtype AS text_type FROM pg_attribute WHERE attrelid='path_review_view'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "first_domain", "outer_domain", "text_type"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"p", true, false, false}, {"n", true, false, false}, {"required", false, true, false}, {"txt", false, false, true}}),
		}
		check := func(pair *fixture.Pair, e fixture.Executor, rec *fixture.Recorder) {
			t.Helper()
			for _, s := range checks {
				fixture.RunStep(t, pair.Ctx, e, rec, s)
				fixture.RunStep(t, pair.Ctx, e, rec, fixture.Prepared(s))
			}
			q := "SELECT $1::path::path_review_one AS p"
			for _, prepared := range []bool{false, true} {
				d := fixture.ControlsReferenceDiagnostic("23514", "value for domain path_review_one violates check constraint \"singleton\"")
				d.Schema, d.DataType, d.Constraint = "public", "path_review_one", "singleton"
				s := fixture.ErrorDiagnostic(q, d, "[(1,2),(3,4)]")
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, pair.Ctx, e, rec, s)
				s = pathSelect(b, q, []string{"p"}, []string{"PATH"}, [][]any{{"[(NaN,55)]"}}, "[(-NaN,55)]")
				s.Prepared = prepared
				fixture.RunStep(t, pair.Ctx, e, rec, s)
				d = fixture.ControlsReferenceDiagnostic("23514", "value for domain path_review_required violates check constraint \"require_value\"")
				d.Schema, d.DataType, d.Constraint = "public", "path_review_required", "require_value"
				s = fixture.ErrorDiagnostic("SELECT p FROM path_review_reject", d)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, pair.Ctx, e, rec, s)
			}
		}
		check(p, c, b.Recorder)
		if b.PG {
			return
		} // Shared SQL is the oracle; host replacement is native-only.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, exact := range []string{"[(-NaN,11)]", "[(-NaN,22)]", "[(-NaN,44)]"} {
			if !bytes.Contains(dump.Bytes(), []byte(exact)) {
				t.Fatalf("domain dump lost signed datum %s: %s", exact, dump.Bytes())
			}
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(p, c, b.Recorder)
		var same bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &same); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), same.Bytes()) {
			t.Fatalf("domain replay changed serialization\nbefore:\n%s\nafter:\n%s", dump.Bytes(), same.Bytes())
		}
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(target, target.Memory, target.MemoryRecord)
		var fresh bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &fresh); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), fresh.Bytes()) {
			t.Fatalf("fresh domain replay changed serialization\nbefore:\n%s\nafter:\n%s", dump.Bytes(), fresh.Bytes())
		}
	})
}
