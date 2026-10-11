package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceLatestIntegration(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					pg, db, record := backend.PG, backend.DB, backend.Recorder
					a, b := fixture.LockConnections(t, p.Ctx, db)
					run := func(x fixture.Executor, s fixture.Step) {
						t.Helper()
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, x, record, s)
					}
					run(a, fixture.Exec("CREATE SEQUENCE s", 0))
					run(a, fixture.Exec("CREATE TABLE t(id INT,n BIGINT,c CHAR(4),v VARCHAR(3) DEFAULT 'ok')", 0))
					run(a, fixture.Exec("INSERT INTO t(id,n,c) VALUES(1,0,'é'),(2,0,'x')", 2))
					run(a, fixture.Exec("UPDATE t SET n=nextval('s'),c=CAST('é🙂xyz' AS CHAR(4)),v=CAST(CAST(nextval('s') AS TEXT) AS VARCHAR(3)) WHERE id=1", 1))
					initial := fixture.Select("SELECT id,n,c,v FROM t ORDER BY id", []string{"id", "n", "c", "v"}, []string{"INT4", "INT8", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), int64(1), "é🙂xy", "2"}, {int64(2), int64(0), "x   ", "ok"}})
					fixture.RunStep(t, p.Ctx, a, record, initial)
					if prepared {
						run(a, initial)
					}
					fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 3)
					for _, commit := range []bool{false, true} {
						tx, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						run(tx, fixture.Exec("ALTER SEQUENCE s RESTART 100", 0))
						run(tx, fixture.Exec("UPDATE t SET n=nextval('s') WHERE id=2", 1))
						private := fixture.SequenceInt64Step(fixture.Select("SELECT n FROM t WHERE id=2", []string{"n"}, []string{"INT8"}, [][]any{{int64(100)}}))
						fixture.RunStep(t, p.Ctx, tx, record, private)
						if prepared {
							run(tx, private)
						}
						fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT n FROM t WHERE id=2", 0)
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						if commit {
							fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT n FROM t WHERE id=2", 100)
							fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 101)
						} else {
							fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT n FROM t WHERE id=2", 0)
							fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 4)
						}
					}
					run(a, fixture.Exec("CREATE TABLE copied(id INT,n BIGINT,c CHAR(4),v VARCHAR(3))", 0))
					run(a, fixture.Exec("INSERT INTO copied SELECT id,nextval('s'),c,v FROM t ORDER BY id", 2))
					check := fixture.Select("SELECT * FROM copied ORDER BY id", []string{"id", "n", "c", "v"}, []string{"INT4", "INT8", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), int64(102), "é🙂xy", "2"}, {int64(2), int64(103), "x   ", "ok"}})
					fixture.RunStep(t, p.Ctx, a, record, check)
					if prepared {
						run(a, check)
					}
					sequenceLatestMetadata(t, p, a, pg)
					if !pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						fixture.RunStep(t, p.Ctx, a, record, check)
						if prepared {
							run(a, check)
						}
						sequenceLatestMetadata(t, p, a, pg)
					}
					fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 104)
				})
			}
		})
	}
}

func TestReferenceSequenceLatestIntegrationIsolation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			a, b := fixture.LockConnections(t, p.Ctx, db)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE snapshot_t(v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO snapshot_t VALUES(1)", 1)
			fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE live_s", 0)
			tx, err := a.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var n int64
			if err = tx.QueryRowContext(p.Ctx, "SELECT v FROM snapshot_t").Scan(&n); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			fixture.LockExec(t, p.Ctx, b, "UPDATE snapshot_t SET v=2", 1)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('live_s')", 1)
			if err = tx.QueryRowContext(p.Ctx, "SELECT nextval('live_s')").Scan(&n); err != nil || n != 2 {
				t.Fatal(n, err)
			}
			if err = tx.QueryRowContext(p.Ctx, "SELECT v FROM snapshot_t").Scan(&n); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT v FROM snapshot_t", 2)
			fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('live_s')", 3)
			fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE cached_s CACHE 5", 0)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('cached_s')", 1)
			fixture.LockExec(t, p.Ctx, a, "CREATE TEMP SEQUENCE local_s START 7", 0)
			for _, q := range []string{"SELECT nextval('cached_s')", "SELECT setval('cached_s',0)"} {
				ro, err := a.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				err = ro.QueryRowContext(p.Ctx, q).Scan(&n)
				name := "nextval"
				if strings.Contains(q, "setval") {
					name = "setval"
				}
				want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+name+"() in a read-only transaction")
				fixture.AssertDiagnostic(t, err, &want)
				if err = ro.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('cached_s')", 1)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT last_value FROM cached_s", 5)
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('cached_s')", 2)
			ro, err := a.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer ro.Rollback()
			for _, step := range []struct {
				q    string
				want int64
			}{{"SELECT nextval('local_s')", 7}, {"SELECT setval('local_s',9,false)", 9}, {"SELECT nextval('local_s')", 9}} {
				if err = ro.QueryRowContext(p.Ctx, step.q).Scan(&n); err != nil || n != step.want {
					t.Fatal(step.q, n, err)
				}
			}
			if err = ro.Commit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceSequenceLatestIntegrationDefaultBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					pg, db, record := backend.PG, backend.DB, backend.Recorder
					a, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE default_s START 17", 0)
					for i, expression := range []string{"nextval(NULL)", "setval('default_s',nextval('default_s'),NULL)", "nextval(NULL::regclass)", "lastval()"} {
						name := "default_bad" + strconv.Itoa(i)
						q := "CREATE TABLE " + name + "(v BIGINT DEFAULT " + expression + ")"
						if prepared {
							// Original smoke used ExecContext even in its Prepared
							// outer phase; retain that route beside prepared CREATE.
							sourceName := name + "_context"
							sourceQuery := "CREATE TABLE " + sourceName + "(v BIGINT DEFAULT " + expression + ")"
							source := fixture.Exec(sourceQuery, 0)
							if !pg {
								source = fixture.Error(sourceQuery, "0A000")
								source.ErrorStage = "execute"
							}
							fixture.RunStep(t, p.Ctx, a, record, source)
							if pg {
								fixture.LockExec(t, p.Ctx, a, "DROP TABLE "+sourceName, 0)
							}
						}
						if pg {
							s := fixture.Exec(q, 0)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, a, record, s)
							if expression == "lastval()" {
								want := fixture.ControlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
								fail := fixture.ErrorDiagnostic("INSERT INTO "+name+" DEFAULT VALUES", want)
								fail.ErrorStage = "execute"
								fail.Prepared = prepared
								fixture.RunStep(t, p.Ctx, a, record, fail)
								fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('default_s')", 17)
								fixture.LockExec(t, p.Ctx, a, "INSERT INTO "+name+" DEFAULT VALUES", 1)
								fixture.RunStep(t, p.Ctx, a, record, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{int64(17)}}))
							} else {
								fixture.LockExec(t, p.Ctx, a, "INSERT INTO "+name+" DEFAULT VALUES", 1)
								fixture.RunStep(t, p.Ctx, a, record, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{nil}}))
							}
						} else {
							s := fixture.Error(q, "0A000")
							s.Prepared = prepared
							if got := fixture.RunStep(t, p.Ctx, a, record, s); got.ErrorStage != "execute" {
								t.Fatal("native capability exclusion occurred before utility execution", got.ErrorStage)
							}
							missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
							missing.Position = "15"
							query := fixture.ErrorDiagnostic("SELECT v FROM "+name, missing)
							query.SelectRows = true
							query.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, a, record, query)

						}
					}
					for i, expression := range []string{"NULL::regclass IS NULL", "'default_s'::regclass IS NULL"} {
						name := "regclass_default" + strconv.Itoa(i)
						if prepared {
							sourceName := name + "_context"
							fixture.RunStep(t, p.Ctx, a, record, fixture.Exec("CREATE TABLE "+sourceName+"(v BOOL DEFAULT ("+expression+"))", 0))
							fixture.LockExec(t, p.Ctx, a, "INSERT INTO "+sourceName+" DEFAULT VALUES", 1)
							fixture.RunStep(t, p.Ctx, a, record, fixture.Select("SELECT v FROM "+sourceName, []string{"v"}, []string{"BOOL"}, [][]any{{i == 0}}))
							fixture.LockExec(t, p.Ctx, a, "DROP TABLE "+sourceName, 0)
						}
						s := fixture.Exec("CREATE TABLE "+name+"(v BOOL DEFAULT ("+expression+"))", 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, a, record, s)
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO "+name+" DEFAULT VALUES", 1)
						fixture.RunStep(t, p.Ctx, a, record, fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{"BOOL"}, [][]any{{i == 0}}))
						fixture.RunStep(t, p.Ctx, a, record, fixture.Select("SELECT count(*) AS n FROM pg_attrdef WHERE adrelid='"+name+"'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
					if !pg {
						fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('default_s')", 17)
					}
				})
			}
		})
	}
}
