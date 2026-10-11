package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableCoreDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(11)", 1))
	for _, tc := range []struct {
		q    string
		want fixture.Diagnostic
	}{
		{"CREATE TABLE t (id integer)", fixture.Diagnostic{Code: "42P07", Message: `relation "t" already exists`}},
		{"CREATE TABLE bad (id integer,id text)", fixture.Diagnostic{Code: "42701", Message: `column "id" specified more than once`}},
		{"CREATE TABLE bad (id no_such_type)", fixture.Diagnostic{Code: "42704", Message: `type "no_such_type" does not exist`, Position: "22"}},
		{"/*é*/ CREATE TABLE bad (id no_such_type)", fixture.Diagnostic{Code: "42704", Message: `type "no_such_type" does not exist`, Position: "28"}},
		{"CREATE TABLE clone (LIKE missing)", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "26"}},
		{"/*é*/ CREATE TABLE clone (LIKE missing)", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "32"}},
		{"CREATE TABLE clone (LIKE public.missing)", fixture.Diagnostic{Code: "42P01", Message: `relation "public.missing" does not exist`, Position: "26"}},
		{"CREATE TABLE sys (ctid integer)", fixture.Diagnostic{Code: "42701", Message: `column name "ctid" conflicts with a system column name`}},
		{"DROP TABLE public.missing", fixture.Diagnostic{Code: "42P01", Message: `table "missing" does not exist`}},
		{"DROP TABLE absent_schema.t", fixture.Diagnostic{Code: "3F000", Message: `schema "absent_schema" does not exist`}},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.q, tc.want)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			p.Step(t, s)
			p.Step(t, fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
		}
	}
}

func TestReferenceTableCoreQuotedTypeNamesAndBoundaries(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			run(fixture.Exec(`CREATE TABLE raw(i "int4" NOT NULL,b "int8",s "text",f "bool")`, 0))
			for _, b := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM raw", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, 0)
			}
			run(fixture.Exec(`CREATE TABLE IF NOT EXISTS raw(id "integer")`, 0))
			for _, b := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM raw", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, 0)
			}
			run(fixture.Exec("INSERT INTO raw VALUES(7,9223372036854775807,'O''Brien',TRUE)", 1))
			unchanged := fixture.Select("SELECT * FROM raw", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien", true}})
			run(unchanged)
			for _, name := range []string{"integer", "int", "bigint", "boolean", "smallint", "real", "decimal", "INTEGER", "INT4", "SERIAL"} {
				s := fixture.ImplicitError(`CREATE TABLE bad (id "`+name+`")`, fixture.Diagnostic{Code: "42704", Message: `type "` + name + `" does not exist`, Position: "22"})
				s.ErrorStage = "execute"
				run(s)
				run(unchanged)
			}
			for _, tc := range []struct{ q, message string }{{`CREATE TABLE bad (id "a""b")`, `type "a"b" does not exist`}, {`CREATE TABLE bad (id "a\b")`, `type "a\b" does not exist`}} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42704", Message: tc.message, Position: "22"})
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec(`CREATE TABLE IF NOT EXISTS raw(id "integer")`, 0))
			run(unchanged)
			// Quoted lowercase serial spellings are CREATE-only shorthand.
			for _, name := range []string{"serial", "smallserial", "bigserial"} {
				q := `CREATE TABLE bad (id "` + name + `")`
				run(fixture.Exec(q, 0))
				run(fixture.Exec(`INSERT INTO bad DEFAULT VALUES`, 1))
				run(fixture.Exec(`DROP TABLE bad RESTRICT`, 0))
			}
		})
	}
}

func TestReferenceTableCoreQuotedDuplicateRelation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			for _, tc := range []struct{ identifier, name, message, nullMessage string }{
				{`"a""b"`, `a"b`, `relation "a"b" already exists`, `null value in column "id" of relation "a"b" violates not-null constraint`},
				{`"a\b"`, `a\b`, `relation "a\b" already exists`, `null value in column "id" of relation "a\b" violates not-null constraint`},
			} {
				run(fixture.Exec("CREATE TABLE "+tc.identifier+"(id INT NOT NULL)", 0))
				run(fixture.Exec("INSERT INTO "+tc.identifier+" VALUES(11)", 1))
				unchanged := fixture.Select("SELECT * FROM "+tc.identifier, []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}})
				run(unchanged)
				s := fixture.ImplicitError("CREATE TABLE "+tc.identifier+"(lost TEXT)", fixture.Diagnostic{Code: "42P07", Message: tc.message})
				s.ErrorStage = "execute"
				run(s)
				run(unchanged)
				for _, b := range p.Backends() {
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, false, "SELECT * FROM "+tc.identifier, []string{"id"}, []string{"INT4"}, []bool{false}, 1)
				}
				// Failed duplicate creation must preserve the original NOT NULL.
				s = fixture.ImplicitError("INSERT INTO "+tc.identifier+" VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: tc.nullMessage, Detail: "Failing row contains (null).", Schema: "public", Table: tc.name, Column: "id"})
				s.ErrorStage = "execute"
				run(s)
				run(unchanged)
			}
		})
	}
}
