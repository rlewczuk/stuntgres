//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRowLockOrderedCacheFrontier(t *testing.T) {
	for _, source := range []string{"t", "w", "stored"} {
		for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
			for _, commit := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					t.Run(source+change+map[bool]string{false: "/rollback", true: "/commit"}[commit]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := newReferencePair(t)
						for backend, db := range []*sql.DB{p.postgres, p.memory} {
							if backend == 1 && source != "stored" {
								continue
							}
							t.Run([]string{"postgres", "memory"}[backend], func(t *testing.T) {
								a, b := referenceLockConnections(t, p.ctx, db)
								for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20)", "CREATE VIEW inner_view AS SELECT id,v+1 AS val FROM t ORDER BY val,id", "CREATE VIEW w AS SELECT id,val+1 AS shifted FROM inner_view ORDER BY shifted,id", "CREATE VIEW stored AS SELECT id,v FROM t ORDER BY v+1,id FOR UPDATE"} {
									if _, err := a.ExecContext(p.ctx, q); err != nil {
										t.Fatal(err)
									}
								}
								expression := "v+1"
								want := []int64{11, 21}
								if source == "w" {
									expression = "shifted+1"
									want = []int64{13, 23}
								}
								query := "SELECT " + expression + " AS k FROM " + source + " ORDER BY k,id"
								if source != "stored" {
									query += " FOR UPDATE"
								}
								ap, bp := 0, 0
								if backend == 0 {
									ap, bp = referenceRowPIDs(t, p.ctx, a, b)
								}
								holder, err := a.BeginTx(p.ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer holder.Rollback()
								referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false), 10)
								referenceRowAffected(t, p.ctx, holder, change, false, 1)
								done := referenceRowAsync(t, p.ctx, b, query, prepared, true)
								if backend == 0 {
									referenceRowWait(t, p.ctx, bp, ap)
								}
								if commit {
									err = holder.Commit()
									if change[0] == 'D' {
										want = want[1:]
									} else {
										want[0] += 20
										if source != "t" {
											// Local stored marks precede the consuming upper Sort.
											// The native exact queue witness lives in engine tests;
											// inherited-only w retains its separately qualified difference.
											want[0], want[1] = want[1], want[0]
										}
									}
								} else {
									err = holder.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
								referenceViewValues(t, referenceLockResult(t, done), "k", "INT4", want...)
							})
						}
					})
				}
			}
		}
	}
	for _, source := range []string{"t", "w", "stored", "stored_rows"} {
		for _, replacement := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					t.Run("volatile/"+source+map[bool]string{false: "/unchanged", true: "/replacement"}[replacement]+map[bool]string{false: "/rollback", true: "/commit"}[commit]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := newReferencePair(t)
						for backend, db := range []*sql.DB{p.postgres, p.memory} {
							if backend == 1 && source != "stored" && source != "stored_rows" {
								continue
							}
							t.Run([]string{"postgres", "memory"}[backend], func(t *testing.T) {
								a, b := referenceLockConnections(t, p.ctx, db)
								for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)", "CREATE SEQUENCE s", "CREATE VIEW w AS SELECT id,v FROM t", "CREATE VIEW stored AS SELECT id,nextval('s') AS n FROM t ORDER BY n,n FOR UPDATE"} {
									if _, err := a.ExecContext(p.ctx, q); err != nil {
										t.Fatal(err)
									}
								}
								if _, err := a.ExecContext(p.ctx, "CREATE VIEW stored_rows AS SELECT id,v FROM t FOR UPDATE"); err != nil {
									t.Fatal(err)
								}
								query := "SELECT nextval('s') AS n FROM " + source + " ORDER BY n,n FOR UPDATE"
								if source == "stored" {
									query = "SELECT n FROM stored"
								}
								if source == "stored_rows" {
									query = "SELECT nextval('s') AS n FROM stored_rows ORDER BY n,n"
								} // Lower stored marks precede the outer volatile Sort on both backends.
								want := int64(1)
								if replacement {
									ap, bp := 0, 0
									if backend == 0 {
										ap, bp = referenceRowPIDs(t, p.ctx, a, b)
									}
									holder, err := a.BeginTx(p.ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer holder.Rollback()
									referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t FOR UPDATE", false), 10)
									referenceRowAffected(t, p.ctx, holder, "UPDATE t SET v=30", false, 1)
									done := referenceRowAsync(t, p.ctx, b, query, prepared, true)
									if backend == 0 {
										referenceRowWait(t, p.ctx, bp, ap)
									} else if source != "stored_rows" {
										referenceLimitSequenceCapture(t, p.ctx, holder)
									}
									if commit {
										err = holder.Commit()
										if source != "stored_rows" {
											want = 2
										}
									} else {
										err = holder.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
									referenceViewValues(t, referenceLockResult(t, done), "n", "INT8", want)
								} else {
									referenceViewValues(t, referenceRowQuery(p.ctx, b, query, prepared), "n", "INT8", want)
								}
								step := referenceSelect("SELECT currval('s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{want}})
								record := p.postgresRecord
								if backend == 1 {
									record = p.memoryRecord
								}
								referenceRunStep(t, p.ctx, b, record, step)
							})
						}
					})
				}
			}
		}
	}
}

func TestReferenceRowLockOrderedCacheRejection(t *testing.T) {
	p := newReferencePair(t)
	a, b := referenceLockConnections(t, p.ctx, p.postgres)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20)", "CREATE TABLE u(id INT)", "INSERT INTO u VALUES(1),(1),(2),(2)", "CREATE VIEW w AS SELECT t.id,t.v+1 AS k FROM t JOIN u USING(id) WHERE t.v<25 ORDER BY k,id"} {
		if _, err := a.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	ap, bp := referenceRowPIDs(t, p.ctx, a, b)
	holder, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false), 10)
	referenceRowAffected(t, p.ctx, holder, "UPDATE t SET v=30 WHERE id=1", false, 1)
	done := referenceRowAsync(t, p.ctx, b, "SELECT k+1 AS output FROM w ORDER BY output,id FOR UPDATE", true, true)
	referenceRowWait(t, p.ctx, bp, ap)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	referenceViewValues(t, referenceLockResult(t, done), "output", "INT4", 22, 22)
	for _, q := range []string{"CREATE VIEW stored AS SELECT id,v+1 AS k FROM t ORDER BY k,id FOR UPDATE", "CREATE VIEW grouped AS SELECT count(*) AS n FROM stored WHERE id=1"} {
		referenceRowAffected(t, p.ctx, a, q, false, 0)
	}
	holder, err = a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false), 30)
	referenceRowAffected(t, p.ctx, holder, "UPDATE t SET v=40 WHERE id=1", false, 1)
	done = referenceRowAsync(t, p.ctx, b, "SELECT n FROM grouped", true, true)
	referenceRowWait(t, p.ctx, bp, ap)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	referenceViewValues(t, referenceLockResult(t, done), "n", "INT8", 1)
}

func TestReferenceRowLockOrderedCacheSnapshot(t *testing.T) {
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				for _, backend := range []struct {
					db     *sql.DB
					record *referenceRecorder
				}{{p.postgres, p.postgresRecord}, {p.memory, p.memoryRecord}} {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					reader, err := b.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer reader.Rollback()
						referenceViewValues(t, referenceRowQuery(p.ctx, reader, "SELECT val+1 AS k FROM w WHERE id<3 ORDER BY k,id", false), "k", "INT4", 12, 22)
						referenceRowAffected(t, p.ctx, a, "UPDATE t SET v=30 WHERE id=1", false, 1)
						query := "SELECT val+1 AS k FROM w WHERE id<3 ORDER BY k,id " + clause
						if clause == "FOR KEY SHARE" {
							referenceViewValues(t, referenceRowQuery(p.ctx, reader, query, prepared), "k", "INT4", 12, 22)
						} else {
							step := referenceImplicitError(query, referenceDiagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
							step.selectRows, step.prepared, step.errorStage = true, prepared, "execute"
							referenceRunStep(t, p.ctx, reader, backend.record, step)
						}
					}()
				}
			})
		}
	}
}
