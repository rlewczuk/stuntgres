package reference_test

import (
	"context"
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
				mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
					run := mergeInsertRun(t, p, b, prepared)
					typ := tc.typ
					if domain {
						run(referenceExec("CREATE DOMAIN carrier AS "+typ, 0))
						typ = "carrier"
					}
					run(referenceExec("CREATE TABLE t(id INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(99)", 1))
					run(referenceExec("CREATE TABLE source(id INT,v "+typ+")", 0))
					n := int64(4)
					if tc.name == "bool" {
						n = 3
					}
					if tc.name == "int8" {
						n = 5
					}
					run(referenceExec("INSERT INTO source VALUES"+tc.values, n))
					q := "MERGE INTO t USING source s ON s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
					run(referenceExec(q, int64(len(tc.rows)-1)))
					run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, tc.rows))
				})
			})
		}
	}
}

func TestReferenceMergeInsertConcurrentUnique(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "rollback", true: "commit"}[commit]+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						holder, worker := referenceLockConnections(t, p.ctx, b.db)
						run := func(s referenceStep) { insertSelectRun(t, p.ctx, holder, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE)", 0))
						run(referenceExec("BEGIN", 0))
						defer holder.ExecContext(p.ctx, "ROLLBACK")
						run(referenceExec("INSERT INTO t VALUES(7)", 1))
						q := "MERGE INTO t USING (VALUES(7)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
						var call func(context.Context) relationOutcome
						if prepared {
							stmt, err := worker.PrepareContext(p.ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							call = func(ctx context.Context) relationOutcome {
								r, e := stmt.ExecContext(ctx)
								return relationOutcome{result: r, err: e}
							}
						} else {
							call = func(ctx context.Context) relationOutcome {
								r, e := worker.ExecContext(ctx, q)
								return relationOutcome{result: r, err: e}
							}
						}
						if !b.pg {
							insertSelectRun(t, p.ctx, worker, b.recorder, referenceExec("SET lock_timeout='100ms'", 0))
							o := call(p.ctx)
							d := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							referenceAssertDiagnostic(t, o.err, &d)
							if o.result != nil {
								t.Fatal("blocked command exposed result")
							}
							insertSelectRun(t, p.ctx, worker, b.recorder, referenceExec("SET lock_timeout=0", 0))
						}
						// Pin before publication so native scheduling cannot turn this into a
						// fresh post-commit match. The actual PG worker also proves unique wait.
						insertSelectRun(t, p.ctx, worker, b.recorder, referenceExec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						defer worker.ExecContext(p.ctx, "ROLLBACK")
						insertSelectRun(t, p.ctx, worker, b.recorder, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
						var hp, wp int
						if b.pg {
							hp = referenceLockPID(t, p.ctx, holder)
							wp = referenceLockPID(t, p.ctx, worker)
						}
						join := referenceLockOperation(t, p.ctx, call)
						if b.pg {
							referenceRowWait(t, p.ctx, wp, hp)
						}
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(referenceExec(end, 0))
						o := join()
						if commit {
							d := uniqueReferenceDuplicate("t", "t_pkey", "Key (id)=(7) already exists.")
							referenceAssertDiagnostic(t, o.err, &d)
							if o.result != nil {
								t.Fatal("unique failure exposed result")
							}
							insertSelectRun(t, p.ctx, worker, b.recorder, referenceExec("ROLLBACK", 0))
						} else {
							mergeInsertAffected(t, b, o.result, o.err, 1)
							insertSelectRun(t, p.ctx, worker, b.recorder, referenceExec("COMMIT", 0))
						}
						run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					})
				}
			})
		}
	}
}

func TestReferenceMergeInsertIsolation(t *testing.T) {
	for _, level := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(level, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
				run := mergeInsertRun(t, p, b, false)
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("CREATE TABLE source(id INT)", 0))
				reader, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				insertSelectRun(t, p.ctx, reader, b.recorder, referenceExec("BEGIN ISOLATION LEVEL "+level, 0))
				defer reader.ExecContext(p.ctx, "ROLLBACK")
				insertSelectRun(t, p.ctx, reader, b.recorder, referenceSelect("SELECT id FROM source", []string{"id"}, []string{"INT4"}, [][]any{}))
				q := "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
				// Preparation uses the actual public handle inside the pinned transaction.
				var call func() relationOutcome
				if prepared {
					stmt, err := reader.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					call = func() relationOutcome { r, e := stmt.ExecContext(p.ctx); return relationOutcome{result: r, err: e} }
				} else {
					call = func() relationOutcome {
						r, e := reader.ExecContext(p.ctx, q)
						return relationOutcome{result: r, err: e}
					}
				}
				run(referenceExec("INSERT INTO source VALUES(1)", 1))
				count := int64(0)
				rows := [][]any{}
				if level == "READ COMMITTED" {
					count = 1
					rows = [][]any{{int64(1)}}
				}
				o := call()
				mergeInsertAffected(t, b, o.result, o.err, count)
				insertSelectRun(t, p.ctx, reader, b.recorder, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, rows))
				run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
				insertSelectRun(t, p.ctx, reader, b.recorder, referenceExec("COMMIT", 0))
				run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, rows))
			})
		})
	}
}
