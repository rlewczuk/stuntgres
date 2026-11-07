package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNullCountCapabilityBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(num_nulls INT,num_nonnulls INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2)", 1))
	p.Step(t, fixture.Select("SELECT num_nulls,num_nonnulls FROM t", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
	p.Step(t, fixture.Select("SELECT abs(1) FROM t", []string{"abs"}, []string{"INT4"}, [][]any{{int64(1)}}))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT num_nulls(NULL),1", []string{"num_nulls", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
		fixture.Select("SELECT $1", []string{"?column?"}, []string{"TEXT"}, [][]any{{"unit"}}, "unit"),
		fixture.Select("SELECT num_nulls(NULL) WHERE TRUE", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT num_nulls(NULL) ORDER BY 1", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT num_nulls(ARRAY[1,NULL])", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT num_nulls(NULL)::int FROM t", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
	} {
		for _, prepared := range []bool{false, true} {
			step.Prepared = prepared
			p.Step(t, step)
		}
	}
	nullCountReferenceError(t, p, "SELECT *", fixture.Diagnostic{Code: "42601", Message: "SELECT * with no tables specified is not valid", Position: "8"})
	// These ordinary-call forms parse successfully. Both execution routes must
	// reject them semantically, without publishing a cursor or prepared handle.
	for _, tc := range []struct {
		query string
		want  fixture.Diagnostic
	}{
		{"SELECT num_nulls(*) FROM t", fixture.Diagnostic{Code: "42883", Message: "function num_nulls() does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
		{"SELECT num_nulls(1) FILTER (WHERE TRUE) FROM t", fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but num_nulls is not an aggregate function", Position: "8"}},
		{"SELECT num_nulls FROM t WHERE num_nulls(NULL)", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "31"}},
		{"SELECT num_nulls(a => 1) FROM t", fixture.Diagnostic{Code: "42883", Message: "function num_nulls(a => integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
		{"SELECT current_setting(num_nulls(NULL))", fixture.Diagnostic{Code: "42883", Message: "function current_setting(integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
	} {
		nullCountReferenceError(t, p, tc.query, tc.want)
	}
	// Replace obsolete no-FROM rejections with a genuine whole-input failure,
	// retaining nil cursor/statement/result and no-prefix-effects obligations.
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			failure := fixture.Error("INSERT INTO t VALUES(9,9);SELECT 1 WHERE", "42601")
			failure.Prepared, failure.SelectRows = prepared, queryRows
			p.Step(t, failure)
			p.Step(t, fixture.Select("SELECT num_nulls,num_nonnulls FROM t", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		}
	}
	for _, backend := range p.Backends() {
		rows, err := backend.DB.QueryContext(p.Ctx, "SELECT $1")
		if rows != nil {
			rows.Close()
			t.Fatal("missing argument exposed cursor")
		}
		if err == nil {
			t.Fatal("missing argument accepted")
		}
		if backend.PG {
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "there is no parameter $1", Position: "8"})
		}
		stmt, err := backend.DB.PrepareContext(p.Ctx, "SELECT $1")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			rows, err := stmt.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("missing prepared argument exposed cursor")
			}
			fixture.TransactionOrdinary(t, err, "sql: expected 1 arguments, got 0")
		}()
	}
	// DISTINCT and in-call ORDER BY retain their separate native syntax
	// boundary. PostgreSQL admits the syntax, then rejects the nonaggregate.
	for _, backend := range p.Backends() {
		for _, tc := range []struct {
			query, modifier, position string
		}{
			{"SELECT num_nulls(DISTINCT 1) FROM t", "DISTINCT", "18"},
			{"SELECT num_nulls(1 ORDER BY 1) FROM t", "ORDER BY", "20"},
		} {
			want := fixture.Diagnostic{Code: "42809", Message: tc.modifier + " specified, but num_nulls is not an aggregate function", Position: "8"}
			if !backend.PG {
				token := tc.modifier
				if token == "ORDER BY" {
					token = "ORDER"
				}
				want = fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"" + token + "\"", Position: tc.position}
			}
			for _, prepared := range []bool{false, true} {
				step := fixture.ImplicitError(tc.query, want)
				step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
				if prepared {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		insert := fixture.Exec("INSERT INTO t VALUES(num_nulls(NULL),1)", 1)
		insert.Prepared = prepared
		p.Step(t, insert)
		bag := fixture.Select("SELECT num_nulls,num_nonnulls FROM t", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(1)}})
		bag.Bag = true
		p.Step(t, bag)
		p.Step(t, fixture.Exec("DELETE FROM t WHERE num_nonnulls=1", 1))
	}
}

// REL_18_0 misc.c count_nulls counts PG_ARGISNULL for ordinary arguments;
// only explicit VARIADIC counts array members. Expectations are source-derived
// independently of native results.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/misc.c
func TestReferenceNullCountArrayComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Select("SELECT num_nulls(ARRAY[1,NULL]) AS n,num_nonnulls(ARRAY[1,NULL]) AS p", []string{"n", "p"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}}),
		fixture.Select("SELECT num_nulls(NULL::int[]) AS n,num_nonnulls(NULL::int[]) AS p,num_nulls(ARRAY[]::int[]) AS en,num_nonnulls(ARRAY[]::int[]) AS ep,num_nulls(ARRAY[NULL::int]) AS cn,num_nonnulls(ARRAY[NULL::int]) AS cp", []string{"n", "p", "en", "ep", "cn", "cp"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(0), int64(1), int64(0), int64(1)}}),
	} {
		for _, prepared := range []bool{false, true} {
			step.Prepared = prepared
			p.Step(t, step)
		}
	}
	for _, tc := range []struct {
		value           any
		nulls, nonnulls int64
	}{
		{nil, 1, 0}, {"{}", 0, 1}, {"{NULL}", 0, 1}, {"{1,NULL}", 0, 1},
	} {
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT num_nulls($1::int[]) AS n,num_nonnulls($1::int[]) AS p", []string{"n", "p"}, []string{"INT4", "INT4"}, [][]any{{tc.nulls, tc.nonnulls}}, tc.value)
			step.Prepared = prepared
			p.Step(t, step)
		}
	}
	for _, backend := range p.Backends() {
		for _, fn := range []struct{ name, position string }{{"num_nulls", "27"}, {"num_nonnulls", "30"}} {
			query := "SELECT " + fn.name + "(VARIADIC ARRAY[1,NULL])"
			for _, prepared := range []bool{false, true} {
				step := fixture.Select(query, []string{fn.name}, []string{"INT4"}, [][]any{{int64(1)}})
				if !backend.PG {
					diagnostic := fixture.ControlsReferenceDiagnostic("0A000", "VARIADIC arrays in null-count arguments are unsupported")
					diagnostic.Position = fn.position
					step = fixture.ErrorDiagnostic(query, diagnostic)
					step.SelectRows = true
					step.ErrorStage = "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
				}
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			}
		}
	}
}
