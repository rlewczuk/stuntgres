package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWTableHierarchyLockLifetime(t *testing.T) {
	t.Run("held-current-arguments", func(t *testing.T) {
		fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, _ *fdwSQLRowLocker, run func(fixture.Step)) {
			fdwHierarchyLockEmptySource(t, p, source)
			run(fixture.Exec("CREATE TABLE fdw_hlock_parent(id integer)", 0))
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_hlock_empty() INHERITS(fdw_hlock_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'hlock_empty')", 0))
			run(fixture.Exec("INSERT INTO fdw_hlock_parent VALUES(1),(2)", 2))
			peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			query := "SELECT id FROM fdw_hlock_parent WHERE id=$1 FOR UPDATE"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			t.Cleanup(func() { fdwRowLockRollback(t, c) })
			for _, id := range []int64{1, 2} {
				run(fixture.Exec("BEGIN", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{id}}, id))
				selected := fmt.Sprintf("SELECT id FROM ONLY fdw_hlock_parent WHERE id=%d FOR UPDATE NOWAIT", id)
				otherID := 3 - id
				other := fmt.Sprintf("SELECT id FROM ONLY fdw_hlock_parent WHERE id=%d FOR UPDATE NOWAIT", otherID)
				fdwHierarchyLockProbe(t, p, b, peer, selected, "id", "fdw_hlock_parent", id, true)
				fdwHierarchyLockProbe(t, p, b, peer, other, "id", "fdw_hlock_parent", otherID, false)
				end := "ROLLBACK"
				if id == 2 {
					end = "COMMIT"
				}
				run(fixture.Exec(end, 0))
				fdwHierarchyLockProbe(t, p, b, peer, selected, "id", "fdw_hlock_parent", id, false)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			fdwHierarchyLockEmptyRows(t, p, b, source)
		})
	})
	t.Run("cursor-local-reordered-child", func(t *testing.T) {
		fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, _ *fdwSQLRowLocker, run func(fixture.Step)) {
			fdwHierarchyLockEmptySource(t, p, source)
			run(fixture.Exec("CREATE TABLE fdw_hlock_parent(id integer,label text,flag boolean)", 0))
			run(fixture.Exec("CREATE TABLE fdw_hlock_child(label text,flag boolean,id integer)", 0))
			run(fixture.Exec("ALTER TABLE fdw_hlock_child INHERIT fdw_hlock_parent", 0))
			run(fixture.Exec("CREATE FOREIGN TABLE fdw_hlock_empty() INHERITS(fdw_hlock_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'hlock_empty')", 0))
			run(fixture.Exec("INSERT INTO fdw_hlock_child(id,label,flag) VALUES(1,'one',true),(2,'two',false)", 2))
			peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			probe := func(id int64, locked bool) {
				q := fmt.Sprintf("SELECT id FROM ONLY fdw_hlock_child WHERE id=%d FOR UPDATE NOWAIT", id)
				fdwHierarchyLockProbe(t, p, b, peer, q, "id", "fdw_hlock_child", id, locked)
			}
			run(fixture.Exec("BEGIN", 0))
			t.Cleanup(func() { fdwRowLockRollback(t, c) })
			run(fixture.Exec("DECLARE fdw_hlock_cursor NO SCROLL CURSOR FOR SELECT id,label,flag,tableoid='fdw_hlock_child'::regclass AS physical FROM fdw_hlock_parent ORDER BY id FOR UPDATE", 0))
			probe(1, false)
			probe(2, false)
			fetch := func(id int64, label string, flag bool) {
				run(fixture.Select("FETCH NEXT FROM fdw_hlock_cursor", []string{"id", "label", "flag", "physical"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{id, label, flag, true}}))
			}
			fetch(1, "one", true)
			probe(1, true)
			probe(2, false)
			fetch(2, "two", false)
			probe(1, true)
			probe(2, true)
			run(fixture.Select("FETCH NEXT FROM fdw_hlock_cursor", []string{"id", "label", "flag", "physical"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, nil))
			run(fixture.Exec("CLOSE fdw_hlock_cursor", 0))
			probe(1, true)
			probe(2, true)
			run(fixture.Exec("COMMIT", 0))
			probe(1, false)
			probe(2, false)
			fdwHierarchyLockEmptyRows(t, p, b, source)
		})
	})
	for _, partition := range []bool{false, true} {
		t.Run(fmt.Sprintf("cursor-foreign/partition=%t", partition), func(t *testing.T) {
			fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, _ *fdwSQLRowLocker, run func(fixture.Step)) {
				fdwHierarchyLockForeignParent(run, partition)
				peer, _ := fixture.LockConnections(t, p.Ctx, source)
				probe := func(id int64, locked bool) {
					q := fmt.Sprintf("SELECT rid FROM fdw_source.data WHERE rid=%d FOR UPDATE NOWAIT", id)
					fdwHierarchyLockProbe(t, p, b, peer, q, "rid", "data", id, locked)
				}
				run(fixture.Exec("BEGIN", 0))
				t.Cleanup(func() { fdwRowLockRollback(t, c) })
				run(fixture.Exec("DECLARE fdw_hlock_cursor NO SCROLL CURSOR FOR SELECT id,label,flag,tableoid='fdw_data'::regclass AS physical FROM fdw_hlock_parent ORDER BY id FOR UPDATE", 0))
				run(fixture.Select("FETCH NEXT FROM fdw_hlock_cursor", []string{"id", "label", "flag", "physical"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), "one", true, true}}))
				probe(1, true)
				// postgres_fdw may lock the rest of its remote fetch batch early.
				// Only fetched rows have a shared incremental locking guarantee;
				// do not invent a remote-row-2-unlocked promise before FETCH 2.
				run(fixture.Select("FETCH NEXT FROM fdw_hlock_cursor", []string{"id", "label", "flag", "physical"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(2), "two", false, true}}))
				probe(2, true)
				run(fixture.Select("FETCH NEXT FROM fdw_hlock_cursor", []string{"id", "label", "flag", "physical"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, nil))
				run(fixture.Exec("CLOSE fdw_hlock_cursor", 0))
				probe(1, true)
				probe(2, true)
				run(fixture.Exec("ROLLBACK", 0))
				probe(1, false)
				probe(2, false)
				fixture.RunStep(t, p.Ctx, source, b.Recorder, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY rid", [][]any{{int64(1), "one", true}, {int64(2), "two", false}}))
			})
		})
	}
}
