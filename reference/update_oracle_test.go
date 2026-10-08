//go:build reference

package reference_test

import "testing"

// This oracle runs PostgreSQL alone so analysis/Prepare failures are captured
// before the embedded implementation is changed. All portable fields are exact.
func TestReferenceUpdateCoreOracle(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{"CREATE TABLE t(a INT,b INT,n SMALLINT NOT NULL)", "INSERT INTO t VALUES(1,2,1),(40000,3,1)"} {
		if _, err := p.postgres.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		q string
		d referenceDiagnostic
	}{
		{"UPDATE missing SET a=1", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "8"}},
		{"UPDATE /*é*/ missing SET a=1", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "14"}},
		{"UPDATE t SET missing=1", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "14"}},
		{"UPDATE t SET a=missing", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "16"}},
		{"UPDATE t SET a=TRUE", referenceDiagnostic{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Position: "16", Hint: "You will need to rewrite or cast the expression."}},
		{"UPDATE t SET a='bad'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "16"}},
		{"UPDATE t SET a=1,a=2", referenceDiagnostic{Code: "42601", Message: `multiple assignments to same column "a"`}},
		{"UPDATE t SET ctid=1", referenceDiagnostic{Code: "0A000", Message: `cannot assign to system column "ctid"`, Position: "14"}},
		{"UPDATE t SET a=1 WHERE 1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
		{"UPDATE t SET a=1 WHERE", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
		// WHERE analysis precedes target transformation; RHS resolution precedes
		// assignment type checks, even when an earlier assignment has wrong type.
		{"UPDATE t SET missing=1 WHERE 1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "30"}},
		{"UPDATE t SET a=TRUE,b=missing", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			want := tc.d
			want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
			_, err := p.postgres.ExecContext(p.ctx, tc.q)
			referenceAssertDiagnostic(t, err, &want)
			stmt, err := p.postgres.PrepareContext(p.ctx, tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid analysis produced prepared handle")
			}
			referenceAssertDiagnostic(t, err, &want)
		})
	}
	for _, tc := range []struct {
		q    string
		args []any
		d    referenceDiagnostic
	}{
		{"UPDATE t SET n=a", nil, referenceDiagnostic{Code: "22003", Message: "smallint out of range"}},
		{"UPDATE t SET n=$1 WHERE b=2", []any{nil}, referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (1, 2, null).", Schema: "public", Table: "t", Column: "n"}},
		{"UPDATE t SET a=$1 WHERE b=2", []any{"bad"}, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			stmt, err := p.postgres.PrepareContext(p.ctx, tc.q)
			if err != nil {
				t.Fatal("runtime failure rejected during Prepare", err)
			}
			defer stmt.Close()
			_, err = stmt.ExecContext(p.ctx, tc.args...)
			want := tc.d
			want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
			// Named pq prepared statements still use the unnamed execution portal.
			referenceAssertDiagnostic(t, err, &want)
		})
	}
	rows, err := p.postgres.QueryContext(p.ctx, "SELECT a,b,n FROM t ORDER BY b")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := [][]any{{int64(1), int64(2), int64(1)}, {int64(40000), int64(3), int64(1)}}
	got := [][]any{}
	for rows.Next() {
		var a, b, n int64
		if err := rows.Scan(&a, &b, &n); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{a, b, n})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := referenceCompareRows(got, want, false); !ok {
		t.Fatalf("failure atomicity: got %v want %v", got, want)
	}
}
