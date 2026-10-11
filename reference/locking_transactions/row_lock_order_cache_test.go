package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockOrderedCacheFrontier(t *testing.T) {
	for _, source := range []string{"t", "w", "stored"} {
		if source != "stored" && !*fixture.Mode {
			continue
		}
		for _, change := range []string{"UPDATE t SET v=30 WHERE id=1", "DELETE FROM t WHERE id=1"} {
			for _, commit := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					t.Run(source+change+map[bool]string{false: "/rollback", true: "/commit"}[commit]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, backend := range p.Backends() {
							if !backend.PG && source != "stored" {
								continue
							}
							t.Run(backend.Name, func(t *testing.T) {
								a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
								for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20)", "CREATE VIEW inner_view AS SELECT id,v+1 AS val FROM t ORDER BY val,id", "CREATE VIEW w AS SELECT id,val+1 AS shifted FROM inner_view ORDER BY shifted,id", "CREATE VIEW stored AS SELECT id,v FROM t ORDER BY v+1,id FOR UPDATE"} {
									if _, err := a.ExecContext(p.Ctx, q); err != nil {
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
								if backend.PG {
									ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
								}
								holder, err := a.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer holder.Rollback()
								fixture.RowValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false, backend.Recorder.RawBackend), 10)
								fixture.RowAffected(t, p.Ctx, holder, change, false, 1)
								done := fixture.RowAsync(t, p.Ctx, b, query, prepared, true, backend.Recorder.RawBackend)
								if backend.PG {
									fixture.RowWait(t, p.Ctx, bp, ap)
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
								fixture.ViewValues(t, fixture.LockResult(t, done), "k", "INT4", want...)
							})
						}
					})
				}
			}
		}
	}
	if *fixture.Mode {
		referenceRowOrderedRejection(t)
	}
}

func TestReferenceRowLockOrderedCacheVolatile(t *testing.T) {
	for _, source := range []string{"t", "w", "stored", "stored_rows"} {
		if source != "stored" && source != "stored_rows" && !*fixture.Mode {
			continue
		}
		for _, replacement := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					t.Run("volatile/"+source+map[bool]string{false: "/unchanged", true: "/replacement"}[replacement]+map[bool]string{false: "/rollback", true: "/commit"}[commit]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, backend := range p.Backends() {
							if !backend.PG && source != "stored" && source != "stored_rows" {
								continue
							}
							t.Run(backend.Name, func(t *testing.T) {
								a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
								for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)", "CREATE SEQUENCE s", "CREATE VIEW w AS SELECT id,v FROM t", "CREATE VIEW stored AS SELECT id,nextval('s') AS n FROM t ORDER BY n,n FOR UPDATE"} {
									if _, err := a.ExecContext(p.Ctx, q); err != nil {
										t.Fatal(err)
									}
								}
								if _, err := a.ExecContext(p.Ctx, "CREATE VIEW stored_rows AS SELECT id,v FROM t FOR UPDATE"); err != nil {
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
									if backend.PG {
										ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
									}
									holder, err := a.BeginTx(p.Ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer holder.Rollback()
									fixture.RowValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM t FOR UPDATE", false, backend.Recorder.RawBackend), 10)
									fixture.RowAffected(t, p.Ctx, holder, "UPDATE t SET v=30", false, 1)
									done := fixture.RowAsync(t, p.Ctx, b, query, prepared, true, backend.Recorder.RawBackend)
									if backend.PG {
										fixture.RowWait(t, p.Ctx, bp, ap)
									} else if source != "stored_rows" {
										fixture.LimitSequenceCapture(t, p.Ctx, holder)
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
									fixture.ViewValues(t, fixture.LockResult(t, done), "n", "INT8", want)
								} else {
									fixture.ViewValues(t, fixture.RowQuery(p.Ctx, b, query, prepared, backend.Recorder.RawBackend), "n", "INT8", want)
								}
								step := fixture.Select("SELECT currval('s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{want}})
								record := backend.Recorder
								fixture.RunStep(t, p.Ctx, b, record, step)
							})
						}
					})
				}
			}
		}
	}
}

func referenceRowOrderedRejection(t *testing.T) {
	p := fixture.NewScenario(t)
	a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20)", "CREATE TABLE u(id INT)", "INSERT INTO u VALUES(1),(1),(2),(2)", "CREATE VIEW w AS SELECT t.id,t.v+1 AS k FROM t JOIN u USING(id) WHERE t.v<25 ORDER BY k,id"} {
		if _, err := a.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
	holder, err := a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	fixture.RowValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false, fixture.RawPostgres), 10)
	fixture.RowAffected(t, p.Ctx, holder, "UPDATE t SET v=30 WHERE id=1", false, 1)
	done := fixture.RowAsync(t, p.Ctx, b, "SELECT k+1 AS output FROM w ORDER BY output,id FOR UPDATE", true, true, fixture.RawPostgres)
	fixture.RowWait(t, p.Ctx, bp, ap)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	fixture.ViewValues(t, fixture.LockResult(t, done), "output", "INT4", 22, 22)
	for _, q := range []string{"CREATE VIEW stored AS SELECT id,v+1 AS k FROM t ORDER BY k,id FOR UPDATE", "CREATE VIEW grouped AS SELECT count(*) AS n FROM stored WHERE id=1"} {
		fixture.RowAffected(t, p.Ctx, a, q, false, 0)
	}
	holder, err = a.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	fixture.RowValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false, fixture.RawPostgres), 30)
	fixture.RowAffected(t, p.Ctx, holder, "UPDATE t SET v=40 WHERE id=1", false, 1)
	done = fixture.RowAsync(t, p.Ctx, b, "SELECT n FROM grouped", true, true, fixture.RawPostgres)
	fixture.RowWait(t, p.Ctx, bp, ap)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	fixture.ViewValues(t, fixture.LockResult(t, done), "n", "INT8", 1)
}

func TestReferenceRowLockOrderedCacheSnapshot(t *testing.T) {
	for _, clause := range fixture.RowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				for _, backend := range p.Backends() {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					reader, err := b.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer reader.Rollback()
						fixture.ViewValues(t, fixture.RowQuery(p.Ctx, reader, "SELECT val+1 AS k FROM w WHERE id<3 ORDER BY k,id", false, backend.Recorder.RawBackend), "k", "INT4", 12, 22)
						fixture.RowAffected(t, p.Ctx, a, "UPDATE t SET v=30 WHERE id=1", false, 1)
						query := "SELECT val+1 AS k FROM w WHERE id<3 ORDER BY k,id " + clause
						if clause == "FOR KEY SHARE" {
							fixture.ViewValues(t, fixture.RowQuery(p.Ctx, reader, query, prepared, backend.Recorder.RawBackend), "k", "INT4", 12, 22)
						} else {
							step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "40001", Message: "could not serialize access due to concurrent update"})
							step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
							fixture.RunStep(t, p.Ctx, reader, backend.Recorder, step)
						}
					}()
				}
			})
		}
	}
}
