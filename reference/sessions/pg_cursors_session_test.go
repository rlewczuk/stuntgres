package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGCursorsSessionAndControls(t *testing.T) {
	t.Run("discard_and_physical_close", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			defer peer.ExecContext(p.Ctx, "CLOSE ALL")
			q := "DECLARE pgc_session NO SCROLL CURSOR WITH HOLD FOR SELECT 1 AS i"
			other := "DECLARE pgc_session SCROLL CURSOR WITH HOLD FOR SELECT 99 AS i"
			sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
			sqlCursorStep(t, p, b, peer, fixture.Exec(other, 0))
			owner := pgcOne(t, p, b, c, "pgc_session", q, true, false)
			peerRow := pgcOne(t, p, b, peer, "pgc_session", other, true, true)
			sqlCursorStep(t, p, b, c, fixture.Exec("DISCARD PLANS", 0))
			pgcSame(t, p, b, c, owner)
			sqlCursorStep(t, p, b, c, fixture.Exec("DISCARD ALL", 0))
			pgcEmpty(t, p, b, c, "pgc_session")
			pgcSame(t, p, b, peer, peerRow)
			sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
			pgcOne(t, p, b, c, "pgc_session", q, true, false)
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
				t.Fatalf("physical discard %v", err)
			}
			replacement, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			pgcEmpty(t, p, b, replacement, "pgc_session")
			pgcSame(t, p, b, peer, peerRow)
		})
	})
	t.Run("healthy_pool_reuse", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			b.DB.SetMaxOpenConns(1)
			b.DB.SetMaxIdleConns(1)
			sqlCursorSetup(t, p, b, c)
			q := "DECLARE pgc_pool NO SCROLL CURSOR WITH HOLD FOR SELECT i,t FROM cursor_rows ORDER BY i"
			sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
			row := pgcOne(t, p, b, c, "pgc_pool", q, true, false)
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM pgc_pool", [][]any{{int64(1), "one"}}, b.PG))
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			borrower, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer borrower.Close()
			defer borrower.ExecContext(p.Ctx, "CLOSE ALL")
			pgcSame(t, p, b, borrower, row)
			sqlCursorStep(t, p, b, borrower, sqlCursorBatch("FETCH NEXT FROM pgc_pool", [][]any{{int64(2), nil}}, b.PG))
			pgcSame(t, p, b, borrower, row)
		})
	})
	t.Run("native_precancel", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorSetup(t, p, b, c)
			q := "DECLARE pgc_cancel NO SCROLL CURSOR WITH HOLD FOR SELECT i,t FROM cursor_rows ORDER BY i"
			sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
			row := pgcOne(t, p, b, c, "pgc_cancel", q, true, false)
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				r, err := c.QueryContext(ctx, pgcQuery("pgc_cancel"))
				if r != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled metadata %v/%v", r, err)
				}
			}
			pgcSame(t, p, b, c, row)
			sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM pgc_cancel", [][]any{{int64(1), "one"}}, b.PG))
		})
	})
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "DECLARE pgc_control NO SCROLL CURSOR WITH HOLD FOR SELECT 1 AS i"
				sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
				row := pgcOne(t, p, b, c, "pgc_control", q, true, false)
				if b.PG {
					// No synthetic PostgreSQL Reset/Restore API: ordinary rollback
					// retains an already committed held cursor and its metadata.
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
					pgcSame(t, p, b, c, row)
					return
				}
				r, err := c.QueryContext(p.Ctx, pgcQuery("pgc_control"))
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				pgcMetadata(t, r, false)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "pgc_control") || strings.Contains(dump.String(), "DECLARE") {
					t.Fatalf("Dump contains cursor metadata: %s", dump.String())
				}
				if replacement == "reset" {
					err = stuntgres.Reset(p.Ctx, p.Native)
				} else {
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()))
				}
				if err != nil {
					t.Fatal(err)
				}
				if !r.Next() {
					t.Fatalf("detached metadata disappeared: %v", r.Err())
				}
				var retained pgcTuple
				if err := r.Scan(&retained.Name, &retained.Statement, &retained.Hold, &retained.Binary, &retained.Scroll, &retained.Created); err != nil {
					t.Fatal(err)
				}
				if retained.Name != row.Name || retained.Statement != row.Statement || retained.Hold != row.Hold || retained.Binary || retained.Scroll != row.Scroll || !retained.Created.Equal(row.Created) {
					t.Fatalf("detached metadata changed %#v", retained)
				}
				if r.Next() || r.Err() != nil {
					t.Fatalf("extra detached row/error: %v", r.Err())
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				// Metadata survives replacement as immutable builtin scalar facts;
				// the actual producer/store retains its original execution fence.
				pgcSame(t, p, b, c, row)
				result, err := c.QueryContext(p.Ctx, "FETCH ALL FROM pgc_control")
				if result != nil {
					t.Fatal("fenced FETCH published Rows")
				}
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			})
		})
	}
}
