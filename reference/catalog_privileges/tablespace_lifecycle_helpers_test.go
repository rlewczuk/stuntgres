package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// lifecycleCase keeps the shared scenario, actual driver routes and notice stream
// together. It deliberately has no native descriptor or physical-state injection.
type lifecycleCase struct {
	p       *fixture.Pair
	b       fixture.Backend
	c       *sql.Conn
	notices *[]fixture.Diagnostic
	inTx    bool
}

func tablespaceLifecycleScenario(t *testing.T, roles []string, body func(*testing.T, *lifecycleCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = rec
			s := &lifecycleCase{p: p, b: b, c: c, notices: &notices}
			defer func() {
				// Cleanup must survive a failed assertion or an expired scenario.
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ALL"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("lifecycle session cleanup", q, err)
					}
				}
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			}()
			body(t, s)
		})
	}
}

func (s *lifecycleCase) step(t *testing.T, step fixture.Step) {
	t.Helper()
	fixture.RunStep(t, s.p.Ctx, s.c, s.b.Recorder, step)
}

func (s *lifecycleCase) exec(t *testing.T, q string) {
	t.Helper()
	s.step(t, fixture.Exec(q, 0))
}

func (s *lifecycleCase) begin(t *testing.T) {
	t.Helper()
	s.exec(t, "BEGIN")
	s.inTx = true
}

func (s *lifecycleCase) rollback(t *testing.T) {
	t.Helper()
	s.exec(t, "ROLLBACK")
	s.inTx = false
}

func (s *lifecycleCase) both(t *testing.T, step fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		step.Prepared = prepared
		s.step(t, step)
	}
}

// Each failed route gets its own savepoint. In particular, the prepared route
// must assert the feature error, not the direct route's aborted-transaction error.
func (s *lifecycleCase) error(t *testing.T, q string, want fixture.Diagnostic, parse bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			if s.inTx {
				s.exec(t, "SAVEPOINT ts_lifecycle_error")
				defer func() {
					s.exec(t, "ROLLBACK TO SAVEPOINT ts_lifecycle_error")
					s.exec(t, "RELEASE SAVEPOINT ts_lifecycle_error")
				}()
			}
			step := fixture.ErrorDiagnostic(q, want)
			step.Prepared, step.ErrorStage = prepared, "execute"
			if prepared && parse {
				step.ErrorStage = "prepare"
			}
			s.step(t, step)
		})
	}
}

func (s *lifecycleCase) names(t *testing.T, def, global string) {
	t.Helper()
	rows := tablespaceRows(s.b)
	rows[0][1], rows[1][1] = def, global
	s.both(t, tablespaceStep(s.b, "SELECT oid,spcname,spcowner,spcacl,spcoptions FROM pg_tablespace WHERE oid=1663 OR oid=1664 ORDER BY oid", rows))
}

func (s *lifecycleCase) count(t *testing.T, q string, n int64, args ...any) {
	t.Helper()
	s.both(t, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}}, args...))
}

func (s *lifecycleCase) owner(t *testing.T, name, owner, acl string) {
	t.Helper()
	var expected any
	if acl != "" {
		expected = acl
	}
	s.both(t, fixture.Select("SELECT t.spcname,r.rolname,t.spcowner=r.oid AS same_owner,t.spcacl::text AS acl FROM pg_tablespace t JOIN pg_roles r ON r.oid=t.spcowner WHERE t.spcname=$1", []string{"spcname", "rolname", "same_owner", "acl"}, []string{"NAME", "NAME", "BOOL", "TEXT"}, [][]any{{name, owner, true, expected}}, name))
}

func (s *lifecycleCase) noticesWant(t *testing.T, want ...fixture.Diagnostic) {
	t.Helper()
	if len(*s.notices) != len(want) || len(want) != 0 && !reflect.DeepEqual(*s.notices, want) {
		t.Errorf("notices got %#v want %#v", *s.notices, want)
	}
	*s.notices = nil
}

func lifecycleReserved(name string) fixture.Diagnostic {
	d := tablespaceDiagnostic("42939", fmt.Sprintf("unacceptable tablespace name %q", name))
	d.Detail = "The prefix \"pg_\" is reserved for system tablespaces."
	return d
}

func lifecycleMissing(name string) fixture.Diagnostic {
	return tablespaceDiagnostic("42704", fmt.Sprintf("tablespace %q does not exist", name))
}

func lifecycleQuote(name string) string { return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\"" }

func lifecycleRoute(prepared bool) string {
	if prepared {
		return "prepared"
	}
	return "direct"
}

// Utility query execution exposes the real zero-column Describe contract. The
// separate Exec route checks RowsAffected rather than substituting SQL PREPARE.
func (s *lifecycleCase) utility(t *testing.T, q string, prepared bool) {
	t.Helper()
	step := fixture.Select(q, []string{}, []string{}, [][]any{})
	step.Prepared = prepared
	s.step(t, step)
}
