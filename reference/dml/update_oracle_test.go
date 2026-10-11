//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// This oracle runs PostgreSQL alone so analysis/Prepare failures are captured
// before the embedded implementation is changed. All portable fields are exact.
func TestReferenceUpdateCoreOracle(t *testing.T) {
	p := fixture.NewPair(t)
	for _, q := range []string{"CREATE TABLE t(a INT,b INT,n SMALLINT NOT NULL)", "INSERT INTO t VALUES(1,2,1),(40000,3,1)"} {
		if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		q string
		d fixture.Diagnostic
	}{
		{"UPDATE missing SET a=1", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "8"}},
		{"UPDATE /*é*/ missing SET a=1", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "14"}},
		{"UPDATE t SET missing=1", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "14"}},
		{"UPDATE t SET a=missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "16"}},
		{"UPDATE t SET a=TRUE", fixture.Diagnostic{Code: "42804", Message: `column "a" is of type integer but expression is of type boolean`, Position: "16", Hint: "You will need to rewrite or cast the expression."}},
		{"UPDATE t SET a='bad'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "16"}},
		{"UPDATE t SET a=1,a=2", fixture.Diagnostic{Code: "42601", Message: `multiple assignments to same column "a"`}},
		{"UPDATE t SET ctid=1", fixture.Diagnostic{Code: "0A000", Message: `cannot assign to system column "ctid"`, Position: "14"}},
		{"UPDATE t SET a=1 WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "24"}},
		{"UPDATE t SET a=1 WHERE", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "23"}},
		// WHERE analysis precedes target transformation; RHS resolution precedes
		// assignment type checks, even when an earlier assignment has wrong type.
		{"UPDATE t SET missing=1 WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "30"}},
		{"UPDATE t SET a=TRUE,b=missing", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			want := tc.d
			want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
			_, err := p.Postgres.ExecContext(p.Ctx, tc.q)
			fixture.AssertDiagnostic(t, err, &want)
			stmt, err := p.Postgres.PrepareContext(p.Ctx, tc.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid analysis produced prepared handle")
			}
			fixture.AssertDiagnostic(t, err, &want)
		})
	}
	for _, tc := range []struct {
		q    string
		args []any
		d    fixture.Diagnostic
	}{
		{"UPDATE t SET n=a", nil, fixture.Diagnostic{Code: "22003", Message: "smallint out of range"}},
		{"UPDATE t SET n=$1 WHERE b=2", []any{nil}, fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (1, 2, null).", Schema: "public", Table: "t", Column: "n"}},
		{"UPDATE t SET a=$1 WHERE b=2", []any{"bad"}, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			stmt, err := p.Postgres.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal("runtime failure rejected during Prepare", err)
			}
			defer stmt.Close()
			_, err = stmt.ExecContext(p.Ctx, tc.args...)
			want := tc.d
			want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
			// Named pq prepared statements still use the unnamed execution portal.
			fixture.AssertDiagnostic(t, err, &want)
		})
	}
	rows, err := p.Postgres.QueryContext(p.Ctx, "SELECT a,b,n FROM t ORDER BY b")
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
	if _, ok := fixture.CompareRows(got, want, false); !ok {
		t.Fatalf("failure atomicity: got %v want %v", got, want)
	}
}
