package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionDetachConcurrentPendingAlter(t *testing.T) {
	for _, mode := range []string{"owner", "nested_attach", "nested_detach", "descendant_owner", "rename", "schema", "reopen_rename", "reopen_schema"} {
		for _, held := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "dc_move")
				fixture.RoleReferenceCleanup(t, []string{"dc_actor"})
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					nested := mode == "nested_attach" || mode == "nested_detach" || mode == "descendant_owner"
					if nested {
						for _, q := range []string{
							"CREATE TABLE dc_parent(k INT,payload TEXT,CONSTRAINT dc_positive CHECK(k>0)) PARTITION BY RANGE(k)",
							"CREATE TABLE dc_keep PARTITION OF dc_parent FOR VALUES FROM(1) TO(2)",
							"CREATE TABLE dc_leave PARTITION OF dc_parent FOR VALUES FROM(2) TO(4) PARTITION BY RANGE(k)",
							"CREATE TABLE dc_leaf PARTITION OF dc_leave FOR VALUES FROM(2) TO(3)",
							"CREATE UNIQUE INDEX dc_key ON dc_parent(k)",
							"CREATE UNIQUE INDEX dc_payload ON dc_parent(k,payload)",
							"CREATE TABLE dc_candidate(k INT,payload TEXT,CONSTRAINT dc_positive CHECK(k>0))",
						} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO dc_parent VALUES(1,'keep'),(2,'leave')", 2))
					} else {
						detachConcurrentSetup(run)
					}
					if mode == "owner" || mode == "descendant_owner" {
						run(fixture.Exec("CREATE ROLE dc_actor", 0))
					}
					reopen := mode == "reopen_rename" || mode == "reopen_schema"
					var worker *detachConcurrentWorker
					var reader *sql.Tx
					if reopen {
						conn := detachConcurrentConn(t, p, b)
						pid := detachConcurrentPID(t, p, b, conn)
						var err error
						reader, err = conn.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { reader.Rollback() })
						fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT k FROM ONLY dc_parent", []string{"k"}, []string{"INT4"}, nil))
						worker = detachConcurrentStart(t, p, b, "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY", held)
						fixture.PartitionDetachPending(t, p, b, "dc_parent", "dc_leave", worker.pid, pid, worker.finished)
					} else {
						detachConcurrentPending(t, p, b, held)
					}
					switch mode {
					case "owner", "nested_attach", "nested_detach":
						q := "ALTER TABLE dc_leave OWNER TO dc_actor"
						if mode == "nested_attach" {
							q = "ALTER TABLE dc_leave ATTACH PARTITION dc_candidate FOR VALUES FROM(3) TO(4)"
						}
						if mode == "nested_detach" {
							q = "ALTER TABLE dc_leave DETACH PARTITION dc_leaf"
						}
						c := detachConcurrentConn(t, p, b)
						var executor fixture.Executor = c
						if held {
							executor = partitionDetachHold(t, p, c, q)
						}
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "55000", Message: `cannot alter partition "dc_leave" with an incomplete detach`, Hint: detachConcurrentHint})
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
						run(detachConcurrentGraph("dc_parent", "dc_leave", true, true))
						run(detachConcurrentRows("dc_parent", 1))
						run(detachConcurrentRows("dc_leave", 2))
						if nested {
							run(detachConcurrentGraph("dc_leave", "dc_leaf", true, false))
							run(detachConcurrentGraph("dc_leave", "dc_candidate", false, false))
						}
						detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", held)
					case "descendant_owner":
						// Only the target's immediate pending edge is guarded; a pending ancestor
						// must not impose an invented descendant-wide ALTER ban.
						detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_leaf OWNER TO dc_actor", held)
						run(fixture.Select("SELECT relowner=(SELECT oid FROM pg_roles WHERE rolname='dc_actor') AS changed FROM pg_class WHERE oid='dc_leaf'::regclass", []string{"changed"}, []string{"BOOL"}, [][]any{{true}}))
						run(detachConcurrentGraph("dc_parent", "dc_leave", true, true))
						detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_leaf OWNER TO postgres", held)
						detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", held)
					default:
						child, index, tableName, schema := "dc_renamed", "dc_leave_k_idx", "dc_renamed", "public"
						q := "ALTER TABLE dc_leave RENAME TO dc_renamed"
						if mode == "schema" || mode == "reopen_schema" {
							run(fixture.Exec("CREATE SCHEMA dc_move", 0))
							child, index, tableName, schema = "dc_move.dc_leave", "dc_move.dc_leave_k_idx", "dc_leave", "dc_move"
							q = "ALTER TABLE dc_leave SET SCHEMA dc_move"
						}
						detachConcurrentExecute(t, p, b, b.DB, q, held)
						run(detachConcurrentGraph("dc_parent", child, true, true))
						run(detachConcurrentRows(child, 2))
						run(partitionDetachTopEdges(index, true))
						if reopen {
							run(fixture.Exec("CREATE TABLE dc_leave(k INT,payload TEXT)", 0))
							run(fixture.Exec("INSERT INTO dc_leave VALUES(99,'replacement')", 1))
							if err := reader.Rollback(); err != nil {
								t.Fatal(err)
							}
							detachConcurrentSuccess(t, worker.join())
							run(detachConcurrentRows("dc_leave", 99))
						} else {
							detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_parent DETACH PARTITION "+child+" FINALIZE", held)
						}
						run(detachConcurrentGraph("dc_parent", child, false, false))
						run(partitionDetachTopEdges(index, false))
						run(detachConcurrentRows(child, 2))
						run(detachConcurrentRows("dc_parent", 1))
						run(fixture.ImplicitError("INSERT INTO "+child+" VALUES(9,'outside')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "` + tableName + `" violates check constraint "dc_leave_k_check"`, Detail: "Failing row contains (9, outside).", Schema: schema, Table: tableName, Constraint: "dc_leave_k_check"}))
					}
				})
			})
		}
	}
}
