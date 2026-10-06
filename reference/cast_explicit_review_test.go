//go:build reference

package reference_test

import "testing"

func TestReferenceCastExplicitEqualityDiagnosticOwnership(t *testing.T) {
	p := castReferenceSetup(t)
	for _, tc := range []struct {
		q string
		d referenceDiagnostic
	}{
		{"SELECT id FROM t WHERE i=int8(TRUE)", referenceDiagnostic{Code: "42883", Message: "function int8(boolean) does not exist", Position: "26", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=CAST(int8(TRUE) AS text)", referenceDiagnostic{Code: "42883", Message: "function int8(boolean) does not exist", Position: "31", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=int8(TRUE)::text", referenceDiagnostic{Code: "42883", Message: "function int8(boolean) does not exist", Position: "26", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=CAST(TRUE AS bigint)", referenceDiagnostic{Code: "42846", Message: "cannot cast type boolean to bigint", Position: "26"}},
		{"SELECT id FROM t WHERE i=TRUE::int8", referenceDiagnostic{Code: "42846", Message: "cannot cast type boolean to bigint", Position: "30"}},
		{"SELECT id FROM t WHERE i=TRUE::bool", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=CAST(TRUE AS bool)", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=bool(TRUE)", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		{"SELECT id FROM t WHERE i=CAST(bool(TRUE) AS bool)", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "25", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			step := castReferenceError(tc.q, tc.d, false)
			t.Run("direct", func(t *testing.T) { castReferenceBoth(t, p, step) })
			step.errorStage = "prepare"
			t.Run("prepared", func(t *testing.T) { castReferenceBoth(t, p, referencePrepared(step)) })
		})
	}
}

func TestReferenceCastExplicitEmptyImmutableExecution(t *testing.T) {
	p := castReferenceSetup(t)
	p.step(t, referenceExec("CREATE TABLE e(i INT4,s TEXT)", 0))
	for _, tc := range []struct {
		q string
		d referenceDiagnostic
	}{
		{"SELECT 2147483648::int4 FROM e", referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
		{"SELECT CAST(2147483648 AS int4) FROM e", referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
		{"SELECT int4(2147483648) FROM e", referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
		{"SELECT CAST('oops'::text AS int4) FROM e", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"SELECT text(int4('oops'::text)) FROM e", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"SELECT i FROM e WHERE i=2147483648::int4", referenceDiagnostic{Code: "22003", Message: "integer out of range"}},
		{"SELECT i FROM e WHERE i=CAST('oops'::text AS int4)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"SELECT i FROM e WHERE bool(int4('oops'::text))", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			step := castReferenceError(tc.q, tc.d, false)
			t.Run("direct", func(t *testing.T) { castReferenceBoth(t, p, step) })
			// Both backends must successfully Prepare, then fail Query before
			// publishing a cursor, even though the relation has no input rows.
			t.Run("prepared", func(t *testing.T) { castReferenceBoth(t, p, referencePrepared(step)) })
		})
	}
	for _, tc := range []struct {
		q, name, typ string
	}{
		{"SELECT s::int4 FROM e", "s", "INT4"},
		{"SELECT int4(s)::text FROM e", "int4", "TEXT"},
		{"SELECT i FROM e WHERE s::bool", "i", "INT4"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			step := referenceSelect(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{})
			t.Run("direct", func(t *testing.T) { castReferenceBoth(t, p, step) })
			t.Run("prepared", func(t *testing.T) { castReferenceBoth(t, p, referencePrepared(step)) })
		})
	}
	for _, backend := range []struct {
		name string
		db   referenceExecutor
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run("held-row-dependent/"+backend.name, func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx, "SELECT s::int4 FROM e")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("empty held execution", rows.Err())
			}
			rows.Close()
			result, err := backend.db.ExecContext(p.ctx, "INSERT INTO e VALUES(1,'oops')")
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			rows, err = stmt.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("populated invalid row exposed cursor")
			}
			step := castReferenceError("SELECT s::int4 FROM e", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, false)
			referenceCheckDiagnostic(t, err, step, "execute")
		})
	}
	t.Run("rejected-row-dependent", func(t *testing.T) {
		step := referenceSelect("SELECT s::int4 FROM e WHERE i=0", []string{"s"}, []string{"INT4"}, [][]any{})
		t.Run("direct", func(t *testing.T) { castReferenceBoth(t, p, step) })
		t.Run("prepared", func(t *testing.T) { castReferenceBoth(t, p, referencePrepared(step)) })
	})
	t.Run("rejected-immutable", func(t *testing.T) {
		step := castReferenceError("SELECT 2147483648::int4 FROM e WHERE i=0", referenceDiagnostic{Code: "22003", Message: "integer out of range"}, false)
		t.Run("direct", func(t *testing.T) { castReferenceBoth(t, p, step) })
		t.Run("prepared", func(t *testing.T) { castReferenceBoth(t, p, referencePrepared(step)) })
	})
}
