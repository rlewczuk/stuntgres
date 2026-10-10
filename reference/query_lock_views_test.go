package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceQueryLockViews(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, clauses := range []string{"FOR UPDATE OF a SKIP LOCKED FOR SHARE OF a NOWAIT", "FOR SHARE OF a NOWAIT FOR UPDATE OF a SKIP LOCKED"} {
			t.Run(clauses+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					referenceLockExec(t, p.ctx, a, "CREATE VIEW selected AS SELECT a.id,a.v FROM t a JOIN u b USING(id) "+clauses, 0)
					referenceLockExec(t, p.ctx, a, "CREATE VIEW nested AS SELECT id,v FROM selected", 0)
					referenceLockExec(t, p.ctx, a, "CREATE VIEW grouped AS SELECT count(*) AS n FROM nested WHERE id=1 HAVING count(*)=0", 0)
					if !b.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						for _, text := range []string{"SKIP LOCKED", "NOWAIT", "OF"} {
							if !strings.Contains(dump.String(), text) {
								t.Fatal("lost stored clause", text, dump.String())
							}
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						if p.postgres != nil {
							// Replay actual owned native SQL, rather than an independently typed copy.
							if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW IF EXISTS grouped,nested,selected; DROP TABLE IF EXISTS t,u"); err != nil {
								t.Fatal(err)
							}
							if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
								t.Fatal(err)
							}
							referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id FROM nested ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
							if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW grouped,nested,selected; DROP TABLE t,u"); err != nil {
								t.Fatal(err)
							}
						}
					}
					holder := queryLockBegin(t, p.ctx, a)
					queryLockRows(t, p, b, holder, "SELECT v FROM nested WHERE id=1 FOR KEY SHARE SKIP LOCKED", prepared, "v", 10)
					referenceRowAffected(t, p.ctx, peer, "UPDATE u SET v=v WHERE id=1", prepared, 1)
					queryLockMigratedBlocked(t, p.ctx, peer, b.pg, "UPDATE t SET v=v WHERE id=1", prepared)
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=2", prepared, 1)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					holder = queryLockBegin(t, p.ctx, a)
					step := referenceSelect("SELECT n FROM grouped", []string{"n"}, []string{"INT8"}, [][]any{})
					step.prepared = prepared
					referenceRunStep(t, p.ctx, holder, b.recorder, step)
					queryLockMigratedBlocked(t, p.ctx, peer, b.pg, "DELETE FROM t WHERE id=1", prepared)
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=2", prepared, 1)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					blocker := queryLockBegin(t, p.ctx, peer)
					queryLockRows(t, p, b, blocker, "SELECT id FROM t WHERE id=1 FOR NO KEY UPDATE", false, "id", 1)
					queryLockNowait(t, p, b, a, "SELECT id FROM nested WHERE id=1 FOR KEY SHARE SKIP LOCKED", prepared, "t")
					if err := blocker.Rollback(); err != nil {
						t.Fatal(err)
					}
					stmt, err := a.PrepareContext(p.ctx, "SELECT v FROM nested WHERE id=1 FOR SHARE NOWAIT")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					referenceLockExec(t, p.ctx, a, "ALTER TABLE t RENAME TO renamed", 0)
					rows, err := stmt.QueryContext(p.ctx)
					queryLockMigratedRows(t, rows, err, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
					referenceLockExec(t, p.ctx, a, "CREATE OR REPLACE VIEW nested AS SELECT id,v+1 AS v FROM selected", 0)
					rows, err = stmt.QueryContext(p.ctx)
					queryLockMigratedRows(t, rows, err, []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}})
					if !b.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						queryLockRows(t, p, b, a, "SELECT v FROM nested WHERE id=1", prepared, "v", 11)
					}
				})
			})
		}
	}
	// A stored-local mark below the nullable branch stays legal and skips whole
	// real inputs; it does not mark a manufactured NULL extension.
	queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
		referenceLockExec(t, p.ctx, a, "CREATE VIEW local AS SELECT id,v FROM u FOR UPDATE OF u SKIP LOCKED", 0)
		holder := queryLockBegin(t, p.ctx, a)
		queryLockRows(t, p, b, holder, "SELECT id FROM u WHERE id=1 FOR UPDATE", false, "id", 1)
		reader := queryLockBegin(t, p.ctx, peer)
		queryLockRows(t, p, b, reader, "SELECT a.id FROM t a LEFT JOIN local b USING(id) ORDER BY a.id FOR SHARE OF a NOWAIT", true, "id", 1, 2, 3, 4)
	})
}

func TestReferenceQueryLockAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"query_lock_reader"})
	queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
		referenceLockExec(t, p.ctx, a, "CREATE ROLE query_lock_reader", 0)
		if b.pg {
			referenceLockExec(t, p.ctx, a, "GRANT USAGE ON SCHEMA public TO query_lock_reader", 0)
		}
		referenceLockExec(t, p.ctx, a, "GRANT SELECT ON t,u TO query_lock_reader", 0)
		referenceLockExec(t, p.ctx, a, "GRANT UPDATE(v) ON t TO query_lock_reader", 0)
		referenceLockExec(t, p.ctx, a, "CREATE VIEW owner_view AS SELECT a.id,a.v FROM t a JOIN u b USING(id) FOR UPDATE OF a NOWAIT", 0)
		referenceLockExec(t, p.ctx, a, "CREATE VIEW invoker_view WITH(security_invoker=true) AS SELECT a.id,a.v FROM t a JOIN u b USING(id) FOR UPDATE OF a NOWAIT", 0)
		referenceLockExec(t, p.ctx, a, "GRANT SELECT ON owner_view,invoker_view TO query_lock_reader", 0)
		referenceLockExec(t, p.ctx, peer, "SET ROLE query_lock_reader", 0)
		t.Cleanup(func() { peer.ExecContext(p.ctx, "RESET ROLE") })
		for _, prepared := range []bool{false, true} {
			queryLockRows(t, p, b, peer, "SELECT b.v FROM t a JOIN u b USING(id) WHERE a.id=1 FOR UPDATE OF a NOWAIT", prepared, "v", 10)
			queryLockFailure(t, p, b, peer, "SELECT a.v FROM t a JOIN u b USING(id) FOR UPDATE OF b NOWAIT", prepared, referenceDiagnostic{Code: "42501", Message: "permission denied for table u"}, "execute")
			queryLockRows(t, p, b, peer, "SELECT v FROM owner_view WHERE id=1", prepared, "v", 10)
			queryLockRows(t, p, b, peer, "SELECT v FROM invoker_view WHERE id=1", prepared, "v", 10)
		}
		referenceLockExec(t, p.ctx, peer, "RESET ROLE", 0)
	})
}
