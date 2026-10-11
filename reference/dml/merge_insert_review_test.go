package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertOnCarriers(t *testing.T) {
	for _, tc := range []struct {
		name, typ, values string
		rows              [][]any
	}{
		{"bool", "BOOLEAN", "(1,FALSE),(2,TRUE),(3,NULL)", [][]any{{int64(1)}, {int64(3)}, {int64(99)}}},
		{"int2", "SMALLINT", "(1,0),(2,-32768),(3,32767),(4,NULL)", [][]any{{int64(1)}, {int64(4)}, {int64(99)}}},
		{"int4", "INTEGER", "(1,0),(2,-1),(3,2147483647),(4,NULL)", [][]any{{int64(1)}, {int64(4)}, {int64(99)}}},
		{"int8", "BIGINT", "(1,0),(2,-9223372036854775808),(3,4294967296),(4,9223372036854775807),(5,NULL)", [][]any{{int64(1)}, {int64(5)}, {int64(99)}}},
		{"text", "TEXT", "(1,''),(2,'0'),(3,'false'),(4,NULL)", [][]any{{int64(4)}, {int64(99)}}},
	} {
		for _, domain := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/base", true: "/domain"}[domain], func(t *testing.T) {
				mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					run := fixture.MergeInsertRun(t, p, b, prepared)
					typ := tc.typ
					if domain {
						run(fixture.Exec("CREATE DOMAIN carrier AS "+typ, 0))
						typ = "carrier"
					}
					run(fixture.Exec("CREATE TABLE t(id INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(99)", 1))
					run(fixture.Exec("CREATE TABLE source(id INT,v "+typ+")", 0))
					n := int64(4)
					if tc.name == "bool" {
						n = 3
					}
					if tc.name == "int8" {
						n = 5
					}
					run(fixture.Exec("INSERT INTO source VALUES"+tc.values, n))
					q := "MERGE INTO t USING source s ON s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
					run(fixture.Exec(q, int64(len(tc.rows)-1)))
					run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, tc.rows))
				})
			})
		}
	}
}

func TestReferenceMergeInsertConcurrentUnique(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "rollback", true: "commit"}[commit]+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, holder, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE)", 0))
						run(fixture.Exec("BEGIN", 0))
						defer holder.ExecContext(p.Ctx, "ROLLBACK")
						run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
						q := "MERGE INTO t USING (VALUES(7)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
						var call func(context.Context) fixture.RelationOutcome
						if prepared {
							stmt, err := worker.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							call = func(ctx context.Context) fixture.RelationOutcome {
								r, e := stmt.ExecContext(ctx)
								return fixture.RelationOutcome{Result: r, Err: e}
							}
						} else {
							call = func(ctx context.Context) fixture.RelationOutcome {
								r, e := worker.ExecContext(ctx, q)
								return fixture.RelationOutcome{Result: r, Err: e}
							}
						}
						if !b.PG {
							fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, fixture.Exec("SET lock_timeout='100ms'", 0))
							o := call(p.Ctx)
							d := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							fixture.AssertDiagnostic(t, o.Err, &d)
							if o.Result != nil {
								t.Fatal("blocked command exposed result")
							}
							fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, fixture.Exec("SET lock_timeout=0", 0))
						}
						// Pin before publication so native scheduling cannot turn this into a
						// fresh post-commit match. The actual PG worker also proves unique wait.
						fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						defer worker.ExecContext(p.Ctx, "ROLLBACK")
						fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
						var hp, wp int
						if b.PG {
							hp = fixture.LockPID(t, p.Ctx, holder)
							wp = fixture.LockPID(t, p.Ctx, worker)
						}
						join := fixture.LockOperation(t, p.Ctx, call)
						if b.PG {
							fixture.RowWait(t, p.Ctx, wp, hp)
						}
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(fixture.Exec(end, 0))
						o := join()
						if commit {
							d := fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (id)=(7) already exists.")
							fixture.AssertDiagnostic(t, o.Err, &d)
							if o.Result != nil {
								t.Fatal("unique failure exposed result")
							}
							fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, fixture.Exec("ROLLBACK", 0))
						} else {
							mergeInsertAffected(t, b, o.Result, o.Err, 1)
							fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, fixture.Exec("COMMIT", 0))
						}
						run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					})
				}
			})
		}
	}
}

func TestReferenceMergeInsertIsolation(t *testing.T) {
	for _, level := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(level, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeInsertRun(t, p, b, false)
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("CREATE TABLE source(id INT)", 0))
				reader, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				fixture.InsertSelectRun(t, p.Ctx, reader, b.Recorder, fixture.Exec("BEGIN ISOLATION LEVEL "+level, 0))
				defer reader.ExecContext(p.Ctx, "ROLLBACK")
				fixture.InsertSelectRun(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT id FROM source", []string{"id"}, []string{"INT4"}, [][]any{}))
				q := "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
				// Preparation uses the actual public handle inside the pinned transaction.
				var call func() fixture.RelationOutcome
				if prepared {
					stmt, err := reader.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					call = func() fixture.RelationOutcome {
						r, e := stmt.ExecContext(p.Ctx)
						return fixture.RelationOutcome{Result: r, Err: e}
					}
				} else {
					call = func() fixture.RelationOutcome {
						r, e := reader.ExecContext(p.Ctx, q)
						return fixture.RelationOutcome{Result: r, Err: e}
					}
				}
				run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
				count := int64(0)
				rows := [][]any{}
				if level == "READ COMMITTED" {
					count = 1
					rows = [][]any{{int64(1)}}
				}
				o := call()
				mergeInsertAffected(t, b, o.Result, o.Err, count)
				fixture.InsertSelectRun(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, rows))
				run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
				fixture.InsertSelectRun(t, p.Ctx, reader, b.Recorder, fixture.Exec("COMMIT", 0))
				run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, rows))
			})
		})
	}
}
