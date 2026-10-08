package reference_test

import "testing"

// Scalar-entry failures retain their result/handle and utility Prepare stages.
func queryJoinsScalarFailure(t *testing.T, p *referencePair, backend referenceBackend, q string, want referenceDiagnostic, prepared, execute bool) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	if prepared {
		stmt, err := backend.db.PrepareContext(p.ctx, q)
		if !execute {
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid scalar input prepared")
			}
			referenceAssertDiagnostic(t, err, &want)
			return
		}
		if err != nil {
			t.Fatal("utility Prepare admission lost", err)
		}
		defer stmt.Close()
		result, err := stmt.ExecContext(p.ctx)
		if result != nil {
			t.Fatal("invalid scalar input exposed result")
		}
		referenceAssertDiagnostic(t, err, &want)
	} else {
		result, err := backend.db.ExecContext(p.ctx, q)
		if result != nil {
			t.Fatal("invalid scalar input exposed result")
		}
		referenceAssertDiagnostic(t, err, &want)
	}
}

func TestReferenceQueryJoinsRetainsScalarInputBoundaries(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	const detail = `There is an entry for table "a", but it cannot be referenced from this part of the query.`
	for _, backend := range p.backends() {
		for _, tc := range []struct {
			q          string
			native, pg referenceDiagnostic
			pgExecute  bool
		}{
			{"CREATE TABLE bad(v INT DEFAULT a.k)", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "."`, Position: "33"}, referenceDiagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "32"}, true},
			{"INSERT INTO a(k) VALUES(a.k)", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "."`, Position: "26"}, referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: detail, Position: "25"}, false},
			{"SELECT current_setting(a.k)", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "."`, Position: "25"}, referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "a"`, Position: "24"}, false},
			{"INSERT INTO a(k) VALUES(COALESCE(a.k,1))", referenceDiagnostic{Code: "0A000", Message: "column references are unsupported in scalar inputs", Position: "34"}, referenceDiagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: detail, Position: "34"}, false},
		} {
			want := tc.native
			if backend.pg {
				want = tc.pg
			}
			queryJoinsScalarFailure(t, p, backend, tc.q, want, false, false)
			queryJoinsScalarFailure(t, p, backend, tc.q, want, true, backend.pg && tc.pgExecute)
		}
		want := referenceDiagnostic{Code: "0A000", Message: "cannot use column reference in DEFAULT expression", Position: "41"}
		q := "CREATE TABLE bad(v INT DEFAULT COALESCE(a.k,1))"
		queryJoinsScalarFailure(t, p, backend, q, want, false, true)
		queryJoinsScalarFailure(t, p, backend, q, want, true, true)
		bad := referenceImplicitError("SELECT * FROM bad", referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
		bad.selectRows = true
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, bad)
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT count(*) FROM a", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
	}
}

func TestReferenceQueryJoinsRetainsNoFromBoundaries(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	const detail = `There is an entry for table "a", but it cannot be referenced from this part of the query.`
	for _, backend := range p.backends() {
		for _, tc := range []struct {
			q, code, message, pos, pgMessage, pgPos string
			invalid                                 bool
		}{
			{"SELECT a.k", "0A000", "qualified column references are unsupported", "9", `missing FROM-clause entry for table "a"`, "8", false},
			{"SELECT a.*", "0A000", "qualified column references are unsupported", "9", `missing FROM-clause entry for table "a"`, "8", false},
			{"INSERT INTO a(k) SELECT a.k", "0A000", "qualified column references are unsupported", "26", `invalid reference to FROM-clause entry for table "a"`, "25", true},
			{"INSERT INTO a(k) SELECT a.*", "0A000", "qualified column references are unsupported", "26", `invalid reference to FROM-clause entry for table "a"`, "25", true},
			{"SELECT (a.k)", "42601", `syntax error at or near "."`, "10", `missing FROM-clause entry for table "a"`, "9", false},
			{"INSERT INTO a(k) SELECT (a.k)", "42601", `syntax error at or near "."`, "27", `invalid reference to FROM-clause entry for table "a"`, "26", true},
			{"SELECT num_nulls(a.k)", "42601", `syntax error at or near "."`, "19", `missing FROM-clause entry for table "a"`, "18", false},
			{"SELECT COALESCE(a.k,1)", "0A000", "SELECT without FROM is unsupported", "1", `missing FROM-clause entry for table "a"`, "17", false},
			{"INSERT INTO a(k) SELECT COALESCE(a.k,1)", "0A000", "qualified column references are unsupported", "34", `invalid reference to FROM-clause entry for table "a"`, "34", true},
		} {
			want := referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.pos}
			if backend.pg {
				want = referenceDiagnostic{Code: "42P01", Message: tc.pgMessage, Position: tc.pgPos}
				if tc.invalid {
					want.Detail = detail
				}
			}
			queryJoinsScalarFailure(t, p, backend, tc.q, want, false, false)
			queryJoinsScalarFailure(t, p, backend, tc.q, want, true, false)
		}
	}
	p.step(t, referenceExec("CREATE TABLE singleton(k INT)", 0))
	p.step(t, referenceExec("CREATE TABLE zero()", 0))
	for _, prepared := range []bool{false, true} {
		for _, step := range []referenceStep{
			referenceSelect("SELECT num_nulls(NULL,1)", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
			referenceExec("INSERT INTO singleton(k) SELECT COALESCE($1,7)", 1, nil),
			referenceExec("INSERT INTO zero SELECT", 1),
		} {
			step.prepared = prepared
			p.step(t, step)
		}
	}
	bag := referenceSelect("SELECT k FROM singleton", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
	bag.bag = true
	p.step(t, bag)
	p.step(t, referenceSelect("SELECT count(*) FROM zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	p.step(t, referenceSelect("SELECT count(*) FROM a", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
}
