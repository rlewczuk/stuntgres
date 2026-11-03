package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatDatabaseCacheLive(t *testing.T) {
	statDatabaseScenario(t, "cache", func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
		statsExec(t, p, b, observer, "CREATE TABLE stat_db_local(id integer)", 0)
		for _, mode := range []string{"snapshot", "cache", "none"} {
			left, right := create(mode+"_b"), create(mode+"_c")
			baseLeft := statDatabaseSetup(t, p, left, observer)
			baseRight := statDatabaseSetup(t, p, right, observer)
			statsExec(t, p, b, observer, "SET stats_fetch_consistency="+mode, 0)
			statsExec(t, p, b, observer, "BEGIN", 0)
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_stat_user_tables WHERE relname='stat_db_local'", []string{"relname"}, []string{"NAME"}, [][]any{{"stat_db_local"}}))
			// cache captures each accessed object; snapshot must already include B/C
			// after the preceding existing table-statistics entry point.
			if statDatabaseGet(t, p, observer, left.Name) != baseLeft || statDatabaseGet(t, p, observer, right.Name) != baseRight {
				t.Fatal("initial capture lost database facts")
			}
			// A separate outside-transaction observer proves actual publication
			// and admission without clearing either retained cumulative snapshot.
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			statsExec(t, p, b, fresh, "SET stats_fetch_consistency=none", 0)
			for _, target := range []fixture.Backend{left, right} {
				writer, retire := statDatabaseConn(t, p, target)
				live := statDatabaseGet(t, p, observer, target.Name)
				wantLive := int64(1)
				if b.PG {
					// Released backend_status caches the whole backend table for
					// this transaction, independently of cumulative fetch mode.
					wantLive = 0
				}
				if live.Backends != wantLive {
					t.Fatal("retained backend status", mode, live, wantLive)
				}
				if current := statDatabaseGet(t, p, fresh, target.Name); current.Backends != 1 {
					t.Fatal("actual peer admission", current)
				}
				statsExec(t, p, target, writer, "BEGIN", 0)
				statsExec(t, p, target, writer, "INSERT INTO stat_db_rows VALUES(1)", 1)
				statsExec(t, p, target, writer, "COMMIT", 0)
				retire()
			}
			// A separate none-mode reader proves actual publication without clearing
			// the retained observer or forcing any writer.
			statDatabaseAwait(t, p, fresh, left.Name, func(v statDatabaseVector) bool { return v.Insert == baseLeft.Insert+1 && v.Backends == 0 })
			statDatabaseAwait(t, p, fresh, right.Name, func(v statDatabaseVector) bool { return v.Insert == baseRight.Insert+1 && v.Backends == 0 })
			fresh.Close()
			for _, pair := range []struct {
				target fixture.Backend
				base   statDatabaseVector
			}{{left, baseLeft}, {right, baseRight}} {
				got := statDatabaseGet(t, p, observer, pair.target.Name)
				if got.Backends != 0 {
					t.Fatal("cached retirement", got)
				}
				want := pair.base.Insert
				if mode == "none" {
					want++
				}
				if got.Insert != want {
					t.Fatal(mode, got, pair.base)
				}
			}
			statsClear(t, p, b, observer)
			for _, pair := range []struct {
				target fixture.Backend
				base   statDatabaseVector
			}{{left, baseLeft}, {right, baseRight}} {
				if got := statDatabaseGet(t, p, observer, pair.target.Name); got.Insert != pair.base.Insert+1 {
					t.Fatal("clear omitted accessible database", got)
				}
			}
			statsExec(t, p, b, observer, "COMMIT", 0)
			statsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
		}
	})
}

func TestReferenceStatDatabaseSessions(t *testing.T) {
	statDatabaseScenario(t, "sessions", func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
		target := create("target")
		base := statDatabaseSetup(t, p, target, observer)
		writer, retire := statDatabaseConn(t, p, target)
		if got := statDatabaseGet(t, p, observer, target.Name); got.Backends != 1 {
			t.Fatal(got)
		}
		retire()
		got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == base.Sessions+1 })
		// PostgreSQL's InitPostgres commits before the idle client is retired;
		// native registration has no transaction. Cleanup itself adds neither.
		statDatabaseWantRetiredDelta(t, base, got, statDatabaseStartupCommits(target), 0, 0, 0, 0, 1, 0, 0, 0)
		// Real administrator termination is not a statement cancellation.
		writer, end := statDatabaseConn(t, p, target)
		var pid int64
		if err := writer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		var signaled bool
		if err := observer.QueryRowContext(p.Ctx, "SELECT pg_terminate_backend($1,1000)", pid).Scan(&signaled); err != nil || !signaled {
			t.Fatal(signaled, err)
		}
		end()
		killed := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Killed == got.Killed+1 })
		if killed.Abandoned != got.Abandoned || killed.Fatal != got.Fatal || killed.Sessions != got.Sessions+1 {
			t.Fatal(got, killed)
		}
		// Cancellation of an idle target sends no disconnect and leaves it usable.
		writer, end = statDatabaseConn(t, p, target)
		if err := writer.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if err := observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
			t.Fatal(signaled, err)
		}
		fixture.RunStep(t, p.Ctx, writer, target.Recorder, fixture.Select("SELECT 42 AS live", []string{"live"}, []string{"INT4"}, [][]any{{int64(42)}}))
		end()
		canceled := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == killed.Sessions+1 })
		if canceled.Abandoned != killed.Abandoned || canceled.Fatal != killed.Fatal || canceled.Killed != killed.Killed {
			t.Fatal("cancel became disconnect", killed, canceled)
		}
	})
}
