package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These probes never wait for the missing hierarchy lock. A broken native
// adapter must fail its NOWAIT result, not strand the following oracle case.
func fdwHierarchyLockProbe(t *testing.T, p *fixture.Pair, b fixture.Backend, peer *sql.Conn, query, column, relation string, value int64, locked bool) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
	defer fdwRowLockRollback(t, peer)
	step := fixture.Select(query, []string{column}, []string{"INT4"}, [][]any{{value}})
	if locked {
		step = fixture.ErrorDiagnostic(query, fdwDiagnostic("55P03", fmt.Sprintf("could not obtain lock on row in relation %q", relation)))
		step.SelectRows, step.ErrorStage = true, "execute"
	}
	fixture.RunStep(t, p.Ctx, peer, b.Recorder, step)
}

// Preserve the actual cursor/statement until its explicit close witness, while
// checking independent SQL metadata and values (including an empty result).
func fdwHierarchyLockRows(t *testing.T, outcome fixture.RelationOutcome, want fixture.Step) {
	t.Helper()
	if outcome.Rows != nil {
		t.Cleanup(func() { outcome.Rows.Close() })
	}
	if outcome.Stmt != nil {
		t.Cleanup(func() { outcome.Stmt.Close() })
	}
	if outcome.Err != nil || outcome.Rows == nil {
		t.Fatalf("hierarchy locking query: %v", outcome.Err)
	}
	names, err := outcome.Rows.Columns()
	if err != nil || !reflect.DeepEqual(names, want.Columns) {
		t.Fatalf("hierarchy columns %v, want %v: %v", names, want.Columns, err)
	}
	types, err := outcome.Rows.ColumnTypes()
	if err != nil || len(types) != len(want.Types) {
		t.Fatalf("hierarchy metadata: %v", err)
	}
	for i, name := range want.Types {
		if types[i].DatabaseTypeName() != name {
			t.Fatalf("column %s type %s, want %s", names[i], types[i].DatabaseTypeName(), name)
		}
	}
	got := make([][]any, 0)
	for outcome.Rows.Next() {
		row, destinations := make([]any, len(names)), make([]any, len(names))
		for i := range row {
			destinations[i] = &row[i]
		}
		if err := outcome.Rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i] = fixture.Normalize(row[i])
		}
		got = append(got, row)
	}
	if err := outcome.Rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(got, want.Rows, want.Bag); !ok {
		t.Fatalf("hierarchy rows %#v, want %#v", got, want.Rows)
	}
}

func fdwHierarchyLockEmptySource(t *testing.T, p *fixture.Pair, source *sql.DB) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, source, "CREATE TABLE fdw_source.hlock_empty(rid integer,generation integer,revision integer,id integer,label text,flag boolean)", 0)
}

func fdwHierarchyLockEmptyRows(t *testing.T, p *fixture.Pair, b fixture.Backend, source *sql.DB) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Select("SELECT count(*) FROM fdw_source.hlock_empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func TestReferenceFDWTableHierarchyLocalLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, physical := range []string{"parent", "child"} {
			for _, end := range []string{"ROLLBACK", "COMMIT"} {
				t.Run(fdwRowLockModeName(prepared)+"/"+physical+"/"+end, func(t *testing.T) {
					fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, _ *fdwSQLRowLocker, run func(fixture.Step)) {
						fdwHierarchyLockEmptySource(t, p, source)
						run(fixture.Exec("CREATE TABLE fdw_hlock_parent(id integer)", 0))
						run(fixture.Exec("CREATE FOREIGN TABLE fdw_hlock_empty() INHERITS(fdw_hlock_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'hlock_empty')", 0))
						target := "fdw_hlock_parent"
						if physical == "child" {
							// The parent's id is not at physical child ordinal zero.
							run(fixture.Exec("CREATE TABLE fdw_hlock_child(spare text,id integer)", 0))
							run(fixture.Exec("ALTER TABLE fdw_hlock_child INHERIT fdw_hlock_parent", 0))
							target = "fdw_hlock_child"
						}
						run(fixture.Exec("INSERT INTO "+target+"(id) VALUES(1)", 1))
						run(fixture.Select("SELECT id,tableoid='"+target+"'::regclass AS physical FROM fdw_hlock_parent", []string{"id", "physical"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
						peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
						run(fixture.Exec("BEGIN", 0))
						t.Cleanup(func() { fdwRowLockRollback(t, c) })
						query := "SELECT id FROM fdw_hlock_parent FOR UPDATE"
						outcome := fixture.RowQuery(p.Ctx, c, query, prepared)
						fdwHierarchyLockRows(t, outcome, fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
						probe := "SELECT id FROM ONLY " + target + " WHERE id=1 FOR UPDATE NOWAIT"
						fdwHierarchyLockProbe(t, p, b, peer, probe, "id", target, 1, true)
						fdwRowLockClose(t, outcome)
						fdwHierarchyLockProbe(t, p, b, peer, probe, "id", target, 1, true)
						run(fixture.Exec(end, 0))
						fdwHierarchyLockProbe(t, p, b, peer, probe, "id", target, 1, false)
						fdwHierarchyLockEmptyRows(t, p, b, source)
					})
				})
			}
		}
	}
}

func TestReferenceFDWTableHierarchyPartitionLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, _ *fdwSQLRowLocker, run func(fixture.Step)) {
				fdwHierarchyLockEmptySource(t, p, source)
				run(fixture.Exec("CREATE TABLE fdw_hlock_parts(id integer) PARTITION BY RANGE(id)", 0))
				run(fixture.Exec("CREATE TABLE fdw_hlock_local PARTITION OF fdw_hlock_parts FOR VALUES FROM(0) TO(10)", 0))
				run(fixture.Exec("CREATE FOREIGN TABLE fdw_hlock_remote PARTITION OF fdw_hlock_parts FOR VALUES FROM(10) TO(20) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'hlock_empty')", 0))
				run(fixture.Exec("INSERT INTO fdw_hlock_parts VALUES(1)", 1))
				run(fixture.Select("SELECT id,tableoid='fdw_hlock_local'::regclass AS physical,tableoid<>'fdw_hlock_parts'::regclass AS not_root FROM fdw_hlock_parts", []string{"id", "physical", "not_root"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}}))
				peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run(fixture.Exec("BEGIN", 0))
				t.Cleanup(func() { fdwRowLockRollback(t, c) })
				query := "SELECT id FROM fdw_hlock_parts FOR UPDATE"
				outcome := fixture.RowQuery(p.Ctx, c, query, prepared)
				fdwHierarchyLockRows(t, outcome, fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				probe := "SELECT id FROM ONLY fdw_hlock_local WHERE id=1 FOR UPDATE NOWAIT"
				fdwHierarchyLockProbe(t, p, b, peer, probe, "id", "fdw_hlock_local", 1, true)
				fdwRowLockClose(t, outcome)
				fdwHierarchyLockProbe(t, p, b, peer, probe, "id", "fdw_hlock_local", 1, true)
				run(fixture.Exec("COMMIT", 0))
				fdwHierarchyLockProbe(t, p, b, peer, probe, "id", "fdw_hlock_local", 1, false)
				fdwHierarchyLockEmptyRows(t, p, b, source)
			})
		})
	}
}

// Attach the existing genuine provider table with a deliberately different
// physical column order from its logical parent, for both hierarchy families.
func fdwHierarchyLockForeignParent(run func(fixture.Step), partition bool) {
	if partition {
		run(fixture.Exec("CREATE TABLE fdw_hlock_parent(label text,flag boolean,id integer) PARTITION BY RANGE(id)", 0))
		run(fixture.Exec("CREATE TABLE fdw_hlock_unused PARTITION OF fdw_hlock_parent FOR VALUES FROM(10) TO(20)", 0))
		run(fixture.Exec("ALTER TABLE fdw_hlock_parent ATTACH PARTITION fdw_data FOR VALUES FROM(0) TO(10)", 0))
	} else {
		run(fixture.Exec("CREATE TABLE fdw_hlock_parent(label text,flag boolean,id integer)", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE fdw_data INHERIT fdw_hlock_parent", 0))
	}
}

func TestReferenceFDWTableHierarchyForeignLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, partition := range []bool{false, true} {
			for _, nowait := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/partition=%t/nowait=%t", fdwRowLockModeName(prepared), partition, nowait), func(t *testing.T) {
					fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
						fdwHierarchyLockForeignParent(run, partition)
						run(fixture.Select("SELECT id,tableoid='fdw_data'::regclass AS physical,tableoid<>'fdw_hlock_parent'::regclass AS not_root FROM fdw_hlock_parent ORDER BY id", []string{"id", "physical", "not_root"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), true, true}}))
						peer, _ := fixture.LockConnections(t, p.Ctx, source)
						requested := false
						policy := stuntgres.ForeignLockBlock
						if nowait {
							policy = stuntgres.ForeignLockNoWait
						}
						if !b.PG {
							locker.markRequestHook = func(request stuntgres.ForeignRowMarkRequest) {
								if request.Strength == stuntgres.ForeignLockUpdate && request.WaitPolicy == policy {
									requested = true
								} else if request.Strength != stuntgres.ForeignLockNone {
									t.Errorf("hierarchy mark strength/wait: %v/%v, want UPDATE/%v", request.Strength, request.WaitPolicy, policy)
								}
							}
							defer func() { locker.markRequestHook = nil }()
						}
						run(fixture.Exec("BEGIN", 0))
						t.Cleanup(func() { fdwRowLockRollback(t, c) })
						query := "SELECT id,label,flag FROM fdw_hlock_parent WHERE id=1 FOR UPDATE"
						if nowait {
							// This is a successful parameterized policy request, not an
							// assertion that postgres_fdw pushes remote NOWAIT down.
							step := fdwRows("SELECT id,label,flag FROM fdw_hlock_parent WHERE id=$1 FOR UPDATE NOWAIT", [][]any{{int64(1), "one", true}})
							step.Args, step.Prepared = []any{int64(1)}, prepared
							run(step)
						} else {
							outcome := fdwRowLockRead(t, p.Ctx, c, query, prepared, [][]any{{int64(1), "one", true}})
							fdwHierarchyLockProbe(t, p, b, peer, "SELECT rid FROM fdw_source.data WHERE rid=1 FOR UPDATE NOWAIT", "rid", "data", 1, true)
							fdwRowLockClose(t, outcome)
						}
						fdwHierarchyLockProbe(t, p, b, peer, "SELECT rid FROM fdw_source.data WHERE rid=1 FOR UPDATE NOWAIT", "rid", "data", 1, true)
						if !b.PG && !requested {
							t.Fatal("true foreign child did not receive the inherited UPDATE/wait policy")
						}
						end := "ROLLBACK"
						if nowait {
							end = "COMMIT"
						}
						run(fixture.Exec(end, 0))
						fdwHierarchyLockProbe(t, p, b, peer, "SELECT rid FROM fdw_source.data WHERE rid=1 FOR UPDATE NOWAIT", "rid", "data", 1, false)
						fixture.RunStep(t, p.Ctx, source, b.Recorder, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY rid", [][]any{{int64(1), "one", true}, {int64(2), "two", false}}))
					})
				})
			}
		}
	}
}

// All blocking rechecks use fixture.LockOperation; their NOWAIT preflight is
// deliberately separate so a missing native grant never waits for admission.
func fdwHierarchyLockWait(t *testing.T, p *fixture.Pair, b fixture.Backend, waiter, holder int, finished <-chan struct{}) {
	t.Helper()
	if b.PG {
		ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
		defer cancel()
		fixture.RowWait(t, ctx, waiter, holder)
	} else {
		fixture.RelationLockPending(t, p, b, 0, 0, 0, "", finished)
	}
}
