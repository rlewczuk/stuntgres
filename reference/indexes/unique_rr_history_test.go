package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueRepeatableReadKeyHistory(t *testing.T) {
	uniqueReferenceKeyHistory(t, false)
}

func TestReferenceUniqueIndexHistory(t *testing.T) {
	uniqueReferenceKeyHistory(t, true)
}

func uniqueReferenceKeyHistory(t *testing.T, standalone bool) {
	cases := []struct {
		name, typ, seed     string
		updates             []string
		separate, serialize bool
		final               int64
	}{
		{"payload", "INT", "1", []string{"UPDATE t SET v=20"}, false, false, 20},
		{"exact_noop", "INT", "1", []string{"UPDATE t SET k=k"}, false, false, 10},
		{"unique_change", "INT", "1", []string{"UPDATE t SET k=2"}, false, true, 10},
		{"numeric_scale", "NUMERIC", "1.0", []string{"UPDATE t SET k=1.00"}, false, true, 10},
		{"numeric_exact", "NUMERIC", "1.0", []string{"UPDATE t SET k=1.0"}, false, false, 10},
		{"signed_zero", "DOUBLE PRECISION", "'0'", []string{"UPDATE t SET k='-0'"}, false, true, 10},
		{"restored_one_tx", "INT", "1", []string{"UPDATE t SET k=2", "UPDATE t SET k=1"}, false, true, 10},
		{"restored_two_tx", "INT", "1", []string{"UPDATE t SET k=2", "UPDATE t SET k=1"}, true, true, 10},
		{"change_then_payload", "INT", "1", []string{"UPDATE t SET k=2", "UPDATE t SET v=20"}, true, true, 20},
		{"restore_then_payload", "INT", "1", []string{"UPDATE t SET k=2", "UPDATE t SET k=1", "UPDATE t SET v=20"}, true, true, 20},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", c.name, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
						uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k "+c.typ+" CONSTRAINT u UNIQUE,v INT)", "CREATE TABLE t(k "+c.typ+",v INT)", "CREATE UNIQUE INDEX u ON t(k)")
						run(fixture.Exec("INSERT INTO t VALUES("+c.seed+",10)", 1))
						run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
						if !c.separate {
							fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
						}
						for _, update := range c.updates {
							fixture.LockExec(t, p.Ctx, peer, update, 1)
						}
						if !c.separate {
							fixture.LockExec(t, p.Ctx, peer, "COMMIT", 0)
						}
						query := "SELECT v FROM t WHERE k=" + c.seed + " FOR KEY SHARE"
						if c.serialize {
							outcome := fixture.RowQuery(p.Ctx, a, query, prepared)
							if outcome.Rows != nil {
								present := outcome.Rows.Next()
								if outcome.Err == nil {
									outcome.Err = outcome.Rows.Err()
								}
								outcome.Rows.Close()
								if present {
									t.Fatal("key-changing RR locking query returned a retained row")
								}
							}
							if outcome.Stmt != nil {
								outcome.Stmt.Close()
							}
							want := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")
							fixture.AssertDiagnostic(t, outcome.Err, &want)
							aborted := fixture.ImplicitError("INSERT INTO t VALUES(8,80)", fixture.TransactionAborted())
							if prepared {
								aborted = fixture.Prepared(aborted)
							}
							run(aborted)
						} else {
							s := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}})
							if prepared {
								s = fixture.Prepared(s)
							}
							run(s)
						}
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{c.final}}))
						run(fixture.Exec("INSERT INTO t VALUES(9,90)", 1))
						run(fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{c.final}, {int64(90)}}))
					})
				}
			})
		}
	}
}

func TestReferenceUniqueRepeatableReadHistoryControls(t *testing.T) {
	uniqueReferenceHistoryControls(t, false)
}

func TestReferenceUniqueIndexHistoryControls(t *testing.T) {
	uniqueReferenceHistoryControls(t, true)
}

func uniqueReferenceHistoryControls(t *testing.T, standalone bool) {
	for _, mode := range []string{"rolled_back_change", "old_change", "old_restore"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", mode, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
						uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v INT)", "CREATE TABLE t(k INT,v INT)", "CREATE UNIQUE INDEX u ON t(k)")
						run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
						key := int64(1)
						if mode != "rolled_back_change" {
							fixture.LockExec(t, p.Ctx, peer, "UPDATE t SET k=2", 1)
							key = 2
							if mode == "old_restore" {
								fixture.LockExec(t, p.Ctx, peer, "UPDATE t SET k=1", 1)
								key = 1
							}
						}
						run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						run(fixture.Select("SELECT k,v FROM t", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{key, int64(10)}}))
						if mode == "rolled_back_change" {
							fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
							fixture.LockExec(t, p.Ctx, peer, "UPDATE t SET k=2", 1)
							fixture.LockExec(t, p.Ctx, peer, "ROLLBACK", 0)
						}
						fixture.LockExec(t, p.Ctx, peer, "UPDATE t SET v=20", 1)
						s := fixture.Select(fmt.Sprintf("SELECT k,v FROM t WHERE k=%d FOR KEY SHARE", key), []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{key, int64(10)}})
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Select("SELECT k,v FROM t FOR KEY SHARE", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{key, int64(20)}}))
					})
				}
			})
		}
	}
}
