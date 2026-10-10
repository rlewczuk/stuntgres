package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceSerializableWriteSkew(t *testing.T) {
	for _, mode := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		for _, held := range []bool{false, true} {
			for _, goTx := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					name := mode + map[bool]string{false: "/direct", true: "/held"}[held] + map[bool]string{false: "/sql", true: "/go"}[goTx] + map[bool]string{false: "/Afirst", true: "/Bfirst"}[reverse]
					t.Run(name, func(t *testing.T) {
						serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
							a, c := referenceLockConnections(t, p.ctx, b.db)
							run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
							run(a, referenceExec("CREATE TABLE mytab(class INT, value INT)", 0))
							run(a, referenceExec("INSERT INTO mytab VALUES(1,10),(1,20),(2,100),(2,200)", 4))
							// Prepare before BEGIN: no handle-owned transaction view.
							qa, qb := "SELECT value FROM mytab WHERE class=1 ORDER BY value", "SELECT value FROM mytab WHERE class=2 ORDER BY value"
							var ra, rb referenceExecutor
							if !goTx {
								ra = serializableReferenceHeld(t, p.ctx, a, qa, held)
								rb = serializableReferenceHeld(t, p.ctx, c, qb, held)
							}
							x, ca := serializableReferenceBegin(t, p.ctx, a, mode, goTx)
							y, cb := serializableReferenceBegin(t, p.ctx, c, mode, goTx)
							if goTx {
								ra = serializableReferenceHeld(t, p.ctx, x, qa, held)
								rb = serializableReferenceHeld(t, p.ctx, y, qb, held)
							} else if !held {
								ra, rb = x, y
							}
							totalA, totalB := int64(0), int64(0)
							sa := referenceSelect(qa, []string{"value"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(20)}})
							sb := referenceSelect(qb, []string{"value"}, []string{"INT4"}, [][]any{{int64(100)}, {int64(200)}})
							sa.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
								var v int64
								if err := rows.Scan(&v); err != nil {
									t.Fatal(err)
								}
								totalA += v
							}
							sb.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
								var v int64
								if err := rows.Scan(&v); err != nil {
									t.Fatal(err)
								}
								totalB += v
							}
							run(ra, sa)
							run(rb, sb)
							if totalA != 30 || totalB != 300 {
								t.Fatal("application totals", totalA, totalB)
							}
							// Application-side totals keep this SSI witness independent of aggregates.
							ea := serializableReferenceWrite(t, p.ctx, x, fmt.Sprintf("INSERT INTO mytab VALUES(2,%d)", totalA), held, 1)
							eb := serializableReferenceWrite(t, p.ctx, y, fmt.Sprintf("INSERT INTO mytab VALUES(1,%d)", totalB), held, 1)
							okA, okB := false, false
							commit := func(e error, end func() error) bool {
								if e != nil {
									return false
								}
								if err := end(); err != nil {
									serializableReferenceFailure(t, err)
									return false
								}
								return true
							}
							if reverse {
								okB = commit(eb, cb)
								okA = commit(ea, ca)
							} else {
								okA = commit(ea, ca)
								okB = commit(eb, cb)
							}
							if mode == "REPEATABLE READ" && (!okA || !okB) {
								t.Fatal("RR skew must commit both")
							}
							if mode == "SERIALIZABLE" && okA == okB {
								t.Fatal("expected exactly one skew participant to commit", okA, okB)
							}
							serializableReferenceRollback(p.ctx, x)
							serializableReferenceRollback(p.ctx, y)
							want := [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}}
							if okB {
								want = append(want, []any{int64(1), int64(300)})
							}
							if okA {
								want = append(want, []any{int64(2), int64(30)})
							}
							want = append(want, []any{int64(2), int64(100)}, []any{int64(2), int64(200)})
							run(a, referenceSelect("SELECT class,value FROM mytab ORDER BY class,value", []string{"class", "value"}, []string{"INT4", "INT4"}, want))
						})
					})
				}
			}
		}
	}
}

func TestReferenceSerializableEmptyPredicates(t *testing.T) {
	for _, mode := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		for _, unmatched := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/empty", true: "/unmatched"}[unmatched], func(t *testing.T) {
				serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c := referenceLockConnections(t, p.ctx, b.db)
					run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
					for _, table := range []string{"p", "q"} {
						run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
						if unmatched {
							run(a, referenceExec("INSERT INTO "+table+" VALUES(0)", 1))
						}
					}
					x, ca := serializableReferenceBegin(t, p.ctx, a, mode, false)
					y, cb := serializableReferenceBegin(t, p.ctx, c, mode, false)
					for i, table := range []string{"p", "q"} {
						q := "SELECT v FROM " + table
						if unmatched {
							q += " WHERE v=1"
						}
						owner := x
						if i == 1 {
							owner = y
						}
						run(owner, referenceTransactionRows(false, q))
					}
					ea := serializableReferenceWrite(t, p.ctx, x, "INSERT INTO q VALUES(1)", false, 1)
					eb := serializableReferenceWrite(t, p.ctx, y, "INSERT INTO p VALUES(1)", false, 1)
					ok := []bool{false, false}
					for i, end := range []func() error{ca, cb} {
						e := ea
						if i == 1 {
							e = eb
						}
						if e == nil {
							if err := end(); err != nil {
								serializableReferenceFailure(t, err)
							} else {
								ok[i] = true
							}
						}
					}
					if mode == "SERIALIZABLE" && ok[0] == ok[1] {
						t.Fatal("invalid skew outcome", ok)
					}
					if mode == "REPEATABLE READ" && (!ok[0] || !ok[1]) {
						t.Fatal("RR outcome", ok)
					}
					x.ExecContext(p.ctx, "ROLLBACK")
					y.ExecContext(p.ctx, "ROLLBACK")
					for i, table := range []string{"p", "q"} {
						values := []int64{}
						if unmatched {
							values = append(values, 0)
						}
						if ok[1-i] {
							values = append(values, 1)
						}
						run(a, referenceTransactionRows(false, "SELECT v FROM "+table+" ORDER BY v", values...))
					}
				})
			})
		}
	}
}

func TestReferenceSerializableConflictFree(t *testing.T) {
	for _, lone := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(map[bool]string{false: "independent", true: "lone_edge"}[lone]+map[bool]string{false: "/Afirst", true: "/Bfirst"}[reverse], func(t *testing.T) {
				serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c := referenceLockConnections(t, p.ctx, b.db)
					run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
					run(a, referenceExec("CREATE TABLE a(v INT)", 0))
					run(a, referenceExec("CREATE TABLE b(v INT)", 0))
					run(a, referenceExec("INSERT INTO a VALUES(1)", 1))
					run(a, referenceExec("INSERT INTO b VALUES(2)", 1))
					x, ca := serializableReferenceBegin(t, p.ctx, a, "SERIALIZABLE", false)
					y, cb := serializableReferenceBegin(t, p.ctx, c, "SERIALIZABLE", false)
					run(x, referenceTransactionRows(false, "SELECT v FROM a", 1))
					run(y, referenceTransactionRows(false, "SELECT v FROM b", 2))
					if lone {
						run(y, referenceExec("INSERT INTO a VALUES(10)", 1))
					} else {
						run(x, referenceExec("INSERT INTO a VALUES(10)", 1))
					}
					run(y, referenceExec("INSERT INTO b VALUES(20)", 1))
					if reverse {
						referenceState(t, cb(), "")
						referenceState(t, ca(), "")
					} else {
						referenceState(t, ca(), "")
						referenceState(t, cb(), "")
					}
					run(a, referenceTransactionRows(false, "SELECT v FROM a ORDER BY v", 1, 10))
					run(a, referenceTransactionRows(false, "SELECT v FROM b ORDER BY v", 2, 20))
				})
			})
		}
	}
}
