package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendCurrentShape(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		activityExec(t, p, peer, "SET application_name='backend current idle'")
		for kind, name := range backendFunctionNames {
			q := "SELECT * FROM pg_catalog." + name + "($1)"
			held, err := o.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, prepared := range []bool{false, true} {
				var e fixture.Executor = o
				if prepared {
					e = fixture.HeldStatement{Executor: o, Statement: held, SQL: q}
				}
				for _, input := range []any{pid, int64(0), nil, int64(-2147483648), int64(2147483647), pid} {
					rows := backendQuery(t, p, b.PG, e, kind, input)
					known := input == pid
					switch kind {
					case 0:
						if input == nil {
							if len(rows) == 0 {
								t.Fatal("NULL activity means all actual sessions")
							}
							continue
						}
						if !known {
							if len(rows) != 0 {
								t.Fatal("unknown PID", rows)
							}
							continue
						}
						if len(rows) != 1 || rows[0][1] != pid || rows[0][3] != "backend current idle" || rows[0][4] != "idle" || rows[0][17] != "client backend" {
							t.Fatal("actual idle activity", rows)
						}
						for _, i := range []int{18, 25, 27, 28} {
							if rows[0][i] != false {
								t.Fatal("absent native/fixture security", i, rows[0][i])
							}
						}
						for _, i := range []int{19, 20, 21, 22, 23, 24, 26, 29} {
							if rows[0][i] != nil {
								t.Fatal("unavailable security/leader", i, rows[0][i])
							}
						}
						if !b.PG {
							for _, i := range []int{12, 13, 14} {
								if rows[0][i] != nil {
									t.Fatal("invented endpoint", rows[0][i])
								}
							}
						}
					case 1:
						if known {
							backendIOCheck(t, rows, b.PG)
						} else if len(rows) != 0 {
							t.Fatal("strict/unknown IO empty", rows)
						}
					case 2:
						backendWALCheck(t, rows, b.PG, known)
					}
				}
			}
		}
		for _, input := range []string{"0", "NULL"} {
			if n := backendCount(t, p, o, "SELECT count(*) FROM pg_stat_get_backend_io("+input+")"); n != 0 {
				t.Fatal(n)
			}
			var count, nonnull int64
			if err := o.QueryRowContext(p.Ctx, "SELECT count(*),count(wal_records) FROM pg_stat_get_backend_wal("+input+")").Scan(&count, &nonnull); err != nil || count != 1 || nonnull != 0 {
				t.Fatal("scalar NULL record cardinality", count, nonnull, err)
			}
			var null bool
			if err := o.QueryRowContext(p.Ctx, "SELECT pg_stat_get_backend_wal("+input+") IS NULL").Scan(&null); err != nil || !null {
				t.Fatal(null, err)
			}
		}
		if backendStamp(t, p, b.PG, o, pid, true) != nil || backendStamp(t, p, b.PG, o, pid, false) != nil {
			t.Fatal("fresh backend reset must be NULL")
		}
	})
}

func TestReferenceBackendCurrentSourcesAndMetadata(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		// Describe before the target is born must not sample a provider or raw cut.
		held, err := o.PrepareContext(p.Ctx, "SELECT * FROM pg_stat_get_activity($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		peer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		rows, err := held.QueryContext(p.Ctx, pid)
		if err != nil {
			t.Fatal(err)
		}
		if got := backendRows(t, rows, b.PG, backendActivityNames, backendActivityTypes); len(got) != 1 {
			t.Fatal("Prepare captured membership", got)
		}
		for kind, name := range backendFunctionNames {
			for _, q := range []string{"SELECT * FROM " + name + "(0) LIMIT 0", "SELECT (" + name + "(0)).* LIMIT 0", "SELECT * FROM " + name + "(NULL) LIMIT 0"} {
				for _, prepare := range []bool{false, true} {
					var r *sql.Rows
					if prepare {
						s, err := o.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						r, err = s.QueryContext(p.Ctx)
						if err != nil {
							s.Close()
							t.Fatal(err)
						}
						got := backendRows(t, r, b.PG, backendOutputNames[kind], backendOutputTypes[kind])
						s.Close()
						if len(got) != 0 {
							t.Fatal(got)
						}
					} else {
						r, err = o.QueryContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						if got := backendRows(t, r, b.PG, backendOutputNames[kind], backendOutputTypes[kind]); len(got) != 0 {
							t.Fatal(got)
						}
					}
				}
			}
			q := "SELECT " + name + "(0) AS value LIMIT 0"
			r, err := o.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			backendRows(t, r, b.PG, []string{"value"}, []string{"RECORD"})
		}
		steps := []fixture.Step{
			fixture.Select("SELECT wal_records,g,n FROM ROWS FROM(pg_stat_get_backend_wal(0),generate_series(1,2)) WITH ORDINALITY AS r(wal_records,wal_fpi,wal_bytes,wal_buffers_full,stats_reset,g,n) ORDER BY n", []string{"wal_records", "g", "n"}, []string{"INT8", "INT4", "INT8"}, [][]any{{nil, int64(1), int64(1)}, {nil, int64(2), int64(2)}}),
			fixture.Select("SELECT x.v,i.ordinality FROM (VALUES(0),(NULL::integer)) x(v) LEFT JOIN LATERAL pg_stat_get_backend_io(x.v) WITH ORDINALITY AS i ON true ORDER BY x.v NULLS LAST", []string{"v", "ordinality"}, []string{"INT4", "INT8"}, [][]any{{int64(0), nil}, {nil, nil}}),
			fixture.Select("SELECT reads,g,n FROM ROWS FROM(pg_stat_get_backend_io(0),generate_series(1,2)) WITH ORDINALITY AS r(backend_type,object,context,reads,read_bytes,read_time,writes,write_bytes,write_time,writebacks,writeback_time,extends,extend_bytes,extend_time,hits,evictions,reuses,fsyncs,fsync_time,stats_reset,g,n) ORDER BY n", []string{"reads", "g", "n"}, []string{"INT8", "INT4", "INT8"}, [][]any{{nil, int64(1), int64(1)}, {nil, int64(2), int64(2)}}),
			fixture.Select("SELECT count(*),min(ordinality),max(ordinality) FROM pg_stat_get_backend_io($1) WITH ORDINALITY i WHERE ordinality>2", []string{"count", "min", "max"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(6), int64(3), int64(8)}}, pid),
			fixture.Select("SELECT count(*),min(ordinality),max(ordinality) FROM (SELECT ordinality FROM pg_stat_get_backend_io($1) WITH ORDINALITY ORDER BY ordinality OFFSET 3 LIMIT 2) i", []string{"count", "min", "max"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(4), int64(5)}}, pid),
			fixture.Select("SELECT count(*) FROM pg_stat_get_backend_io($1) i,generate_series(1,2) g", []string{"count"}, []string{"INT8"}, [][]any{{int64(16)}}, pid),
		}
		for _, step := range steps {
			fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Prepared(step))
		}
		q := fmt.Sprintf("SELECT count(*),min(ordinality),max(ordinality) FROM (VALUES(%d),(%d)) x(v),LATERAL pg_stat_get_backend_io(x.v) WITH ORDINALITY i", pid, pid)
		step := fixture.Select(q, []string{"count", "min", "max"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(16), int64(1), int64(8)}})
		fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
		fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Prepared(step))
	})
	backendCurrentProtocol(t)
}

func TestReferenceBackendCurrentIdentity(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		for _, arg := range []string{"NULL", "-1", fmt.Sprint(pid)} {
			q := "SELECT count(*) FROM pg_stat_get_activity(" + arg + ") WHERE pid=$1"
			if backendCount(t, p, o, q, pid) != 1 {
				t.Fatal("PID selects actual idle session", arg)
			}
		}
		for _, arg := range []string{"0", "-2", "-2147483648", "2147483647"} {
			if backendCount(t, p, o, "SELECT count(*) FROM pg_stat_get_activity("+arg+")") != 0 {
				t.Fatal("non-PID admitted", arg)
			}
		}
		activityExec(t, p, o, "CREATE DATABASE backend_current_peer")
		defer o.ExecContext(context.Background(), "DROP DATABASE IF EXISTS backend_current_peer")
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, "backend_current_peer", "")
		defer pool.Close()
		other, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		otherPID := statActivityPID(t, p, other)
		if otherPID == pid {
			t.Fatal("physical sessions share a PID")
		}
		var database int64
		if err := other.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		got := backendQuery(t, p, b.PG, o, 0, otherPID)
		if len(got) != 1 || fmt.Sprint(got[0][0]) != fmt.Sprint(database) {
			t.Fatal("cross-database ownership", got, database)
		}
		backendIOCheck(t, backendQuery(t, p, b.PG, o, 1, otherPID), b.PG)
		backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, otherPID), b.PG, true)
		// Only real processes returned by the fixture may supply exclusion witnesses.
		r, err := o.QueryContext(p.Ctx, "SELECT pid FROM pg_stat_activity WHERE backend_type IN ('checkpointer','background writer','startup','autovacuum launcher')")
		if err != nil {
			t.Fatal(err)
		}
		var excluded []int64
		for r.Next() {
			var id int64
			if err := r.Scan(&id); err != nil {
				t.Fatal(err)
			}
			excluded = append(excluded, id)
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
		r.Close()
		for _, id := range excluded {
			if len(backendQuery(t, p, b.PG, o, 1, id)) != 0 {
				t.Fatal("excluded kind")
			}
			backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, id), b.PG, false)
		}
	})
}
