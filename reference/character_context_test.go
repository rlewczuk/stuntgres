//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

// Character assignment IO must not widen function argument coercion. Character
// casts use the existing comparison/logical surface without replacing its
// noncharacter behavior; remaining capability boundaries are backend-specific.
func TestReferenceCharacterContextBoundaries(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE seed(c CHAR(2))", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES('a')", 1))
	for _, s := range []referenceStep{
		referenceSelect("SELECT c FROM seed WHERE 1=1", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}),
		referenceSelect("SELECT CAST(c AS text)='a',c='a   ',CAST('a ' AS text)=c FROM seed", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}),
		referenceSelect("SELECT c FROM seed WHERE CAST(c AS text)='a' AND NOT CAST('a ' AS text)=c", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}),
		referenceSelect("SELECT text(c),CAST(CAST('123 ' AS varchar) AS text)::int4 FROM seed", []string{"text", "int4"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(123)}}),
		referenceSelect("SELECT CAST(CAST(123 AS text) AS bpchar(4)) FROM seed", []string{"bpchar"}, []string{"BPCHAR"}, [][]any{{"123 "}}),
	} {
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct{ query, nativeCode, postgresCode string }{
		{"SELECT current_setting(TIMESTAMP 'epoch')", "42804", "42883"},
		{"SELECT set_config('application_name',TIMESTAMP 'epoch',FALSE)", "42804", "42883"},
		{`SELECT CAST('a' AS "VARCHAR") FROM seed`, "42704", "42704"},
		{`SELECT 'a'::"character" FROM seed`, "42704", "42704"},
	} {
		q := tc.query
		for _, native := range []bool{true, false} {
			db, code := p.postgres, tc.postgresCode
			if native {
				db, code = p.memory, tc.nativeCode
			}
			for _, prepared := range []bool{false, true} {
				var err error
				if prepared {
					stmt, e := db.PrepareContext(p.ctx, q)
					err = e
					if stmt != nil {
						stmt.Close()
						t.Fatal("unsupported function argument exposed handle")
					}
				} else {
					rows, e := db.QueryContext(p.ctx, q)
					err = e
					if rows != nil {
						rows.Close()
						t.Fatal("unsupported function argument exposed rows")
					}
				}
				if err == nil || referenceExtractDiagnostic(t, err).Code != code {
					t.Fatal(native, prepared, q, err)
				}
			}
		}
	}
	p.step(t, referenceSelect("SELECT c FROM seed", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}}))
}

func TestReferenceCharacterSelectedSingletonCastArguments(t *testing.T) {
	p := newReferencePair(t)
	q := "SELECT num_nulls(CAST($1 AS bpchar),CAST($1 AS text)),num_nonnulls(CAST($1 AS bpchar),CAST($1 AS text))"
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		for _, prepared := range []bool{false, true} {
			var stmt *sql.Stmt
			if prepared {
				var err error
				stmt, err = db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, frame := range []struct {
				input any
				want  []any
			}{
				{"a  ", []any{int64(0), int64(2)}},
				{nil, []any{int64(2), int64(0)}},
				{"z ", []any{int64(0), int64(2)}},
			} {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = stmt.QueryContext(p.ctx, frame.input)
				} else {
					rows, err = db.QueryContext(p.ctx, q, frame.input)
				}
				if err != nil {
					t.Fatal(q, prepared, frame.input, err)
				}
				characterParameterReviewRows(t, rows, []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, frame.want)
			}
			if stmt != nil {
				stmt.Close()
			}
		}
	}
}
