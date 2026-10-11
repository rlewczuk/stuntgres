package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec(`CREATE TABLE t(a INT,b TEXT,oid INT,"q""u\z" INT)`, 0))
	p.Step(t, fixture.Exec("CREATE TABLE u(a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7,'keep',9,9)", 1))
	before := referenceDDLNativeDump(t, p)
	cases := []struct{ q, code, message string }{
		{"ALTER TABLE t RENAME missing TO b", "42703", `column "missing" does not exist`},
		{"ALTER TABLE t RENAME a TO b", "42701", `column "b" of relation "t" already exists`},
		{"ALTER TABLE t RENAME a TO a", "42701", `column "a" of relation "t" already exists`},
		{"ALTER TABLE t RENAME TO u", "42P07", `relation "u" already exists`},
		{"ALTER TABLE t RENAME TO t", "42P07", `relation "t" already exists`},
		{`ALTER TABLE t RENAME a TO "q""u\z"`, "42701", `column "q"u\z" of relation "t" already exists`},
		{`ALTER TABLE t RENAME "abs""ent\z" TO b`, "42703", `column "abs"ent\z" does not exist`},
		{"ALTER TABLE missing RENAME TO u", "42P01", `relation "missing" does not exist`},
	}
	for _, system := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
		cases = append(cases, struct{ q, code, message string }{"ALTER TABLE t RENAME " + system + " TO b", "0A000", `cannot rename system column "` + system + `"`}, struct{ q, code, message string }{"ALTER TABLE t RENAME a TO " + system, "42701", `column name "` + system + `" conflicts with a system column name`})
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			p.Step(t, s)
			if referenceDDLNativeDump(t, p) != before {
				t.Fatal("semantic rename failure changed native catalog", tc.q)
			}
			p.Step(t, fixture.Select("SELECT * FROM t", []string{"a", "b", "oid", `q"u\z`}, []string{"INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(7), "keep", int64(9), int64(9)}}))
		}
	}
	p.Step(t, fixture.Exec(`ALTER TABLE t DROP "q""u\z"`, 0))
	for _, prepared := range []bool{false, true} {
		p.Step(t, fixture.RoleReferenceError(`ALTER TABLE t RENAME "q""u\z" TO b`, "42703", `column "q"u\z" does not exist`, "", prepared, "execute"))
	}
	p.Step(t, fixture.Exec("ALTER TABLE t RENAME oid TO ordinary", 0))
	p.Step(t, fixture.Select("SELECT * FROM t", []string{"a", "b", "ordinary"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "keep", int64(9)}}))
}
