package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSequenceLatestIntegration(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					pg, db, record := backend.pg, backend.db, backend.recorder
					a, b := referenceLockConnections(t, p.ctx, db)
					run := func(x referenceExecutor, s referenceStep) {
						t.Helper()
						s.prepared = prepared
						referenceRunStep(t, p.ctx, x, record, s)
					}
					run(a, referenceExec("CREATE SEQUENCE s", 0))
					run(a, referenceExec("CREATE TABLE t(id INT,n BIGINT,c CHAR(4),v VARCHAR(3) DEFAULT 'ok')", 0))
					run(a, referenceExec("INSERT INTO t(id,n,c) VALUES(1,0,'é'),(2,0,'x')", 2))
					run(a, referenceExec("UPDATE t SET n=nextval('s'),c=CAST('é🙂xyz' AS CHAR(4)),v=CAST(CAST(nextval('s') AS TEXT) AS VARCHAR(3)) WHERE id=1", 1))
					initial := referenceSelect("SELECT id,n,c,v FROM t ORDER BY id", []string{"id", "n", "c", "v"}, []string{"INT4", "INT8", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), int64(1), "é🙂xy", "2"}, {int64(2), int64(0), "x   ", "ok"}})
					referenceRunStep(t, p.ctx, a, record, initial)
					if prepared {
						run(a, initial)
					}
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 3)
					for _, commit := range []bool{false, true} {
						tx, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						run(tx, referenceExec("ALTER SEQUENCE s RESTART 100", 0))
						run(tx, referenceExec("UPDATE t SET n=nextval('s') WHERE id=2", 1))
						private := sequenceInt64Step(referenceSelect("SELECT n FROM t WHERE id=2", []string{"n"}, []string{"INT8"}, [][]any{{int64(100)}}))
						referenceRunStep(t, p.ctx, tx, record, private)
						if prepared {
							run(tx, private)
						}
						sequenceProbeValue(t, p.ctx, b, "SELECT n FROM t WHERE id=2", 0)
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						if commit {
							sequenceProbeValue(t, p.ctx, b, "SELECT n FROM t WHERE id=2", 100)
							sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 101)
						} else {
							sequenceProbeValue(t, p.ctx, b, "SELECT n FROM t WHERE id=2", 0)
							sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 4)
						}
					}
					run(a, referenceExec("CREATE TABLE copied(id INT,n BIGINT,c CHAR(4),v VARCHAR(3))", 0))
					run(a, referenceExec("INSERT INTO copied SELECT id,nextval('s'),c,v FROM t ORDER BY id", 2))
					check := referenceSelect("SELECT * FROM copied ORDER BY id", []string{"id", "n", "c", "v"}, []string{"INT4", "INT8", "BPCHAR", "VARCHAR"}, [][]any{{int64(1), int64(102), "é🙂xy", "2"}, {int64(2), int64(103), "x   ", "ok"}})
					referenceRunStep(t, p.ctx, a, record, check)
					if prepared {
						run(a, check)
					}
					sequenceLatestMetadata(t, p, a, pg)
					if !pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						referenceRunStep(t, p.ctx, a, record, check)
						if prepared {
							run(a, check)
						}
						sequenceLatestMetadata(t, p, a, pg)
					}
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 104)
				})
			}
		})
	}
}

func TestReferenceSequenceLatestIntegrationIsolation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			a, b := referenceLockConnections(t, p.ctx, db)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE snapshot_t(v INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO snapshot_t VALUES(1)", 1)
			referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE live_s", 0)
			tx, err := a.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var n int64
			if err = tx.QueryRowContext(p.ctx, "SELECT v FROM snapshot_t").Scan(&n); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			referenceLockExec(t, p.ctx, b, "UPDATE snapshot_t SET v=2", 1)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('live_s')", 1)
			if err = tx.QueryRowContext(p.ctx, "SELECT nextval('live_s')").Scan(&n); err != nil || n != 2 {
				t.Fatal(n, err)
			}
			if err = tx.QueryRowContext(p.ctx, "SELECT v FROM snapshot_t").Scan(&n); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			sequenceProbeValue(t, p.ctx, b, "SELECT v FROM snapshot_t", 2)
			sequenceProbeValue(t, p.ctx, b, "SELECT nextval('live_s')", 3)
			referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE cached_s CACHE 5", 0)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('cached_s')", 1)
			referenceLockExec(t, p.ctx, a, "CREATE TEMP SEQUENCE local_s START 7", 0)
			for _, q := range []string{"SELECT nextval('cached_s')", "SELECT setval('cached_s',0)"} {
				ro, err := a.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				err = ro.QueryRowContext(p.ctx, q).Scan(&n)
				name := "nextval"
				if strings.Contains(q, "setval") {
					name = "setval"
				}
				want := controlsReferenceDiagnostic("25006", "cannot execute "+name+"() in a read-only transaction")
				referenceAssertDiagnostic(t, err, &want)
				if err = ro.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			sequenceProbeValue(t, p.ctx, a, "SELECT currval('cached_s')", 1)
			sequenceProbeValue(t, p.ctx, a, "SELECT last_value FROM cached_s", 5)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('cached_s')", 2)
			ro, err := a.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer ro.Rollback()
			for _, step := range []struct {
				q    string
				want int64
			}{{"SELECT nextval('local_s')", 7}, {"SELECT setval('local_s',9,false)", 9}, {"SELECT nextval('local_s')", 9}} {
				if err = ro.QueryRowContext(p.ctx, step.q).Scan(&n); err != nil || n != step.want {
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
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					pg, db, record := backend.pg, backend.db, backend.recorder
					a, err := db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE default_s START 17", 0)
					for i, expression := range []string{"nextval(NULL)", "setval('default_s',nextval('default_s'),NULL)", "nextval(NULL::regclass)", "lastval()"} {
						name := "default_bad" + strconv.Itoa(i)
						q := "CREATE TABLE " + name + "(v BIGINT DEFAULT " + expression + ")"
						if prepared {
							// Original smoke used ExecContext even in its Prepared
							// outer phase; retain that route beside prepared CREATE.
							sourceName := name + "_context"
							sourceQuery := "CREATE TABLE " + sourceName + "(v BIGINT DEFAULT " + expression + ")"
							source := referenceExec(sourceQuery, 0)
							if !pg {
								source = referenceError(sourceQuery, "0A000")
								source.errorStage = "execute"
							}
							referenceRunStep(t, p.ctx, a, record, source)
							if pg {
								referenceLockExec(t, p.ctx, a, "DROP TABLE "+sourceName, 0)
							}
						}
						if pg {
							s := referenceExec(q, 0)
							s.prepared = prepared
							referenceRunStep(t, p.ctx, a, record, s)
							if expression == "lastval()" {
								want := controlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
								fail := referenceErrorDiagnostic("INSERT INTO "+name+" DEFAULT VALUES", want)
								fail.errorStage = "execute"
								fail.prepared = prepared
								referenceRunStep(t, p.ctx, a, record, fail)
								sequenceProbeValue(t, p.ctx, a, "SELECT nextval('default_s')", 17)
								referenceLockExec(t, p.ctx, a, "INSERT INTO "+name+" DEFAULT VALUES", 1)
								referenceRunStep(t, p.ctx, a, record, referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{int64(17)}}))
							} else {
								referenceLockExec(t, p.ctx, a, "INSERT INTO "+name+" DEFAULT VALUES", 1)
								referenceRunStep(t, p.ctx, a, record, referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"INT8"}, [][]any{{nil}}))
							}
						} else {
							s := referenceError(q, "0A000")
							s.prepared = prepared
							if got := referenceRunStep(t, p.ctx, a, record, s); got.errorStage != "execute" {
								t.Fatal("native capability exclusion occurred before utility execution", got.errorStage)
							}
							missing := controlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
							missing.Position = "15"
							query := referenceErrorDiagnostic("SELECT v FROM "+name, missing)
							query.selectRows = true
							query.errorStage = "execute"
							referenceRunStep(t, p.ctx, a, record, query)

						}
					}
					for i, expression := range []string{"NULL::regclass IS NULL", "'default_s'::regclass IS NULL"} {
						name := "regclass_default" + strconv.Itoa(i)
						if prepared {
							sourceName := name + "_context"
							referenceRunStep(t, p.ctx, a, record, referenceExec("CREATE TABLE "+sourceName+"(v BOOL DEFAULT ("+expression+"))", 0))
							referenceLockExec(t, p.ctx, a, "INSERT INTO "+sourceName+" DEFAULT VALUES", 1)
							referenceRunStep(t, p.ctx, a, record, referenceSelect("SELECT v FROM "+sourceName, []string{"v"}, []string{"BOOL"}, [][]any{{i == 0}}))
							referenceLockExec(t, p.ctx, a, "DROP TABLE "+sourceName, 0)
						}
						s := referenceExec("CREATE TABLE "+name+"(v BOOL DEFAULT ("+expression+"))", 0)
						s.prepared = prepared
						referenceRunStep(t, p.ctx, a, record, s)
						referenceLockExec(t, p.ctx, a, "INSERT INTO "+name+" DEFAULT VALUES", 1)
						referenceRunStep(t, p.ctx, a, record, referenceSelect("SELECT v FROM "+name, []string{"v"}, []string{"BOOL"}, [][]any{{i == 0}}))
					}
					if !pg {
						sequenceProbeValue(t, p.ctx, a, "SELECT nextval('default_s')", 17)
					}
				})
			}
		})
	}
}
