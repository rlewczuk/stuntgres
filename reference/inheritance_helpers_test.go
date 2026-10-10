package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

// All inheritance definitions share the ordinary external fixture, recorder,
// notice delivery and direct/prepared routes. Discovery changes availability only.
type inheritanceCase struct {
	t        *testing.T
	p        *referencePair
	b        referenceBackend
	c        *sql.Conn
	prepared bool
	notices  *[]referenceDiagnostic
	run      func(referenceStep)
	record   *referenceRecorder
}

func inheritanceScenario(t *testing.T, body func(*inheritanceCase)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					if b.pg {
						// Native control phases can replay their actual Dump on the
						// oracle; each backend scenario starts from an empty public.
						if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
					}
					var notices []referenceDiagnostic
					_, c, record := noticeReferencePool(t, p, b.pg, &notices)
					run := func(s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, record, s)
					}
					body(&inheritanceCase{t, p, b, c, prepared, &notices, run, record})
				})
			}
		})
	}
}

func (s *inheritanceCase) exec(q string, count int64, args ...any) {
	s.run(referenceExec(q, count, args...))
}
func (s *inheritanceCase) fail(q string, d referenceDiagnostic, args ...any) {
	s.run(referenceImplicitError(q, d, args...))
}
func (s *inheritanceCase) values(q string, names, types []string, rows [][]any) {
	s.run(referenceSelect(q, names, types, rows))
}
func (s *inheritanceCase) absent(name string) { tableLikeAbsent(s.run, name, s.prepared) }
func (s *inheritanceCase) notice(q string, want ...referenceDiagnostic) {
	s.t.Helper()
	*s.notices = nil
	s.exec(q, 0)
	if !reflect.DeepEqual(*s.notices, want) {
		s.t.Fatalf("notices for %s: got %#v want %#v", q, *s.notices, want)
	}
}
func inheritanceNotice(message string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
}
func inheritanceMergeNotice(column string) referenceDiagnostic {
	return inheritanceNotice(`merging multiple inherited definitions of column "` + column + `"`)
}
func inheritanceLocalNotice(column string, moved bool) referenceDiagnostic {
	if !moved {
		return inheritanceNotice(`merging column "` + column + `" with inherited definition`)
	}
	d := inheritanceNotice(`moving and merging column "` + column + `" with inherited definition`)
	d.Detail = "User-specified column moved to the position of the inherited column."
	return d
}
func inheritanceDropDiagnostic(parent, child string) referenceDiagnostic {
	return referenceDiagnostic{Code: "2BP01", Message: "cannot drop table " + parent + " because other objects depend on it", Detail: "table " + child + " depends on table " + parent, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}
func inheritanceNullDiagnostic(table, column, row string) referenceDiagnostic {
	return referenceDiagnostic{Code: "23502", Message: `null value in column "` + column + `" of relation "` + table + `" violates not-null constraint`, Detail: "Failing row contains (" + row + ").", Schema: "public", Table: table, Column: column}
}
func inheritanceCheckDiagnostic(table, name, row string) referenceDiagnostic {
	return referenceDiagnostic{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + name + `"`, Detail: "Failing row contains (" + row + ").", Schema: "public", Table: table, Constraint: name}
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
