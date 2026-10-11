package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Scalar-entry failures retain their result/handle and utility Prepare stages.
func queryJoinsScalarFailure(t *testing.T, p *fixture.Pair, backend fixture.Backend, q string, want fixture.Diagnostic, prepared, execute bool) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	if prepared {
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if !execute {
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid scalar input prepared")
			}
			fixture.AssertDiagnostic(t, err, &want)
			return
		}
		if err != nil {
			t.Fatal("utility Prepare admission lost", err)
		}
		defer stmt.Close()
		result, err := stmt.ExecContext(p.Ctx)
		if result != nil {
			t.Fatal("invalid scalar input exposed result")
		}
		fixture.AssertDiagnostic(t, err, &want)
	} else {
		result, err := backend.DB.ExecContext(p.Ctx, q)
		if result != nil {
			t.Fatal("invalid scalar input exposed result")
		}
		fixture.AssertDiagnostic(t, err, &want)
	}
}

func TestReferenceQueryJoinsRetainsScalarInputBoundaries(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	const detail = `There is an entry for table "a", but it cannot be referenced from this part of the query.`
	for _, backend := range p.Backends() {
		for _, tc := range []struct {
			q          string
			native, pg fixture.Diagnostic
			pgExecute  bool
		}{
			{"CREATE TABLE bad(v INT DEFAULT a.k)", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "."`, Position: "33"}, fixture.Diagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "32"}, true},
			{"INSERT INTO a(k) VALUES(a.k)", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "."`, Position: "26"}, fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: detail, Position: "25"}, false},
			{"SELECT current_setting(a.k)", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "."`, Position: "25"}, fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "a"`, Position: "24"}, false},
			{"INSERT INTO a(k) VALUES(COALESCE(a.k,1))", fixture.Diagnostic{Code: "0A000", Message: "column references are unsupported in scalar inputs", Position: "34"}, fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: detail, Position: "34"}, false},
		} {
			want := tc.native
			if backend.PG {
				want = tc.pg
			}
			queryJoinsScalarFailure(t, p, backend, tc.q, want, false, false)
			queryJoinsScalarFailure(t, p, backend, tc.q, want, true, backend.PG && tc.pgExecute)
		}
		want := fixture.Diagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "41"}
		q := "CREATE TABLE bad(v INT DEFAULT COALESCE(a.k,1))"
		queryJoinsScalarFailure(t, p, backend, q, want, false, true)
		queryJoinsScalarFailure(t, p, backend, q, want, true, true)
		bad := fixture.ImplicitError("SELECT * FROM bad", fixture.Diagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
		bad.SelectRows = true
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, bad)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT count(*) FROM a", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
	}
}

func TestReferenceQueryJoinsRetainsNoFromBoundaries(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	const detail = `There is an entry for table "a", but it cannot be referenced from this part of the query.`
	for _, backend := range p.Backends() {
		for _, tc := range []struct {
			q, message, pos string
			invalid         bool
		}{
			{"SELECT a.k", `missing FROM-clause entry for table "a"`, "8", false},
			{"SELECT a.*", `missing FROM-clause entry for table "a"`, "8", false},
			{"INSERT INTO a(k) SELECT a.k", `invalid reference to FROM-clause entry for table "a"`, "25", true},
			{"INSERT INTO a(k) SELECT a.*", `invalid reference to FROM-clause entry for table "a"`, "25", true},
			{"SELECT (a.k)", `missing FROM-clause entry for table "a"`, "9", false},
			{"INSERT INTO a(k) SELECT (a.k)", `invalid reference to FROM-clause entry for table "a"`, "26", true},
			{"SELECT num_nulls(a.k)", `missing FROM-clause entry for table "a"`, "18", false},
			{"SELECT COALESCE(a.k,1)", `missing FROM-clause entry for table "a"`, "17", false},
			{"INSERT INTO a(k) SELECT COALESCE(a.k,1)", `invalid reference to FROM-clause entry for table "a"`, "34", true},
		} {
			want := fixture.Diagnostic{Code: "42P01", Message: tc.message, Position: tc.pos}
			if tc.invalid {
				want.Detail = detail
			}
			queryJoinsScalarFailure(t, p, backend, tc.q, want, false, false)
			queryJoinsScalarFailure(t, p, backend, tc.q, want, true, false)
		}
	}
	p.Step(t, fixture.Exec("CREATE TABLE singleton(k INT)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE zero()", 0))
	for _, prepared := range []bool{false, true} {
		for _, step := range []fixture.Step{
			fixture.Select("SELECT num_nulls(NULL,1)", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Exec("INSERT INTO singleton(k) SELECT COALESCE($1,7)", 1, nil),
			fixture.Exec("INSERT INTO zero SELECT", 1),
		} {
			step.Prepared = prepared
			p.Step(t, step)
		}
	}
	bag := fixture.Select("SELECT k FROM singleton", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
	bag.Bag = true
	p.Step(t, bag)
	p.Step(t, fixture.Select("SELECT count(*) FROM zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	p.Step(t, fixture.Select("SELECT count(*) FROM a", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
}
