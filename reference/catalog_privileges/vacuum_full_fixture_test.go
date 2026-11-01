package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

type fullVacuumCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	c        *sql.Conn
	prepared bool
	held     map[string]fixture.HeldStatement
}

func fullVacuumCases(t *testing.T, body func(*fullVacuumCase), roles ...string) {
	t.Helper()
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			if len(roles) != 0 {
				fixture.RoleReferenceCleanup(t, roles)
			}
			vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				body(&fullVacuumCase{t: t, p: p, b: b, c: vacuumConn(t, p, b), prepared: held, held: make(map[string]fixture.HeldStatement)})
			})
		})
	}
}
func (f *fullVacuumCase) run(s fixture.Step) { f.t.Helper(); vacuumRun(f.t, f.p, f.b, f.c, s) }
func (f *fullVacuumCase) command(s fixture.Step) {
	f.t.Helper()
	if !f.prepared {
		f.run(s)
		return
	}
	h, ok := f.held[s.Query]
	if !ok {
		h = vacuumHeld(f.t, f.p, f.c, s.Query)
		f.held[s.Query] = h
	}
	vacuumRun(f.t, f.p, f.b, h, s)
}

type fullLocator struct{ oid, locator int64 }

func (f *fullVacuumCase) locator(name string) fullLocator {
	f.t.Helper()
	var v fullLocator
	if err := f.c.QueryRowContext(f.p.Ctx, "SELECT oid,relfilenode FROM pg_class WHERE oid=$1::regclass", name).Scan(&v.oid, &v.locator); err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *fullVacuumCase) rewritten(name string, before fullLocator) fullLocator {
	f.t.Helper()
	after := f.locator(name)
	if after.oid != before.oid || before.locator <= 0 || after.locator <= 0 || after.locator == before.locator {
		f.t.Fatalf("%s rewrite identities before=%+v after=%+v", name, before, after)
	}
	return after
}
func (f *fullVacuumCase) unchanged(name string, before fullLocator) {
	f.t.Helper()
	if after := f.locator(name); after != before {
		f.t.Fatalf("%s changed after rejected/skipped FULL: before=%+v after=%+v", name, before, after)
	}
}
func (f *fullVacuumCase) emptyProgress() {
	f.t.Helper()
	for _, source := range []string{"pg_stat_progress_cluster", "pg_stat_progress_vacuum"} {
		f.run(fixture.Select("SELECT count(*) FROM "+source, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}
