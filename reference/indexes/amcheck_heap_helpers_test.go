package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const heapMissingHint = "No function matches the given name and argument types. You might need to add explicit type casts."
const heapSignature = "(regclass,boolean,boolean,text,bigint,bigint)"

// The fixture resets public, not extension registrations. Keep administrative
// teardown independent of the borrower whose role the scenario changes.
func heapScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := admin.ExecContext(ctx, "DROP EXTENSION IF EXISTS amcheck CASCADE"); err != nil {
					t.Error(err)
				}
				if err := admin.Close(); err != nil {
					t.Error(err)
				}
			})
			fixture.LockExec(t, p.Ctx, admin, "DROP EXTENSION IF EXISTS amcheck CASCADE", 0)
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			body(t, p, b, c)
		})
	}
}

func heapEmpty(q string, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"blkno", "offnum", "attnum", "msg"}, []string{"INT8", "INT4", "INT4", "TEXT"}, nil, args...)
	s.Bag = true
	return s
}

func heapBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func heapDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func heapError(q string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows = true
	return s
}

func heapPosition(q, token string) string {
	return fixture.Position(len([]rune(q[:strings.Index(q, token)])) + 1)
}

func heapInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION amcheck", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE heap_t(a INT,b TEXT)", 0))
}

func heapPhysicalDiagnostic() fixture.Diagnostic {
	d := heapDiagnostic("0A000", "verify_heapam physical heap options are not supported")
	d.Detail = "Native storage has no physical heap blocks, visibility map, or external TOAST chains."
	return d
}

// Invoke under the restored reader's actual authority. Cleanup uses a separate
// bounded context so an assertion failure or expired scenario cannot strand
// the pinned physical borrower in the reader role.
func heapControlReader(t *testing.T, p *fixture.Pair, b fixture.Backend, owner *sql.Conn, allowed bool) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("SET ROLE heap_control_reader", 0))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := owner.ExecContext(ctx, "RESET ROLE"); err != nil { t.Error("reset heap control reader", err) }
	}()
	query := "SELECT * FROM public.verify_heapam('public.heap_t')"
	step := heapEmpty(query)
	if !allowed { step = heapError(query, heapDiagnostic("42501", "permission denied for function verify_heapam")) }
	heapBoth(t, p, b, owner, step)
}
