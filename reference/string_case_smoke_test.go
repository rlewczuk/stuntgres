package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceStringCaseSmoke(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sc_smoke(id INT,t TEXT,c CHAR(3),v VARCHAR(3))", 0),
			referenceExec("INSERT INTO sc_smoke VALUES(1,'Straße','AbC','ß'),(2,NULL,NULL,NULL)", 2),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT id,lower(t),upper(t),casefold(t),initcap(t),lower(c) AS tail,upper(v COLLATE pg_unicode_fast) AS expansion FROM sc_smoke ORDER BY id", []string{"id", "lower", "upper", "casefold", "initcap", "tail", "expansion"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "straße", "STRAßE", "straße", "StraßE", "abc", "SS"}, {int64(2), nil, nil, nil, nil, nil, nil}}))
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT lower('İ' COLLATE pg_c_utf8) AS simple,casefold('İ' COLLATE pg_c_utf8) AS sf,upper('ßﬃ' COLLATE pg_unicode_fast) AS full,lower('ΑΣ' COLLATE pg_unicode_fast) AS sigma", []string{"simple", "sf", "full", "sigma"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"i", "İ", "SSFFI", "ας"}}))
		q := "SELECT lower($1) AS lo,upper($1) AS up,casefold($1) AS fo,initcap($1) AS ic,casefold($1 COLLATE pg_unicode_fast) AS full FROM sc_smoke WHERE id=1"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			arg any
			row []any
		}{
			{"İ", []any{"İ", "İ", "İ", "İ", "i\u0307"}},
			{"", []any{"", "", "", "", ""}},
			{nil, []any{nil, nil, nil, nil, nil}},
		} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"lo", "up", "fo", "ic", "full"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{tc.row}, tc.arg))
		}
		d := controlsReferenceDiagnostic("42883", "function lower(integer) does not exist")
		d.Position = "15"
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		stringCaseFailure(t, p, b, c, "/*é🙂*/ SELECT lower(1) FROM sc_smoke", d, true)
		for _, s := range []referenceStep{
			referenceExec(`CREATE TABLE sc_smoke_conflict(id INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, 0),
			referenceExec("INSERT INTO sc_smoke_conflict VALUES(1,'A','B'),(2,NULL,'B')", 2),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		stringCaseBoth(t, p, b, c, referenceSelect("SELECT casefold(a||b) AS v FROM sc_smoke_conflict WHERE id=2", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
		d = controlsReferenceDiagnostic("42P22", "could not determine which collation to use for lower() function")
		d.Hint = "Use the COLLATE clause to set the collation explicitly."
		stringCaseFailure(t, p, b, c, "SELECT casefold(a||b) FROM sc_smoke_conflict WHERE id=1", d, false)
		q = "SELECT 'É' COLLATE pg_unicode_fast ILIKE 'é' AS v FROM sc_smoke WHERE id=1"
		if b.pg {
			stringCaseBoth(t, p, b, c, referenceSelect(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
		} else {
			d = controlsReferenceDiagnostic("0A000", "ILIKE is unsupported for this collation")
			d.Position = "36"
			stringCaseFailure(t, p, b, c, q, d, true)
		}
		stringCaseBoth(t, p, b, c, referenceSelect(`SELECT 'A' COLLATE "C" ILIKE 'a' AS yes,'É' COLLATE "C" ILIKE 'é' AS no FROM sc_smoke WHERE id=1`, []string{"yes", "no"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
	})
}
