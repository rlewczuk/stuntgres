package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func pgcMotion(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, row pgcTuple) {
	t.Helper()
	sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM "+row.Name, [][]any{{int64(1), "one"}}, b.PG))
	pgcSame(t, p, b, c, row)
	sqlCursorStep(t, p, b, c, fixture.Exec("MOVE 1 FROM "+row.Name, 1))
	pgcSame(t, p, b, c, row)
	// Querying MOVE independently checks its zero-column public result.
	sqlCursorStep(t, p, b, c, fixture.Select("MOVE 0 FROM "+row.Name, []string{}, []string{}, [][]any{}))
	q := pgcQuery(row.Name)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	pgcSame(t, p, b, held, row)
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM "+row.Name, [][]any{{int64(3), "three"}}, b.PG))
	pgcSame(t, p, b, c, row)
	sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM "+row.Name, [][]any{}, b.PG))
	pgcSame(t, p, b, c, row)
}

func TestReferencePGCursorsOwnerAndMotion(t *testing.T) {
	pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.Exec("CREATE VIEW pgc_invoker AS SELECT name,statement,is_holdable,is_binary,is_scrollable,creation_time FROM pg_catalog.pg_cursors", 0))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(p.Ctx, "ROLLBACK")
		defer peer.ExecContext(p.Ctx, "CLOSE ALL")
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		sqlCursorStep(t, p, b, peer, fixture.Exec("BEGIN", 0))
		source := "DECLARE pgc_shared NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i"
		other := "DECLARE pgc_shared SCROLL CURSOR WITH HOLD FOR SELECT 99 AS i"
		sqlCursorStep(t, p, b, c, fixture.Exec(source, 0))
		sqlCursorStep(t, p, b, peer, fixture.Exec(other, 0))
		row := pgcOne(t, p, b, c, "pgc_shared", source, false, false)
		pgcOne(t, p, b, peer, "pgc_shared", other, true, true)
		only := "DECLARE pgc_owner_only SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i"
		sqlCursorStep(t, p, b, c, fixture.Exec(only, 0))
		scroll := pgcOne(t, p, b, c, "pgc_owner_only", only, false, true)
		pgcEmpty(t, p, b, peer, "pgc_owner_only")
		for _, x := range []fixture.Executor{c, peer} {
			want := source
			if x == peer {
				want = other
			}
			sqlCursorStep(t, p, b, x, fixture.Select("SELECT statement FROM pgc_invoker WHERE name='pgc_shared'", []string{"statement"}, []string{"TEXT"}, [][]any{{want}}))
			sqlCursorStep(t, p, b, x, fixture.Select("SELECT a.statement,b.statement FROM pg_cursors a JOIN (SELECT name,statement FROM pg_cursors) b ON a.name=b.name WHERE a.name='pgc_shared'", []string{"statement", "statement"}, []string{"TEXT", "TEXT"}, [][]any{{want, want}}))
		}
		pgcMotion(t, p, b, c, row)
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH ABSOLUTE 2 FROM pgc_owner_only", [][]any{{int64(2), nil}}, b.PG))
		pgcSame(t, p, b, c, scroll)
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH PRIOR FROM pgc_owner_only", [][]any{{int64(1), "one"}}, b.PG))
		pgcSame(t, p, b, c, scroll)
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM pgc_owner_only", [][]any{{int64(2), nil}}, b.PG))
		if !b.PG {
			// Native metadata results are detached before consumer iteration.
			r, err := c.QueryContext(p.Ctx, pgcQuery(row.Name))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pgc_shared", 0))
			pgcMetadata(t, r, false)
			if !r.Next() {
				t.Fatalf("retained metadata disappeared: %v", r.Err())
			}
			var retained pgcTuple
			if err := r.Scan(&retained.Name, &retained.Statement, &retained.Hold, &retained.Binary, &retained.Scroll, &retained.Created); err != nil {
				t.Fatal(err)
			}
			if retained.Name != row.Name || retained.Statement != row.Statement || retained.Hold != row.Hold || retained.Binary != row.Binary || retained.Scroll != row.Scroll || !retained.Created.Equal(row.Created) {
				t.Fatalf("retained metadata changed: %#v want %#v", retained, row)
			}
			if r.Next() || r.Err() != nil {
				t.Fatalf("extra retained row/error: %v", r.Err())
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			// pq must finish its streaming result before admitting another command.
			pgcSame(t, p, b, c, row)
			sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pgc_shared", 0))
		}
		pgcEmpty(t, p, b, c, "pgc_shared")
		pgcOne(t, p, b, peer, "pgc_shared", other, true, true)
	})
	t.Run("role_and_definer", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pgc_metadata_actor", "pgc_metadata_definer"})
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			defer func() {
				for _, owner := range []*sql.Conn{c, peer} {
					for _, query := range []string{"ROLLBACK", "RESET ROLE", "CLOSE ALL"} {
						if _, err := owner.ExecContext(context.Background(), query); err != nil {
							t.Error("role metadata cleanup", query, err)
						}
					}
				}
			}()
			// Role/view/ACL utilities run before cursor installation and outside
			// explicit transactions; no broader transactional capability is needed.
			for _, query := range []string{
				"CREATE ROLE pgc_metadata_actor",
				"CREATE ROLE pgc_metadata_definer",
				"CREATE VIEW public.pgc_role_view AS SELECT name,statement,is_holdable,is_binary,is_scrollable,creation_time FROM pg_catalog.pg_cursors",
				"ALTER VIEW public.pgc_role_view OWNER TO pgc_metadata_definer",
				"GRANT SELECT ON public.pgc_role_view TO pgc_metadata_actor",
			} {
				sqlCursorStep(t, p, b, c, fixture.Exec(query, 0))
			}
			if b.PG {
				// The PostgreSQL fixture recreates public without its default USAGE.
				// Native public already grants USAGE; native schema GRANT syntax is
				// outside this feature. Keep the actual view ACL shared.
				sqlCursorStep(t, p, b, c, fixture.Exec("GRANT USAGE ON SCHEMA public TO pgc_metadata_actor,pgc_metadata_definer", 0))
			}
			source := "DECLARE pgc_role_shared NO SCROLL CURSOR WITH HOLD FOR SELECT 1 AS owner"
			other := "DECLARE pgc_role_shared SCROLL CURSOR WITH HOLD FOR SELECT 99 AS peer"
			only := "DECLARE pgc_role_only NO SCROLL CURSOR WITH HOLD FOR SELECT 7"
			sqlCursorStep(t, p, b, c, fixture.Exec(source, 0))
			sqlCursorStep(t, p, b, peer, fixture.Exec(other, 0))
			sqlCursorStep(t, p, b, c, fixture.Exec(only, 0))
			ownerRow := pgcOne(t, p, b, c, "pgc_role_shared", source, true, false)
			peerRow := pgcOne(t, p, b, peer, "pgc_role_shared", other, true, true)
			onlyRow := pgcOne(t, p, b, c, "pgc_role_only", only, true, false)
			assert := func(owner *sql.Conn, want pgcTuple) {
				t.Helper()
				pgcSame(t, p, b, owner, want)
				step := fixture.Select("SELECT name,statement,is_holdable,is_binary,is_scrollable,creation_time FROM public.pgc_role_view WHERE name='"+want.Name+"'", pgcColumns, pgcTypes,
					[][]any{{want.Name, want.Statement, want.Hold, false, want.Scroll, want.Created.UTC()}})
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgcMetadata(t, rows, b.PG) }
				sqlCursorStep(t, p, b, owner, step)
			}
			assertBoth := func() {
				t.Helper()
				assert(c, ownerRow)
				assert(c, onlyRow)
				assert(peer, peerRow)
				pgcEmpty(t, p, b, peer, "pgc_role_only")
				sqlCursorStep(t, p, b, peer, fixture.Select("SELECT name,statement,is_holdable,is_binary,is_scrollable,creation_time FROM public.pgc_role_view WHERE name='pgc_role_only'", pgcColumns, pgcTypes, [][]any{}))
			}
			assertBoth()
			for _, roles := range [][2]string{
				{"pgc_metadata_actor", "pgc_metadata_definer"},
				{"pgc_metadata_definer", "pgc_metadata_actor"},
			} {
				for i, owner := range []*sql.Conn{c, peer} {
					sqlCursorStep(t, p, b, owner, fixture.Exec("SET ROLE "+roles[i], 0))
					sqlCursorStep(t, p, b, owner, fixture.Select("SELECT current_user AS actor", []string{"actor"}, []string{"NAME"}, [][]any{{roles[i]}}))
				}
				// The view's definer is deliberately different from one invoker,
				// and the other physical session holds that defining role.
				assertBoth()
			}
			for _, owner := range []*sql.Conn{c, peer} {
				sqlCursorStep(t, p, b, owner, fixture.Exec("RESET ROLE", 0))
				sqlCursorStep(t, p, b, owner, fixture.Select("SELECT current_user=session_user AS reset", []string{"reset"}, []string{"BOOL"}, [][]any{{true}}))
			}
			assertBoth()
		})
	})
}

func TestReferencePGCursorsFeatureSmoke(t *testing.T) {
	pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(p.Ctx, "CLOSE ALL")
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		source := "DECLARE pgc_smoke NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i"
		before := pgcClock(t, p, c)
		sqlCursorStep(t, p, b, c, fixture.Exec(source, 0))
		after := pgcClock(t, p, c)
		row := pgcOne(t, p, b, c, "pgc_smoke", source, false, false)
		pgcBracket(t, row.Created, before, after)
		pgcEmpty(t, p, b, peer, "pgc_smoke")
		pgcMotion(t, p, b, c, row)
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pgc_smoke", 0))
		pgcEmpty(t, p, b, c, "pgc_smoke")
		hold := "DECLARE pgc_hold SCROLL CURSOR WITH HOLD FOR SELECT i,t FROM cursor_rows ORDER BY i"
		sqlCursorStep(t, p, b, c, fixture.Exec(hold, 0))
		h := pgcOne(t, p, b, c, "pgc_hold", hold, true, true)
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM pgc_hold", [][]any{{int64(1), "one"}}, b.PG))
		sqlCursorStep(t, p, b, c, fixture.Exec("COMMIT", 0))
		pgcSame(t, p, b, c, h)
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM pgc_hold", [][]any{{int64(2), nil}}, b.PG))
		peerSource := "DECLARE pgc_peer SCROLL CURSOR WITH HOLD FOR SELECT 99 AS i"
		sqlCursorStep(t, p, b, peer, fixture.Exec(peerSource, 0))
		peerRow := pgcOne(t, p, b, peer, "pgc_peer", peerSource, true, true)
		sqlCursorStep(t, p, b, c, fixture.Exec("DISCARD ALL", 0))
		pgcEmpty(t, p, b, c, "pgc_smoke", "pgc_hold", "pgc_peer")
		pgcSame(t, p, b, peer, peerRow)
	})
}
