package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCheckCorruption(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pv_part")
	p := fixture.NewScenario(t)
	backends := p.Backends()
	for i := len(backends) - 1; i >= 0; i-- {
		initial := backends[i]
		t.Run(initial.Name, func(t *testing.T) {
			// No Backend, Conn, Rows, Stmt, Tx or COPY borrower survives a stop.
			var c *sql.Conn
			b := initial
			acquire := func() {
				for _, current := range p.Backends() {
					if current.PG == initial.PG {
						b = current
					}
				}
				c = pvConn(t, p, b)
			}
			release := func() {
				if c != nil {
					if err := c.Close(); err != nil {
						t.Error(err)
					}
					c = nil
				}
			}
			acquire()
			defer release()
			if b.PG {
				for _, q := range []string{"DROP EXTENSION IF EXISTS pg_visibility CASCADE", "DROP SCHEMA IF EXISTS pv_part CASCADE"} {
					pvRun(t, p, b, c, fixture.Exec(q, 0))
				}
				// This cleanup dynamically borrows the replacement pool, never c.
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"DROP EXTENSION IF EXISTS pg_visibility CASCADE", "DROP SCHEMA IF EXISTS pv_part CASCADE"} {
						if _, err := p.Postgres.ExecContext(ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				})
			}
			for _, q := range []string{"CREATE SCHEMA pv_part", "CREATE EXTENSION pg_visibility SCHEMA pv_part"} {
				pvRun(t, p, b, c, fixture.Exec(q, 0))
			}
			if b.PG {
				copyConn, err := p.CopyPostgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer copyConn.Close()
					defer pvRollback(copyConn)
					for _, q := range []string{"BEGIN", "CREATE TABLE pv_part.heap(k integer) WITH(autovacuum_enabled=false)"} {
						if _, err := copyConn.ExecContext(p.Ctx, q); err != nil {
							t.Fatal(err)
						}
					}
					result, err := fixture.CopyFrom(p.Ctx, copyConn, true, "COPY pv_part.heap FROM STDIN WITH (FREEZE)", strings.NewReader("1\n2\n"))
					if err != nil || result.Count != 2 {
						t.Fatal("COPY FREEZE", result, err)
					}
					if _, err := copyConn.ExecContext(p.Ctx, "COMMIT"); err != nil {
						t.Fatal(err)
					}
				}()
			} else {
				pvRun(t, p, b, c, fixture.Exec("CREATE TABLE pv_part.heap(k integer)", 0))
				pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1),(2)", 2))
				pvFreeze(t, p, b, c)
			}
			pvcChecks(t, p, b, c, "pv_part.heap", nil)
			pvProbe(t, p, b, c, "pv_part.heap", 0, true, true, true)
			if !b.PG {
				// Real native contradictory claims are installed-host internal units,
				// not a fabricated SQL corruption facility. Keep healthy public cells.
				pvcTruncate(t, p, b, c, "pv_part.heap")
				pvcChecks(t, p, b, c, "pv_part.heap", nil)
				pvBoth(t, p, b, c, fixture.Select("SELECT k FROM pv_part.heap ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				return
			}
			pvBoth(t, p, b, c, fixture.Select("SELECT k,ctid FROM pv_part.heap ORDER BY k", []string{"k", "ctid"}, []string{"INT4", "TID"}, [][]any{{int64(1), "(0,1)"}, {int64(2), "(0,2)"}}))
			offline := fixture.NewOfflineVisibilityMap(t, p, "pv_part.heap")
			release()
			frozen, err := offline.Snapshot(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			acquire()
			pvRun(t, p, b, c, fixture.Exec("DELETE FROM pv_part.heap WHERE k=1", 1))
			pvBoth(t, p, b, c, fixture.Select("SELECT k,ctid FROM pv_part.heap ORDER BY k", []string{"k", "ctid"}, []string{"INT4", "TID"}, [][]any{{int64(2), "(0,2)"}}))
			pvcChecks(t, p, b, c, "pv_part.heap", nil)
			release()
			if err := offline.InstallStale(p.Ctx, frozen); err != nil {
				t.Fatal(err)
			}
			acquire()
			// No DISTINCT: exactly one finding, with stable independently known TID.
			pvcChecks(t, p, b, c, "pv_part.heap", [][]any{{"(0,1)"}})
			pvProbe(t, p, b, c, "pv_part.heap", 0, true, true, false)
			pvBoth(t, p, b, c, fixture.Select("SELECT k FROM pv_part.heap", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}))
			pvcTruncate(t, p, b, c, "pv_part.heap")
			pvProbe(t, p, b, c, "pv_part.heap", 0, false, false, false)
			pvcChecks(t, p, b, c, "pv_part.heap", nil)
			pvRun(t, p, b, c, fixture.Exec("VACUUM pv_part.heap", 0))
			pvcChecks(t, p, b, c, "pv_part.heap", nil)
		})
	}
}
