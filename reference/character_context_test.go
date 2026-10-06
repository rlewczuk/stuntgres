//go:build reference

package reference_test

import "testing"

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
