package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceExpressionIndexAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(n INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(0),(2)", 2))
					var before bytes.Buffer
					if !b.pg {
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
					}
					s := referenceImplicitError("CREATE INDEX ix ON t((10/n))", referenceDiagnostic{Code: "22012", Message: "division by zero"})
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
					run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(2)}}))
					if !b.pg {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
							t.Fatal("failed build changed dump", err, after.String())
						}
					}
					run(referenceExec("DELETE FROM t WHERE n=0", 1))
					run(referenceExec("CREATE INDEX ix ON t((10/n))", 0))
					s = referenceImplicitError("INSERT INTO t VALUES(5),(0),(10)", referenceDiagnostic{Code: "22012", Message: "division by zero"})
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					run(referenceSelect("SELECT n,10/n AS k FROM t ORDER BY n", []string{"n", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}}))
					run(referenceExec("INSERT INTO t VALUES(5)", 1))
					run(referenceSelect("SELECT n,10/n AS k FROM t ORDER BY n", []string{"n", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}, {int64(5), int64(2)}}))
				})
			}
		})
	}
}

func TestReferenceExpressionIndexControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			retainedSession, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer retainedSession.Close()
			referenceRunStep(t, p.ctx, retainedSession, b.recorder, referenceExec("CREATE TEMP SEQUENCE kept CACHE 10", 0))
			referenceRunStep(t, p.ctx, retainedSession, b.recorder, referenceSelect("SELECT nextval('kept')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT,v TEXT)", 0), referenceExec("INSERT INTO t VALUES(1,'Alpha'),(2,NULL)", 2), referenceExec("CREATE INDEX ix ON t(lower(v) DESC NULLS LAST)", 0), referenceExec("ALTER TABLE t RENAME v TO renamed", 0)} {
				run(s)
			}
			var dump bytes.Buffer
			if !b.pg {
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(dump.String(), `CREATE INDEX "ix" ON "public"."t" USING btree ((pg_catalog.lower("renamed")) DESC NULLS LAST);`) {
					t.Fatal(dump.String())
				}
				connector, err := stuntgres.NewDriver().OpenConnector("expression-index-destination")
				if err != nil {
					t.Fatal(err)
				}
				destination := sql.OpenDB(connector)
				defer destination.Close()
				if err := stuntgres.Restore(p.ctx, destination, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				if _, err := destination.ExecContext(p.ctx, "INSERT INTO t VALUES(3,'ALPHA')"); err != nil {
					t.Fatal(err)
				}
				fresh := referenceSelect("SELECT id FROM t WHERE lower(renamed)='alpha' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}})
				rows, err := destination.QueryContext(p.ctx, fresh.query)
				deleteReferenceConsume(t, false, rows, err, fresh)
				retained, err := b.db.QueryContext(p.ctx, "SELECT id,lower(renamed) AS folded FROM t ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				metadata, err := retained.ColumnTypes()
				if err != nil || metadata[1].DatabaseTypeName() != "TEXT" {
					t.Fatal(metadata, err)
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				if !retained.Next() {
					t.Fatal("replacement lost eager rows", retained.Err())
				}
				var retainedID int64
				var folded string
				if err := retained.Scan(&retainedID, &folded); err != nil || retainedID != 1 || folded != "alpha" {
					t.Fatal(retainedID, folded, err)
				}
				if err := retained.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				run(referenceExec("DROP TABLE t", 0))
				run(referenceExec("CREATE TABLE t(id INT,renamed TEXT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,'Alpha'),(2,NULL)", 2))
				run(referenceExec("CREATE INDEX ix ON t(lower(renamed) DESC NULLS LAST)", 0))
			}
			referenceRunStep(t, p.ctx, retainedSession, b.recorder, referenceSelect("SELECT nextval('kept')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(referenceExec("INSERT INTO t VALUES(3,'ALPHA')", 1))
			run(referenceSelect("SELECT id FROM t WHERE lower(renamed)='alpha' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
			if !b.pg {
				var before bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"CREATE INDEX bad ON t(lower(missing));")); err == nil {
					t.Fatal("late invalid restore succeeded")
				}
				var after bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
					t.Fatal("late invalid restore replaced destination", err, after.String())
				}
			}
			run(referenceSelect("SELECT id,lower(renamed) AS folded FROM t ORDER BY id", []string{"id", "folded"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), nil}, {int64(3), "alpha"}}))
		})
	}
}
