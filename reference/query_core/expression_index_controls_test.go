package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExpressionIndexAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(n INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(0),(2)", 2))
					var before bytes.Buffer
					if !b.PG {
						if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
					}
					s := fixture.ImplicitError("CREATE INDEX ix ON t((10/n))", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(2)}}))
					if !b.PG {
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
							t.Fatal("failed build changed dump", err, after.String())
						}
					}
					run(fixture.Exec("DELETE FROM t WHERE n=0", 1))
					run(fixture.Exec("CREATE INDEX ix ON t((10/n))", 0))
					s = fixture.ImplicitError("INSERT INTO t VALUES(5),(0),(10)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT n,10/n AS k FROM t ORDER BY n", []string{"n", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}}))
					run(fixture.Exec("INSERT INTO t VALUES(5)", 1))
					run(fixture.Select("SELECT n,10/n AS k FROM t ORDER BY n", []string{"n", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(5)}, {int64(5), int64(2)}}))
				})
			}
		})
	}
}

func TestReferenceExpressionIndexControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			retainedSession, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer retainedSession.Close()
			fixture.RunStep(t, p.Ctx, retainedSession, b.Recorder, fixture.Exec("CREATE TEMP SEQUENCE kept CACHE 10", 0))
			fixture.RunStep(t, p.Ctx, retainedSession, b.Recorder, fixture.Select("SELECT nextval('kept')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0), fixture.Exec("INSERT INTO t VALUES(1,'Alpha'),(2,NULL)", 2), fixture.Exec("CREATE INDEX ix ON t(lower(v) DESC NULLS LAST)", 0), fixture.Exec("ALTER TABLE t RENAME v TO renamed", 0)} {
				run(s)
			}
			var dump bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
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
				if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				if _, err := destination.ExecContext(p.Ctx, "INSERT INTO t VALUES(3,'ALPHA')"); err != nil {
					t.Fatal(err)
				}
				fresh := fixture.Select("SELECT id FROM t WHERE lower(renamed)='alpha' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}})
				rows, err := destination.QueryContext(p.Ctx, fresh.Query)
				fixture.DeleteReferenceConsume(t, false, rows, err, fresh)
				retained, err := b.DB.QueryContext(p.Ctx, "SELECT id,lower(renamed) AS folded FROM t ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				metadata, err := retained.ColumnTypes()
				if err != nil || metadata[1].DatabaseTypeName() != "TEXT" {
					t.Fatal(metadata, err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, nil))
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
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
				run(fixture.Exec("DROP TABLE t", 0))
				run(fixture.Exec("CREATE TABLE t(id INT,renamed TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'Alpha'),(2,NULL)", 2))
				run(fixture.Exec("CREATE INDEX ix ON t(lower(renamed) DESC NULLS LAST)", 0))
			}
			fixture.RunStep(t, p.Ctx, retainedSession, b.Recorder, fixture.Select("SELECT nextval('kept')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Exec("INSERT INTO t VALUES(3,'ALPHA')", 1))
			run(fixture.Select("SELECT id FROM t WHERE lower(renamed)='alpha' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
			if !b.PG {
				var before bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE INDEX bad ON t(lower(missing));")); err == nil {
					t.Fatal("late invalid restore succeeded")
				}
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
					t.Fatal("late invalid restore replaced destination", err, after.String())
				}
			}
			run(fixture.Select("SELECT id,lower(renamed) AS folded FROM t ORDER BY id", []string{"id", "folded"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), nil}, {int64(3), "alpha"}}))
		})
	}
}
