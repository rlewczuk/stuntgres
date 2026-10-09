package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBtreeIndexHeldRowsAfterReset(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) {
						s.nonContext = nonContext
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
					run(referenceExec("CREATE TABLE t(n INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(2),(1)", 2))
					run(referenceExec("CREATE INDEX idx ON t(n)", 0))
					var rows *sql.Rows
					var err error
					if nonContext {
						rows, err = backend.db.Query("SELECT n FROM t ORDER BY n")
					} else {
						rows, err = backend.db.QueryContext(p.ctx, "SELECT n FROM t ORDER BY n")
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil || len(columns) != 1 || columns[0] != "n" {
						t.Fatal(columns, err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
						t.Fatal(types, err)
					}
					if backend.pg {
						run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						run(referenceExec("DROP TABLE t", 0))
					} else if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					run(referenceExec("CREATE TABLE t(s TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES('new')", 1))
					run(referenceExec("CREATE INDEX idx ON t(s)", 0))
					for _, want := range []int64{1, 2} {
						if !rows.Next() {
							t.Fatal("old eager row missing", rows.Err())
						}
						var got int64
						if err := rows.Scan(&got); err != nil || got != want {
							t.Fatal(got, want, err)
						}
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal(rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if types[0].DatabaseTypeName() != "INT4" {
						t.Fatal("retained metadata changed")
					}
					run(referenceSelect("SELECT s FROM t", []string{"s"}, []string{"TEXT"}, [][]any{{"new"}}))
				})
			}
		})
	}
}

func TestReferenceBtreeIndexRestoreRetainedTemporaryCollision(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			schemaReferenceCleanup(t, "portable")
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					retained, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					run := func(s referenceStep) {
						s.nonContext = nonContext
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
					referenceRunStep(t, p.ctx, retained, backend.recorder, referenceExec("CREATE TEMP SEQUENCE retained CACHE 10", 0))
					next := func(want int64) {
						if !nonContext {
							referenceRunStep(t, p.ctx, retained, backend.recorder, referenceSelect("SELECT nextval('retained')", []string{"nextval"}, []string{"INT8"}, [][]any{{want}}))
							return
						}
						var got int64
						if err := retained.QueryRowContext(p.ctx, "SELECT nextval('retained')").Scan(&got); err != nil || got != want {
							t.Fatal(got, want, err)
						}
					}
					next(1)
					const stream = `CREATE SCHEMA portable; CREATE TABLE portable.owner_table(v INT,p TEXT); INSERT INTO portable.owner_table VALUES(7,'old'); CREATE INDEX owner_idx ON portable.owner_table(v int4_ops) INCLUDE(p); CREATE INDEX payload_idx ON portable.owner_table(p COLLATE pg_catalog."POSIX" pg_catalog.text_pattern_ops) INCLUDE(v);`
					if backend.pg {
						if nonContext {
							if _, err := backend.db.ExecContext(p.ctx, stream); err != nil {
								t.Fatal(err)
							}
						} else {
							for _, step := range []referenceStep{
								referenceExec("CREATE SCHEMA portable", 0),
								referenceExec("CREATE TABLE portable.owner_table(v INT,p TEXT)", 0),
								referenceExec("INSERT INTO portable.owner_table VALUES(7,'old')", 1),
								referenceExec("CREATE INDEX owner_idx ON portable.owner_table(v int4_ops) INCLUDE(p)", 0),
								referenceExec(`CREATE INDEX payload_idx ON portable.owner_table(p COLLATE pg_catalog."POSIX" pg_catalog.text_pattern_ops) INCLUDE(v)`, 0),
							} {
								run(step)
							}
						}
					} else if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(stream)); err != nil {
						t.Fatal("indexed Restore with retained TEMP", err)
					}
					defer backend.db.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS portable CASCADE")
					next(2)
					run(referenceExec("INSERT INTO portable.owner_table VALUES(8,'new')", 1))
					run(referenceSelect("SELECT v FROM portable.owner_table ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
					run(referenceSelect("SELECT v,p FROM portable.owner_table ORDER BY v", []string{"v", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "old"}, {int64(8), "new"}}))
					run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='owner_idx' OR relname='payload_idx' ORDER BY relname", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(2)}, {int64(2)}}))
					if !backend.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						const definition = `CREATE INDEX "owner_idx" ON "portable"."owner_table" USING btree ("v" "pg_catalog"."int4_ops" ASC NULLS LAST) INCLUDE ("p");`
						const payload = `CREATE INDEX "payload_idx" ON "portable"."owner_table" USING btree ("p" COLLATE "pg_catalog"."POSIX" "pg_catalog"."text_pattern_ops" ASC NULLS LAST) INCLUDE ("v");`
						if !strings.Contains(dump.String(), definition) || !strings.Contains(dump.String(), payload) || strings.Contains(dump.String(), `CREATE TEMP`) {
							t.Fatal("wrong remapped definition/TEMP serialization", dump.String())
						}
					}
					run(referenceExec("DROP TABLE portable.owner_table", 0))
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='owner_idx'", []string{"relname"}, []string{"NAME"}, nil))
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='payload_idx'", []string{"relname"}, []string{"NAME"}, nil))
					next(3)
					run(referenceExec("DROP SCHEMA portable", 0))
				})
			}
		})
	}
}
