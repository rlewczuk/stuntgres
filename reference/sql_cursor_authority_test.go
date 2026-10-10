package reference_test

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"
)

func TestReferenceSQLCursorNestedDemand(t *testing.T) {
	for _, route := range []string{"filter", "values", "derived", "view", "join", "union", "correlated"} {
		t.Run(route, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				sqlCursorStep(t, p, b, c, referenceExec("CREATE SEQUENCE nested_draw START 20", 0))
				queries := map[string]string{
					"filter":     "SELECT i FROM cursor_rows WHERE nextval('nested_draw') > 0 ORDER BY i",
					"values":     "SELECT n FROM (VALUES(nextval('nested_draw')),(nextval('nested_draw'))) AS v(n)",
					"derived":    "SELECT i,n FROM (SELECT i,nextval('nested_draw') AS n FROM cursor_rows ORDER BY i OFFSET 0) AS d",
					"view":       "SELECT n FROM nested_view ORDER BY n",
					"join":       "SELECT r.i,nextval('nested_draw') AS n FROM cursor_rows AS r JOIN cursor_rows AS s ON r.i=s.i ORDER BY r.i",
					"union":      "SELECT nextval('nested_draw') AS n UNION ALL SELECT nextval('nested_draw') AS n",
					"correlated": "SELECT r.i,(SELECT nextval('nested_draw') FROM cursor_rows AS s WHERE s.i=r.i) AS n FROM cursor_rows AS r ORDER BY r.i",
				}
				if route == "view" {
					sqlCursorStep(t, p, b, c, referenceExec("CREATE VIEW nested_view AS SELECT i,nextval('nested_draw') AS n FROM cursor_rows", 0))
				}
				sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
				sqlCursorStep(t, p, b, c, referenceExec("DECLARE nested NO SCROLL CURSOR FOR "+queries[route], 0))
				sqlCursorStep(t, p, b, c, referenceSelect("SELECT last_value,is_called FROM nested_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(20), false}}))
				for row := int64(1); row <= 2; row++ {
					columns, types := []string{"i", "n"}, []string{"INT4", "INT8"}
					values := [][]any{{row, row + 19}}
					if route == "values" || route == "union" || route == "view" {
						columns, types, values = []string{"n"}, []string{"INT8"}, [][]any{{row + 19}}
					}
					if route == "filter" {
						columns, types, values = []string{"i"}, []string{"INT4"}, [][]any{{row}}
					}
					sqlCursorStep(t, p, b, c, referenceSelect("FETCH 1 FROM nested", columns, types, values))
					// The predicate sort and the view's ORDER BY volatile output
					// demand all inputs. Ordering the view by n avoids associating
					// sequence draws with unspecified physical table order.
					last := row + 19
					if route == "filter" || route == "view" {
						last = 22
					}
					sqlCursorStep(t, p, b, c, referenceSelect("SELECT last_value,is_called FROM nested_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{last, true}}))
				}
				sqlCursorStep(t, p, b, c, referenceExec("CLOSE nested", 0))
			})
		})
	}
}

func TestReferenceSQLCursorLockingLatestRow(t *testing.T) {
	for _, route := range []string{"direct", "derived", "view"} {
		t.Run(route, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				if route == "view" {
					sqlCursorStep(t, p, b, c, referenceExec("CREATE VIEW locking_view AS SELECT i,t FROM cursor_rows", 0))
				}
				source := "cursor_rows"
				if route == "derived" {
					source = "(SELECT i,t FROM cursor_rows) AS d"
				}
				if route == "view" {
					source = "locking_view"
				}
				sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
				sqlCursorStep(t, p, b, c, referenceExec("DECLARE locked NO SCROLL CURSOR FOR SELECT i,t FROM "+source+" WHERE i >= 2 ORDER BY i FOR UPDATE", 0))
				peer, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				sqlCursorStep(t, p, b, peer, referenceExec("UPDATE cursor_rows SET t='latest' WHERE i=2", 1))
				sqlCursorStep(t, p, b, peer, referenceExec("UPDATE cursor_rows SET i=0 WHERE i=3", 1))
				// LockRows/EPQ sees the committed latest row, and rechecks qualification;
				// ordinary insensitive cursor snapshots have a separate witness.
				sqlCursorStep(t, p, b, c, sqlCursorBatchMetadata("FETCH ALL FROM locked", [][]any{{int64(2), "latest"}}, b.pg, []bool{route == "view", true}))
			})
		})
	}
}

func TestReferenceSQLCursorSecondFetchLockTimeout(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE locked NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i FOR UPDATE", 0))
		sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH 1 FROM locked", [][]any{{int64(1), "one"}}, b.pg))
		peer, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(p.ctx, "ROLLBACK")
		sqlCursorStep(t, p, b, peer, referenceExec("BEGIN", 0))
		sqlCursorStep(t, p, b, peer, referenceSelect("SELECT i FROM cursor_rows WHERE i=2 FOR UPDATE", []string{"i"}, []string{"INT4"}, [][]any{{int64(2)}}))
		sqlCursorStep(t, p, b, c, referenceExec("SET LOCAL lock_timeout='100ms'", 0))
		// PostgreSQL identifies its physical tuple in Where; only the logical
		// locking operation/relation are stable. Native exposes no physical ID.
		sqlCursorStep(t, p, b, c, referenceExec("SAVEPOINT timeout_s", 0))
		_, err = c.ExecContext(p.ctx, "FETCH 1 FROM locked")
		if err == nil {
			t.Fatal("second FETCH unexpectedly escaped peer lock")
		}
		diagnostic := referenceExtractDiagnostic(t, err)
		want := sqlCursorDiagnostic("55P03", "canceling statement due to lock timeout")
		if b.pg {
			if !regexp.MustCompile(`^while locking tuple \([0-9]+,[0-9]+\) in relation "cursor_rows"$`).MatchString(diagnostic.Context) {
				t.Fatalf("lock timeout context: %q", diagnostic.Context)
			}
			want.Context = diagnostic.Context
		}
		referenceAssertDiagnostic(t, err, &want)
		sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK TO timeout_s", 0))
		sqlCursorError(t, p, b, c, "FETCH ALL FROM locked", sqlCursorDiagnostic("55000", "portal \"locked\" cannot be run"))
	})
}

func TestReferenceSQLCursorScopePhases(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		var owner string
		if err := c.QueryRowContext(p.ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		actor, relation := owner, "cursor_rows"
		if b.pg {
			actor, relation = "cursor_actor", "public.cursor_rows"
			sqlCursorStep(t, p, b, c, referenceExec("CREATE ROLE cursor_actor", 0))
			defer func() {
				c.ExecContext(p.ctx, "ROLLBACK")
				c.ExecContext(p.ctx, "RESET ROLE")
				c.ExecContext(p.ctx, "REVOKE SELECT ON public.cursor_rows FROM cursor_actor")
				c.ExecContext(p.ctx, "DROP ROLE cursor_actor")
			}()
			sqlCursorStep(t, p, b, c, referenceExec("GRANT SELECT ON cursor_rows TO cursor_actor", 0))
			sqlCursorStep(t, p, b, c, referenceExec("SET application_name='declare_phase'", 0))
		}
		sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
		boundary := func() time.Time {
			var stamp time.Time
			if err := c.QueryRowContext(p.ctx, "SELECT statement_timestamp()").Scan(&stamp); err != nil {
				t.Fatal(err)
			}
			return stamp.Round(0).UTC()
		}
		appExpression := "'first_fetch'::TEXT"
		if b.pg {
			appExpression = "current_setting('application_name')"
		}
		// Role transitions/GRANT and current_setting are PG capability evidence.
		// Native retains its owner identity and exercises supported time/regclass
		// providers without enabling adjacent SQL role/settings features.
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE phases NO SCROLL CURSOR FOR SELECT i,current_user AS actor,"+appExpression+" AS app,statement_timestamp() AS stamp,'cursor_rows'::REGCLASS AS relation FROM cursor_rows ORDER BY i", 0))
		if b.pg {
			sqlCursorStep(t, p, b, c, referenceExec("SET ROLE cursor_actor", 0))
			sqlCursorStep(t, p, b, c, referenceExec("SET application_name='first_fetch'", 0))
		}
		stmt, err := c.PrepareContext(p.ctx, "FETCH 1 FROM phases")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		before := boundary()
		var first time.Time
		fetch := referenceSelect("FETCH 1 FROM phases", []string{"i", "actor", "app", "stamp", "relation"}, []string{"INT4", "NAME", "TEXT", "TIMESTAMPTZ", "REGCLASS"}, [][]any{{int64(1), actor, "first_fetch", nil, relation}})
		fetch.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
			var i int64
			var actor, app, relation string
			if err := rows.Scan(&i, &actor, &app, &first, &relation); err != nil {
				t.Fatal(err)
			}
			first = first.Round(0).UTC()
			if first.Before(before) {
				t.Fatal("cursor timestamp predates first FETCH", first, before)
			}
			fetch.rows[rowIndex][3] = first
		}
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: fetch.query}
		sqlCursorStep(t, p, b, held, fetch)
		after := boundary()
		if first.After(after) {
			t.Fatal("cursor timestamp follows first FETCH", first, after)
		}
		if b.pg {
			sqlCursorStep(t, p, b, c, referenceExec("RESET ROLE", 0))
			sqlCursorStep(t, p, b, c, referenceExec("SET application_name='second_fetch'", 0))
		}
		sqlCursorStep(t, p, b, c, referenceExec("SET search_path=pg_catalog", 0))
		// Stable expressions retain first-effective-FETCH values despite later
		// commands. REGCLASS rendering still observes the changed search path.
		if b.pg {
			sqlCursorStep(t, p, b, c, referenceSelect("SELECT current_user AS actor,current_setting('application_name') AS app", []string{"actor", "app"}, []string{"NAME", "TEXT"}, [][]any{{owner, "second_fetch"}}))
		} else {
			sqlCursorStep(t, p, b, c, referenceSelect("SELECT current_user AS actor", []string{"actor"}, []string{"NAME"}, [][]any{{owner}}))
		}
		sqlCursorStep(t, p, b, held, referenceSelect(fetch.query, fetch.columns, fetch.types, [][]any{{int64(2), actor, "first_fetch", first, "public.cursor_rows"}}))
		sqlCursorStep(t, p, b, c, referenceExec("CLOSE phases", 0))
		sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK", 0))
	})
}

func TestReferenceSQLCursorCorrelatedFailure(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, referenceExec("CREATE TABLE cursor_matches(i INTEGER,v INTEGER)", 0))
		sqlCursorStep(t, p, b, c, referenceExec("INSERT INTO cursor_matches VALUES(1,10),(2,20),(2,21)", 3))
		sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE correlated_error NO SCROLL CURSOR FOR SELECT r.i,(SELECT m.v FROM cursor_matches AS m WHERE m.i=r.i) AS v FROM (SELECT i FROM cursor_rows ORDER BY i OFFSET 0) AS r", 0))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH 1 FROM correlated_error", []string{"i", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
		sqlCursorError(t, p, b, c, "FETCH 1 FROM correlated_error", sqlCursorDiagnostic("21000", "more than one row returned by a subquery used as an expression"))
		sqlCursorError(t, p, b, c, "FETCH ALL FROM correlated_error", sqlCursorDiagnostic("55000", "portal \"correlated_error\" cannot be run"))
	})
}

func TestReferenceSQLCursorAdvisoryScope(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		peer, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(context.Background(), "SELECT pg_advisory_unlock_all()")
		defer c.ExecContext(context.Background(), "SELECT pg_advisory_unlock_all()")
		sqlCursorStep(t, p, b, c, referenceExec("BEGIN", 0))
		// No row order is asserted: both source occurrences request the same key.
		// Changing peer ownership between batches proves the retained expression
		// evaluates under the second FETCH's admission, not DECLARE/first FETCH.
		sqlCursorStep(t, p, b, c, referenceExec("DECLARE advisory_scope NO SCROLL CURSOR FOR SELECT pg_try_advisory_lock(7401::bigint) AS locked FROM cursor_rows WHERE i <= 2", 0))
		sqlCursorStep(t, p, b, peer, referenceSelect("SELECT pg_try_advisory_lock(7401::bigint)", []string{"pg_try_advisory_lock"}, []string{"BOOL"}, [][]any{{true}}))
		sqlCursorStep(t, p, b, peer, referenceSelect("SELECT pg_advisory_unlock(7401::bigint)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH 1 FROM advisory_scope", []string{"locked"}, []string{"BOOL"}, [][]any{{true}}))
		sqlCursorStep(t, p, b, c, referenceSelect("SELECT pg_advisory_unlock(7401::bigint)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}))
		sqlCursorStep(t, p, b, peer, referenceSelect("SELECT pg_try_advisory_lock(7401::bigint)", []string{"pg_try_advisory_lock"}, []string{"BOOL"}, [][]any{{true}}))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH 1 FROM advisory_scope", []string{"locked"}, []string{"BOOL"}, [][]any{{false}}))
		sqlCursorStep(t, p, b, c, referenceSelect("FETCH ALL FROM advisory_scope", []string{"locked"}, []string{"BOOL"}, [][]any{}))
		sqlCursorStep(t, p, b, c, referenceExec("CLOSE advisory_scope", 0))
		sqlCursorStep(t, p, b, c, referenceExec("ROLLBACK", 0))
		// Cursor/transaction teardown cannot release a physical peer's hold.
		sqlCursorStep(t, p, b, c, referenceSelect("SELECT pg_try_advisory_lock(7401::bigint)", []string{"pg_try_advisory_lock"}, []string{"BOOL"}, [][]any{{false}}))
		sqlCursorStep(t, p, b, peer, referenceSelect("SELECT pg_advisory_unlock(7401::bigint)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}))
		sqlCursorStep(t, p, b, c, referenceSelect("SELECT pg_try_advisory_lock(7401::bigint)", []string{"pg_try_advisory_lock"}, []string{"BOOL"}, [][]any{{true}}))
		sqlCursorStep(t, p, b, c, referenceSelect("SELECT pg_advisory_unlock(7401::bigint)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
