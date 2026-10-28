package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var indexProgressRoles = []string{"index_worker", "index_unrelated", "index_member", "index_reader", "index_monitor", "index_effective", "index_effective_member"}

func indexProgressRow(s indexProgressSnapshot) []any {
	row := []any{s.pid, s.datid, s.database, nil, nil, nil, nil}
	if s.relid.Valid {
		row[3] = s.relid.Int64
	}
	if s.index.Valid {
		row[4] = s.index.Int64
	}
	if s.command.Valid {
		row[5] = s.command.String
	}
	if s.phase.Valid {
		row[6] = s.phase.String
	}
	for _, n := range s.counters {
		if n.Valid {
			row = append(row, n.Int64)
		} else {
			row = append(row, nil)
		}
	}
	return row
}

func TestReferenceCreateIndexProgressVisibility(t *testing.T) {
	for _, effective := range []bool{false, true} {
		name := "authenticated"
		if effective {
			name = "worker_set_role"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, indexProgressRoles)
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				admin, holder := vacuumConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
				for _, role := range indexProgressRoles {
					copyProgressExec(t, p.Ctx, admin, "CREATE ROLE "+role+" LOGIN PASSWORD 'role-attributes-fixture'")
					copyProgressExec(t, p.Ctx, admin, "GRANT USAGE ON SCHEMA public TO "+role)
				}
				for _, q := range []string{
					"GRANT index_worker TO index_member",
					"GRANT index_unrelated TO index_member",
					"GRANT pg_read_all_stats TO index_reader",
					"GRANT pg_monitor TO index_monitor",
					"GRANT index_effective TO index_worker",
					"GRANT index_effective TO index_effective_member",
					"GRANT CREATE ON SCHEMA public TO index_effective",
					"CREATE TABLE index_progress_visibility(n INT)",
					"INSERT INTO index_progress_visibility VALUES(1),(2)",
					"GRANT SELECT ON index_progress_visibility TO index_unrelated",
					"ALTER TABLE index_progress_visibility OWNER TO index_effective",
				} {
					copyProgressExec(t, p.Ctx, admin, q)
				}
				worker := copyProgressConn(t, p, fixture.RoleAttributesPool(t, p, b, "index_worker"))
				id := copyProgressIdentify(t, p, worker, "index_progress_visibility")
				if effective {
					copyProgressExec(t, p.Ctx, worker, "SET ROLE index_effective")
				}
				copyProgressExec(t, p.Ctx, holder, "BEGIN")
				copyProgressExec(t, p.Ctx, holder, "LOCK TABLE index_progress_visibility IN ROW EXCLUSIVE MODE")
				run := fixture.ConcurrentIndexStart(t, p, worker, "CREATE INDEX CONCURRENTLY index_progress_visibility_ix ON index_progress_visibility(n)", false)
				visible := indexProgressAwait(t, p, admin, id.pid, "waiting for writers before build", 0)
				indexProgressVisible(t, visible, id, "CREATE INDEX CONCURRENTLY")
				masked := indexProgressSnapshot{pid: id.pid, datid: id.datid, database: id.database}
				for _, role := range indexProgressRoles {
					observer := copyProgressConn(t, p, fixture.RoleAttributesPool(t, p, b, role))
					want := visible
					restricted := role == "index_unrelated" || role == "index_effective" || role == "index_effective_member"
					if restricted {
						want = masked
					}
					q := "SELECT * FROM pg_catalog.pg_stat_progress_create_index WHERE pid=$1"
					s := fixture.Select(q, indexProgressNames, indexProgressTypes, [][]any{indexProgressRow(want)}, id.pid)
					fixture.PGAttributeCheck(t, p, b, observer, s)
					fixture.PGAttributeCheck(t, p, b, observer, fixture.Prepared(s))
					if role == "index_member" {
						stmt, err := observer.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						for _, change := range []struct {
							sql        string
							restricted bool
						}{
							{"SET ROLE index_unrelated", true},
							{"RESET ROLE", false},
						} {
							copyProgressExec(t, p.Ctx, observer, change.sql)
							indexProgressPreparedVisibility(t, p, stmt, id, change.restricted)
						}
						copyProgressExec(t, p.Ctx, admin, "REVOKE index_worker FROM index_member")
						indexProgressPreparedVisibility(t, p, stmt, id, true)
						copyProgressExec(t, p.Ctx, admin, "GRANT index_worker TO index_member")
						indexProgressPreparedVisibility(t, p, stmt, id, false)
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
				adminStmt, err := admin.PrepareContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_progress_create_index WHERE pid=$1")
				if err != nil {
					t.Fatal(err)
				}
				copyProgressExec(t, p.Ctx, admin, "SET SESSION AUTHORIZATION index_unrelated")
				indexProgressPreparedVisibility(t, p, adminStmt, id, true)
				copyProgressExec(t, p.Ctx, admin, "RESET SESSION AUTHORIZATION")
				indexProgressPreparedVisibility(t, p, adminStmt, id, false)
				if err := adminStmt.Close(); err != nil {
					t.Fatal(err)
				}
				copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
				if err := run.Join(t, p); err != nil {
					t.Fatal(err)
				}
				indexProgressIdle(t, p, admin, id.pid)
			})
		})
	}
}

func indexProgressPreparedVisibility(t *testing.T, p *fixture.Pair, stmt *sql.Stmt, id copyProgressIdentity, restricted bool) {
	t.Helper()
	var s indexProgressSnapshot
	args := []any{&s.pid, &s.datid, &s.database, &s.relid, &s.index, &s.command, &s.phase}
	for i := range s.counters {
		args = append(args, &s.counters[i])
	}
	if err := stmt.QueryRowContext(p.Ctx, id.pid).Scan(args...); err != nil {
		t.Fatal(err)
	}
	if restricted {
		indexProgressMasked(t, s, id)
	} else {
		indexProgressVisible(t, s, id, "CREATE INDEX CONCURRENTLY")
	}
}
