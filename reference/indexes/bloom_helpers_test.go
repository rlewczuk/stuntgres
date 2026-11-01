package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// bloomScenario retires the global extension independently of role-changing
// borrowers; resetting public alone cannot remove a global access method.
func bloomScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			clean := func(ctx context.Context) {
				for _, q := range []string{"DROP EXTENSION IF EXISTS bloom CASCADE", "DROP ACCESS METHOD IF EXISTS bloom CASCADE"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				clean(ctx)
				if err := admin.Close(); err != nil {
					t.Error(err)
				}
			})
			clean(p.Ctx)
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				// A fatal assertion inside raw SQL BEGIN or SET ROLE must not
				// strand locks/authority ahead of the independent admin cleanup.
				for _, q := range []string{"ROLLBACK", "RESET ROLE"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c)
		})
	}
}

func bloomRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
}

func bloomBoth(run func(fixture.Step), s fixture.Step) {
	run(s)
	run(fixture.Prepared(s))
}

func bloomError(run func(fixture.Step), query string, d fixture.Diagnostic) {
	s := fixture.ImplicitError(query, d)
	s.ErrorStage = "execute"
	bloomBoth(run, s)
}

func bloomSeed(run func(fixture.Step), table string) {
	run(fixture.Exec("CREATE TABLE "+table+"(i integer,s text)", 0))
	run(fixture.Exec("INSERT INTO "+table+" VALUES(7,'a'),(7,'a'),(7,NULL),(NULL,'a'),(NULL,NULL),(8,'b'),(8,'a')", 7))
}

func bloomInitial() [][]any {
	return [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}, {nil, "a"}, {nil, nil}, {int64(8), "b"}, {int64(8), "a"}}
}

func bloomFinal() [][]any {
	return [][]any{{int64(9), "a"}, {int64(9), "a"}, {int64(9), nil}, {nil, "a"}, {nil, nil}, {int64(9), "z"}}
}

// Stored-column metadata is independently specified, including pq's unknown
// nullability and native's known nullable INT4/TEXT descriptors, even when empty.
func bloomSelect(b fixture.Backend, query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"i", "s"}, []string{"INT4", "TEXT"}, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4", "TEXT"}, []bool{true, true})
	}
	return s
}

func bloomHeld(t *testing.T, p *fixture.Pair, c fixture.Executor, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func bloomIndexAbsent(run func(fixture.Step), name string) {
	run(fixture.Select("SELECT relname FROM pg_class WHERE relname=$1 AND relnamespace='public'::regnamespace", []string{"relname"}, []string{"NAME"}, nil, name))
}
