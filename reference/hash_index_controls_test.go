package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceHashIndexControls(t *testing.T) { hashIndexControlsContract(t) }

func hashIndexControlsContract(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			schemaReferenceCleanup(t, "portable")
			p := newReferenceScenario(t)
			var emitted string
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { s.nonContext = nonContext; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					retained, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					referenceRunStep(t, p.ctx, retained, b.recorder, referenceExec("CREATE TEMP SEQUENCE kept CACHE 10", 0))
					referenceRunStep(t, p.ctx, retained, b.recorder, referenceSelect("SELECT nextval('kept')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
					for _, s := range []referenceStep{
						referenceExec("CREATE SCHEMA portable", 0),
						referenceExec("CREATE TABLE portable.t(extra INT,id INT,v VARCHAR(8))", 0),
						referenceExec("INSERT INTO portable.t VALUES(0,1,'Alpha'),(0,2,'ALPHA'),(0,3,NULL)", 3),
						referenceExec("CREATE INDEX hn ON portable.t USING hash(id)", 0),
						referenceExec(`CREATE INDEX hp ON portable.t USING hash(v COLLATE pg_catalog."POSIX" pg_catalog.varchar_pattern_ops)`, 0),
						referenceExec("CREATE INDEX he ON portable.t USING hash(lower(v))", 0),
						referenceExec("ALTER TABLE portable.t RENAME v TO payload", 0),
						referenceExec("ALTER TABLE portable.t DROP extra", 0),
						referenceExec("ALTER TABLE portable.t ALTER id TYPE BIGINT", 0),
						referenceExec("REINDEX INDEX portable.hn", 0),
					} {
						run(s)
					}
					check := func() {
						run(referenceSelect("SELECT id FROM portable.t WHERE id=1::bigint", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}}))
						run(referencePrepared(referenceSelect("SELECT id FROM portable.t WHERE lower(payload)='alpha' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}})))
						run(referenceSelect("SELECT id,payload FROM portable.t ORDER BY id", []string{"id", "payload"}, []string{"INT8", "VARCHAR"}, [][]any{{int64(1), "Alpha"}, {int64(2), "ALPHA"}, {int64(3), nil}}))
					}
					check()
					var held *sql.Stmt
					if nonContext {
						held, err = b.db.Prepare("SELECT id FROM portable.t WHERE id=$1")
					} else {
						held, err = b.db.PrepareContext(p.ctx, "SELECT id FROM portable.t WHERE id=$1")
					}
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					if !b.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						emitted = dump.String()
						for _, definition := range []string{
							`CREATE INDEX "hn" ON "portable"."t" USING hash ("id");`,
							`CREATE INDEX "hp" ON "portable"."t" USING hash ("payload" COLLATE "pg_catalog"."POSIX" "pg_catalog"."varchar_pattern_ops");`,
							`CREATE INDEX "he" ON "portable"."t" USING hash ((pg_catalog.lower("payload")));`,
						} {
							if !strings.Contains(emitted, definition) {
								t.Fatal("missing method-correct Dump definition", definition, emitted)
							}
						}
						if strings.Contains(emitted, " NULLS ") || strings.Contains(emitted, " ASC") || strings.Contains(emitted, "CREATE TEMP") {
							t.Fatal("hash ordering/TEMP leaked into Dump", emitted)
						}
						var rows *sql.Rows
						if nonContext {
							rows, err = p.native.Query("SELECT id,payload FROM portable.t WHERE id=1::bigint")
						} else {
							rows, err = p.native.QueryContext(p.ctx, "SELECT id,payload FROM portable.t WHERE id=1::bigint")
						}
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						metadata, err := rows.ColumnTypes()
						if err != nil || len(metadata) != 2 || metadata[0].DatabaseTypeName() != "INT8" || metadata[1].DatabaseTypeName() != "VARCHAR" {
							t.Fatal(metadata, err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(emitted)); err != nil {
							t.Fatal("self Restore", err)
						}
						check()
						foreign := newReferenceFixture(t, false, true)
						if err := stuntgres.Restore(foreign.ctx, foreign.native, strings.NewReader(emitted)); err != nil {
							t.Fatal("foreign Restore", err)
						}
						referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceExec("INSERT INTO portable.t VALUES(4,'Alpha')", 1))
						referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceSelect("SELECT id FROM portable.t WHERE lower(payload)='alpha' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}}))
						referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceExec("UPDATE portable.t SET payload='Beta' WHERE id=2::bigint", 1))
						referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceSelect("SELECT id FROM portable.t WHERE lower(payload)='alpha' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(4)}}))
						referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceExec("DELETE FROM portable.t WHERE id=4::bigint", 1))
						referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceSelect("SELECT id FROM portable.t WHERE id=4::bigint", []string{"id"}, []string{"INT8"}, nil))
						referenceRunStep(t, foreign.ctx, foreign.memory, foreign.memoryRecord, referenceSelect("SELECT id,payload FROM portable.t ORDER BY id", []string{"id", "payload"}, []string{"INT8", "VARCHAR"}, [][]any{{int64(1), "Alpha"}, {int64(2), "Beta"}, {int64(3), nil}}))
						check() // destination mutation must not mutate the source roots/rows
						var before bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
						err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(emitted+"CREATE INDEX broken ON portable.t USING hash(missing);"))
						want := controlsReferenceDiagnostic("42703", `column "missing" does not exist`)
						referenceAssertDiagnostic(t, err, &want)
						failure := errors.New("hash control reader failed")
						if err := stuntgres.Restore(p.ctx, p.native, catalogControlFailingReader{failure}); !errors.Is(err, failure) {
							t.Fatal(err)
						}
						failure = errors.New("hash control writer failed")
						if err := stuntgres.Dump(p.ctx, p.native, catalogControlFailingWriter{failure}); !errors.Is(err, failure) {
							t.Fatal(err)
						}
						canceled, cancel := context.WithCancel(p.ctx)
						if err := stuntgres.Restore(canceled, p.native, &catalogControlCancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
							cancel()
							t.Fatal(err)
						}
						if err := stuntgres.Reset(canceled, p.native); !errors.Is(err, context.Canceled) {
							cancel()
							t.Fatal(err)
						}
						cancel()
						var after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
							t.Fatal("failed Restore changed catalog/rows/roots", err, after.String())
						}
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
							t.Fatal(err)
						}
						run(referenceSelect("SELECT relname FROM pg_class WHERE relname='hn' OR relname='hp' OR relname='he'", []string{"relname"}, []string{"NAME"}, nil))
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(emitted)); err != nil {
							t.Fatal("Restore after Reset", err)
						}
						if !rows.Next() {
							t.Fatal("retained eager row missing", rows.Err())
						}
						var id int64
						var payload string
						if err := rows.Scan(&id, &payload); err != nil || id != 1 || payload != "Alpha" {
							t.Fatal(id, payload, err)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal(rows.Err())
						}
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
						if metadata[0].DatabaseTypeName() != "INT8" || metadata[1].DatabaseTypeName() != "VARCHAR" {
							t.Fatal("replacement changed held metadata")
						}
					} else {
						if emitted == "" {
							t.Fatal("native stream was not captured")
						}
						run(referenceExec("DROP SCHEMA portable CASCADE", 0))
						if nonContext {
							_, err = b.db.Exec(emitted)
						} else {
							_, err = b.db.ExecContext(p.ctx, emitted)
						}
						if err != nil {
							t.Fatal("real PostgreSQL rejected unchanged native hash Dump", err)
						}
					}
					check()
					var heldRows *sql.Rows
					if nonContext {
						heldRows, err = held.Query(int64(1))
					} else {
						heldRows, err = held.QueryContext(p.ctx, int64(1))
					}
					deleteReferenceConsume(t, b.pg, heldRows, err, referenceSelect("SELECT id FROM portable.t WHERE id=$1", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}}))
					referenceRunStep(t, p.ctx, retained, b.recorder, referenceSelect("SELECT nextval('kept')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
					run(referenceExec("INSERT INTO portable.t VALUES(4,'Alpha')", 1))
					run(referenceSelect("SELECT id FROM portable.t WHERE lower(payload)='alpha' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}}))
					run(referenceSelect("SELECT relname,relnatts FROM pg_class WHERE relname='hn' OR relname='hp' OR relname='he' ORDER BY relname", []string{"relname", "relnatts"}, []string{"NAME", "INT2"}, [][]any{{"he", int64(1)}, {"hn", int64(1)}, {"hp", int64(1)}}))
					run(referenceExec("DROP SCHEMA portable CASCADE", 0))
				})
			}
		})
	}
}
