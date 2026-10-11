package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatMetadata(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE format_meta(p text,v text)", 0))
		for _, populated := range []bool{false, true} {
			if populated {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO format_meta VALUES(NULL,'unused'),('%s',''),('%s','x')", 3))
			}
			for _, q := range []string{"SELECT format(p,v) FROM format_meta ORDER BY v", "SELECT format(p,v) AS rendered FROM format_meta ORDER BY v"} {
				t.Run(q+"/"+map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
					name := "format"
					if strings.Contains(q, " AS ") {
						name = "rendered"
					}
					want := [][]any{}
					if populated {
						want = [][]any{{""}, {nil}, {"x"}}
					}
					s := fixture.Select(q, []string{name}, []string{"TEXT"}, want)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.FormatReferenceMetadata(t, b.PG, rows, false) }
					formatReferenceBoth(t, p, b, c, s)
				})
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW format_meta_view AS SELECT format(p,v) AS rendered FROM format_meta", 0))
		s := fixture.Select("SELECT rendered FROM format_meta_view ORDER BY rendered", []string{"rendered"}, []string{"TEXT"}, [][]any{{""}, {"x"}, {nil}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.FormatReferenceMetadata(t, b.PG, rows, true) }
		formatReferenceBoth(t, p, b, c, s)
	})
}

func TestReferenceFormatCollations(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct{ q, want string }{
			{`SELECT lower(format('%s','É' COLLATE "C")) AS v`, "É"},
			{`SELECT lower(format('%s','É' COLLATE pg_unicode_fast)) AS v`, "é"},
			{`SELECT lower(format('%s' COLLATE pg_unicode_fast,'É')) AS v`, "é"},
			{`SELECT lower(format('É','unused' COLLATE pg_unicode_fast)) AS v`, "é"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				formatReferenceBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
			})
		}
		for _, q := range []string{
			`SELECT format('%s' COLLATE "C",'x' COLLATE "POSIX")`,
			`SELECT format(NULL::text COLLATE "C",'unused' COLLATE "POSIX")`,
			`SELECT format('literal','x' COLLATE "C",'unused' COLLATE "POSIX")`,
		} {
			t.Run(q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
				d.Position = fixture.Position(len([]rune(q[:strings.LastIndex(q, "COLLATE")])) + 1)
				formatReferenceFailure(t, p, b, c, q, d, true)
			})
		}
		run(fixture.Exec(`CREATE TABLE format_coll(a text COLLATE "C",b text COLLATE "POSIX")`, 0))
		run(fixture.Exec("INSERT INTO format_coll VALUES('É','x'),(NULL,'y')", 2))
		for _, q := range []string{"SELECT format('%s/%s',a,b) AS v FROM format_coll WHERE a IS NOT NULL", "SELECT format('É',a,b) AS v FROM format_coll WHERE a IS NOT NULL"} {
			want := "É/x"
			if strings.Contains(q, "format('É'") {
				want = "É"
			}
			formatReferenceBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{want}}))
		}
		d := fixture.ControlsReferenceDiagnostic("42P22", "could not determine which collation to use for lower() function")
		d.Hint = "Use the COLLATE clause to set the collation explicitly."
		formatReferenceFailure(t, p, b, c, "SELECT lower(format('%s/%s',a,b)) FROM format_coll WHERE a IS NOT NULL", d, false)
		formatReferenceBoth(t, p, b, c, fixture.Select(`SELECT lower(format('%s/%s',a,b) COLLATE "C") AS v FROM format_coll WHERE a IS NOT NULL`, []string{"v"}, []string{"TEXT"}, [][]any{{"É/x"}}))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT lower(format(NULL,a,b)) AS v FROM format_coll", []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {nil}}))
		d = fixture.ControlsReferenceDiagnostic("42P22", `could not determine which collation to use for view column "v"`)
		d.Hint = "Use the COLLATE clause to set the collation explicitly."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("CREATE VIEW format_conflict AS SELECT format('%s/%s',a,b) AS v FROM format_coll", d)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		run(fixture.Exec(`CREATE VIEW format_conflict AS SELECT format('%s/%s',a,b) COLLATE "C" AS v FROM format_coll`, 0))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT lower(v) AS v FROM format_conflict ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"/y"}, {"É/x"}}))
		run(fixture.Exec(`CREATE VIEW format_collated AS SELECT format('%s',a COLLATE pg_unicode_fast) AS v FROM format_coll`, 0))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT lower(v) AS v FROM format_collated ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{""}, {"é"}}))
	})
}
