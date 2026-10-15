package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// All inheritance definitions share the ordinary external fixture, recorder,
// notice delivery and direct/prepared routes. Discovery changes availability only.
type inheritanceCase struct {
	t           *testing.T
	p           *fixture.Pair
	b           fixture.Backend
	c           *sql.Conn
	prepared    bool
	notices     *[]fixture.Diagnostic
	run         func(fixture.Step)
	record      *fixture.Recorder
	replayRoles []string
}

func inheritanceScenario(t *testing.T, body func(*inheritanceCase), replayRoles ...string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			if len(replayRoles) != 0 {
				// Pair cleanup closes borrowers and resets the schema before roles
				// are dropped. Logical Dump requires its named owners to preexist.
				fixture.RoleReferenceCleanup(t, replayRoles)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				for _, role := range replayRoles {
					step := fixture.Exec("CREATE ROLE "+role+" NOSUPERUSER NOBYPASSRLS", 0)
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
				}
			}
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					if b.PG {
						// Native control phases can replay their actual Dump on the
						// oracle; each backend scenario starts from an empty public.
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
					}
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, record, s)
					}
					body(&inheritanceCase{t, p, b, c, prepared, &notices, run, record, replayRoles})
				})
			}
		})
	}
}

func (s *inheritanceCase) exec(q string, count int64, args ...any) {
	s.run(fixture.Exec(q, count, args...))
}
func (s *inheritanceCase) fail(q string, d fixture.Diagnostic, args ...any) {
	s.run(fixture.ImplicitError(q, d, args...))
}
func (s *inheritanceCase) values(q string, names, types []string, rows [][]any) {
	s.run(fixture.Select(q, names, types, rows))
}
func (s *inheritanceCase) absent(name string) { fixture.TableLikeAbsent(s.run, name, s.prepared) }
func (s *inheritanceCase) notice(q string, want ...fixture.Diagnostic) {
	s.t.Helper()
	*s.notices = nil
	s.exec(q, 0)
	if !reflect.DeepEqual(*s.notices, want) {
		s.t.Fatalf("notices for %s: got %#v want %#v", q, *s.notices, want)
	}
}
func inheritanceNotice(message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
}
func inheritanceMergeNotice(column string) fixture.Diagnostic {
	return inheritanceNotice(`merging multiple inherited definitions of column "` + column + `"`)
}
func inheritanceLocalNotice(column string, moved bool) fixture.Diagnostic {
	if !moved {
		return inheritanceNotice(`merging column "` + column + `" with inherited definition`)
	}
	d := inheritanceNotice(`moving and merging column "` + column + `" with inherited definition`)
	d.Detail = "User-specified column moved to the position of the inherited column."
	return d
}
func inheritanceDropDiagnostic(parent, child string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table " + parent + " because other objects depend on it", Detail: "table " + child + " depends on table " + parent, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}
func inheritanceNullDiagnostic(table, column, row string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "23502", Message: `null value in column "` + column + `" of relation "` + table + `" violates not-null constraint`, Detail: "Failing row contains (" + row + ").", Schema: "public", Table: table, Column: column}
}
func inheritanceCheckDiagnostic(table, name, row string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + name + `"`, Detail: "Failing row contains (" + row + ").", Schema: "public", Table: table, Constraint: name}
}
func (s *inheritanceCase) constraints(table string, rows [][]any) {
	s.values("SELECT c.conname,c.contype AS kind FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid WHERE t.relname='"+table+"' ORDER BY c.conname", []string{"conname", "kind"}, []string{"NAME", "CHAR"}, rows)
}
func (s *inheritanceCase) edges(child string, parents ...string) {
	rows := make([][]any, len(parents))
	for i, parent := range parents {
		rows[i] = []any{parent, "n"}
	}
	s.values("SELECT p.relname,d.deptype AS kind FROM pg_depend d JOIN pg_class p ON p.oid=d.refobjid JOIN pg_class c ON c.oid=d.objid JOIN pg_class dc ON dc.oid=d.classid JOIN pg_class rc ON rc.oid=d.refclassid WHERE dc.relname='pg_class' AND c.relname='"+child+"' AND d.objsubid=0 AND rc.relname='pg_class' AND d.refobjsubid=0 ORDER BY p.relname", []string{"relname", "kind"}, []string{"NAME", "CHAR"}, rows)
}
