package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGSequencesDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, q := range []string{"CREATE SEQUENCE pgsv_demand_a", "CREATE SEQUENCE pgsv_demand_b", "CREATE VIEW pgsv_demand_view AS SELECT sequencename,last_value FROM pg_sequences"} {
				pgSequenceExec(t, p, b, a, q)
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT sequencename FROM pg_sequences WHERE sequencename='pgsv_demand_a'", []string{"sequencename"}, []string{"NAME"}, [][]any{{"pgsv_demand_a"}}),
				fixture.Select("SELECT count(*) FROM pg_sequences WHERE sequencename='pgsv_demand_a' OR sequencename='pgsv_demand_b'", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}),
				fixture.Select("SELECT last_value FROM pg_sequences WHERE FALSE", []string{"last_value"}, []string{"INT8"}, [][]any{}),
				fixture.Select("SELECT last_value,last_value FROM pg_sequences WHERE sequencename='pgsv_demand_a'", []string{"last_value", "last_value"}, []string{"INT8", "INT8"}, [][]any{{nil, nil}}),
				fixture.Select("SELECT sequencename FROM pgsv_demand_view WHERE sequencename='pgsv_demand_a'", []string{"sequencename"}, []string{"NAME"}, [][]any{{"pgsv_demand_a"}}),
				fixture.Select("SELECT sequencename FROM (SELECT sequencename FROM pg_sequences) s WHERE sequencename='pgsv_demand_a'", []string{"sequencename"}, []string{"NAME"}, [][]any{{"pgsv_demand_a"}}),
				fixture.Select("SELECT last_value FROM (SELECT sequencename,last_value FROM pg_sequences) s WHERE sequencename='pgsv_demand_a'", []string{"last_value"}, []string{"INT8"}, [][]any{{nil}}),
				fixture.Select("SELECT sequencename FROM (SELECT sequencename FROM pg_sequences) s WHERE sequencename='pgsv_demand_a' FOR UPDATE", []string{"sequencename"}, []string{"NAME"}, [][]any{{"pgsv_demand_a"}}),
			} {
				fixture.PgDescriptionExecutorRows(t, p, b, a, s)
				fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Prepared(s))
			}
			if !b.PG {
				return
			}
			pid := fixture.LockPID(t, p.Ctx, a)
			cases := []struct {
				name, q           string
				locks             []string
				prepare, readonly bool
			}{
				{"definition", "SELECT sequencename FROM pg_sequences WHERE sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				{"count", "SELECT count(*) FROM pg_sequences", []string{"pgsv_demand_a", "pgsv_demand_b"}, false, false},
				{"false", "SELECT last_value FROM pg_sequences WHERE FALSE", nil, false, false},
				{"null_predicate", "SELECT sequencename FROM pg_sequences WHERE last_value IS NULL AND (sequencename='pgsv_demand_a' OR sequencename='pgsv_demand_b')", []string{"pgsv_demand_a", "pgsv_demand_b"}, false, false},
				{"filtered", "SELECT last_value FROM pg_sequences WHERE sequencename='pgsv_demand_b'", []string{"pgsv_demand_b"}, false, false},
				{"sort", "SELECT sequencename FROM pg_sequences WHERE sequencename='pgsv_demand_a' OR sequencename='pgsv_demand_b' ORDER BY last_value", []string{"pgsv_demand_a", "pgsv_demand_b"}, false, false},
				{"full", "SELECT * FROM pg_sequences WHERE sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				{"repeated", "SELECT last_value,last_value FROM pg_sequences WHERE sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				{"prepare_only", "SELECT * FROM pg_sequences WHERE sequencename='pgsv_demand_a'", nil, true, false},
				{"read_only", "SELECT last_value FROM pg_sequences WHERE sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, true},
				{"stored_definition", "SELECT sequencename FROM pgsv_demand_view WHERE sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				{"stored_value", "SELECT last_value FROM pgsv_demand_view WHERE sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				{"join_definition", "SELECT s.sequencename FROM pg_sequences s JOIN pg_class c ON c.relname=s.sequencename WHERE s.sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				{"join_value", "SELECT s.last_value FROM pg_sequences s JOIN pg_class c ON c.relname=s.sequencename WHERE s.sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				// Source-derived admission hypothesis: the outer definition qualifier
				// rejects b before the volatile helper, including across FOR UPDATE.
				// The pinned oracle must establish these modes before native remedy.
				{"derived_definition", "SELECT sequencename FROM (SELECT sequencename FROM pg_sequences) s WHERE sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				{"derived_value", "SELECT last_value FROM (SELECT sequencename,last_value FROM pg_sequences) s WHERE sequencename='pgsv_demand_a'", []string{"pgsv_demand_a"}, false, false},
				{"derived_for_update", "SELECT sequencename FROM (SELECT sequencename FROM pg_sequences) s WHERE sequencename='pgsv_demand_a' FOR UPDATE", []string{"pgsv_demand_a"}, false, false},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					begin := "BEGIN"
					if c.readonly {
						begin = "BEGIN READ ONLY"
					}
					pgSequenceExec(t, p, b, a, begin)
					defer a.ExecContext(context.Background(), "ROLLBACK")
					if c.prepare {
						stmt, err := a.PrepareContext(p.Ctx, c.q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					} else {
						rows, err := a.QueryContext(p.Ctx, c.q)
						if err != nil {
							t.Fatal(err)
						}
						for rows.Next() {
						}
						err = rows.Err()
						closeErr := rows.Close()
						if err != nil {
							t.Fatal(err)
						}
						if closeErr != nil {
							t.Fatal(closeErr)
						}
					}
					rows, err := peer.QueryContext(p.Ctx, "SELECT c.relname,l.mode,l.granted FROM pg_locks l JOIN pg_class c ON c.oid=l.relation WHERE l.pid=$1 AND c.relkind='S' AND (c.relname='pgsv_demand_a' OR c.relname='pgsv_demand_b') ORDER BY c.relname,l.mode", pid)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					var locks []string
					var actual [][]any
					for rows.Next() {
						var name, mode string
						var granted bool
						if err := rows.Scan(&name, &mode, &granted); err != nil {
							t.Fatal(err)
						}
						actual = append(actual, []any{name, mode, granted})
						if mode == "RowExclusiveLock" && granted {
							locks = append(locks, name)
						}
					}
					if rows.Err() != nil {
						t.Fatal(rows.Err())
					}
					if !reflect.DeepEqual(locks, c.locks) {
						t.Errorf("locks=%#v want RX=%q", actual, c.locks)
					}
					switch c.name {
					case "derived_definition", "derived_value", "derived_for_update":
						want := [][]any{{"pgsv_demand_a", "RowExclusiveLock", true}}
						if !reflect.DeepEqual(actual, want) {
							t.Errorf("derived sequence lock modes=%#v want %#v", actual, want)
						}
					}
				})
			}
			pgSequencesLockOracle(t, p, b, a, peer)
		})
	}
}

func pgSequencesLockOracle(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
	pgSequencesDeniedLockOracle(t, p, b, a, peer)
	t.Run("BlockedRestart", func(t *testing.T) {
		pgSequenceExec(t, p, b, a, "BEGIN")
		defer a.ExecContext(context.Background(), "ROLLBACK")
		fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select("SELECT last_value FROM pg_sequences WHERE sequencename='pgsv_demand_a'", []string{"last_value"}, []string{"INT8"}, [][]any{{nil}}))
		holder := fixture.LockPID(t, p.Ctx, a)
		waiter := fixture.LockPID(t, p.Ctx, peer)
		oid := fixture.LockOID(t, p.Ctx, "pgsv_demand_a")
		ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
		defer cancel()
		ch := make(chan error, 1)
		go func() { _, err := peer.ExecContext(ctx, "ALTER SEQUENCE pgsv_demand_a RESTART 17"); ch <- err }()
		joined := false
		defer func() {
			cancel()
			if !joined {
				<-ch
			}
		}()
		fixture.LockWaitBehind(t, ctx, waiter, holder, oid, "ShareRowExclusiveLock")
		var stopped bool
		if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", waiter).Scan(&stopped); err != nil || !stopped {
			t.Fatal("cancel acknowledgement", stopped, err)
		}
		err := <-ch
		joined = true
		if err == nil {
			t.Fatal("blocked restart succeeded")
		}
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		fixture.AssertDiagnostic(t, err, &want)
		pgSequenceExec(t, p, b, a, "ROLLBACK")
		pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgsv_demand_a RESTART 17")
		pgSequencesScalar(t, p, b, peer, "SELECT nextval('pgsv_demand_a')", "nextval", 17)
	})
	for _, c := range []struct {
		name, q string
	}{
		{"BlockedHelper", "SELECT last_value FROM pg_sequences WHERE sequencename='pgsv_demand_b'"},
		{"BlockedDerivedDefinition", "SELECT sequencename FROM (SELECT sequencename FROM pg_sequences) s WHERE sequencename='pgsv_demand_b'"},
		{"BlockedDerivedValue", "SELECT last_value FROM (SELECT sequencename,last_value FROM pg_sequences) s WHERE sequencename='pgsv_demand_b'"},
		{"BlockedDerivedForUpdate", "SELECT sequencename FROM (SELECT sequencename FROM pg_sequences) s WHERE sequencename='pgsv_demand_b' FOR UPDATE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			pgSequenceExec(t, p, b, peer, "BEGIN")
			defer peer.ExecContext(context.Background(), "ROLLBACK")
			pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgsv_demand_b RESTART 31")
			holder := fixture.LockPID(t, p.Ctx, peer)
			waiter := fixture.LockPID(t, p.Ctx, a)
			oid := fixture.LockOID(t, p.Ctx, "pgsv_demand_b")
			ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
			defer cancel()
			ch := make(chan error, 1)
			go func() {
				rows, err := a.QueryContext(ctx, c.q)
				if rows != nil {
					rows.Close()
				}
				ch <- err
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					<-ch
				}
			}()
			fixture.SequenceWaitBehind(t, ctx, waiter, holder, oid)
			var stopped bool
			if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", waiter).Scan(&stopped); err != nil || !stopped {
				t.Fatal("cancel acknowledgement", stopped, err)
			}
			err := <-ch
			joined = true
			if err == nil {
				t.Fatal("blocked helper succeeded")
			}
			want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, err, &want)
			pgSequenceExec(t, p, b, peer, "ROLLBACK")
			fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select("SELECT last_value FROM pg_sequences WHERE sequencename='pgsv_demand_b'", []string{"last_value"}, []string{"INT8"}, [][]any{{nil}}))
		})
	}
}
