package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashIndexControls(t *testing.T) { hashIndexControlsContract(t) }

func hashIndexControlsContract(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "portable")
			p := fixture.NewScenario(t)
			var emitted string
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { s.NonContext = nonContext; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					retained, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					fixture.RunStep(t, p.Ctx, retained, b.Recorder, fixture.Exec("CREATE TEMP SEQUENCE kept CACHE 10", 0))
					fixture.RunStep(t, p.Ctx, retained, b.Recorder, fixture.Select("SELECT nextval('kept')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
					for _, s := range []fixture.Step{
						fixture.Exec("CREATE SCHEMA portable", 0),
						fixture.Exec("CREATE TABLE portable.t(extra INT,id INT,v VARCHAR(8))", 0),
						fixture.Exec("INSERT INTO portable.t VALUES(0,1,'Alpha'),(0,2,'ALPHA'),(0,3,NULL)", 3),
						fixture.Exec("CREATE INDEX hn ON portable.t USING hash(id)", 0),
						fixture.Exec(`CREATE INDEX hp ON portable.t USING hash(v COLLATE pg_catalog."POSIX" pg_catalog.varchar_pattern_ops)`, 0),
						fixture.Exec("CREATE INDEX he ON portable.t USING hash(lower(v))", 0),
						fixture.Exec("ALTER TABLE portable.t RENAME v TO payload", 0),
						fixture.Exec("ALTER TABLE portable.t DROP extra", 0),
						fixture.Exec("ALTER TABLE portable.t ALTER id TYPE BIGINT", 0),
						fixture.Exec("REINDEX INDEX portable.hn", 0),
					} {
						run(s)
					}
					check := func() {
						run(fixture.Select("SELECT id FROM portable.t WHERE id=1::bigint", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}}))
						run(fixture.Prepared(fixture.Select("SELECT id FROM portable.t WHERE lower(payload)='alpha' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}})))
						run(fixture.Select("SELECT id,payload FROM portable.t ORDER BY id", []string{"id", "payload"}, []string{"INT8", "VARCHAR"}, [][]any{{int64(1), "Alpha"}, {int64(2), "ALPHA"}, {int64(3), nil}}))
					}
					check()
					var held *sql.Stmt
					if nonContext {
						held, err = b.DB.Prepare("SELECT id FROM portable.t WHERE id=$1")
					} else {
						held, err = b.DB.PrepareContext(p.Ctx, "SELECT id FROM portable.t WHERE id=$1")
					}
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
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
							rows, err = p.Native.Query("SELECT id,payload FROM portable.t WHERE id=1::bigint")
						} else {
							rows, err = p.Native.QueryContext(p.Ctx, "SELECT id,payload FROM portable.t WHERE id=1::bigint")
						}
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						metadata, err := rows.ColumnTypes()
						if err != nil || len(metadata) != 2 || metadata[0].DatabaseTypeName() != "INT8" || metadata[1].DatabaseTypeName() != "VARCHAR" {
							t.Fatal(metadata, err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
							t.Fatal("self Restore", err)
						}
						check()
						foreign := fixture.NewFixture(t, false, true)
						if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(emitted)); err != nil {
							t.Fatal("foreign Restore", err)
						}
						fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec("INSERT INTO portable.t VALUES(4,'Alpha')", 1))
						fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Select("SELECT id FROM portable.t WHERE lower(payload)='alpha' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}}))
						fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec("UPDATE portable.t SET payload='Beta' WHERE id=2::bigint", 1))
						fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Select("SELECT id FROM portable.t WHERE lower(payload)='alpha' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(4)}}))
						fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec("DELETE FROM portable.t WHERE id=4::bigint", 1))
						fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Select("SELECT id FROM portable.t WHERE id=4::bigint", []string{"id"}, []string{"INT8"}, nil))
						fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Select("SELECT id,payload FROM portable.t ORDER BY id", []string{"id", "payload"}, []string{"INT8", "VARCHAR"}, [][]any{{int64(1), "Alpha"}, {int64(2), "Beta"}, {int64(3), nil}}))
						check() // destination mutation must not mutate the source roots/rows
						var before bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"CREATE INDEX broken ON portable.t USING hash(missing);"))
						want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
						fixture.AssertDiagnostic(t, err, &want)
						failure := errors.New("hash control reader failed")
						if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{failure}); !errors.Is(err, failure) {
							t.Fatal(err)
						}
						failure = errors.New("hash control writer failed")
						if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{failure}); !errors.Is(err, failure) {
							t.Fatal(err)
						}
						canceled, cancel := context.WithCancel(p.Ctx)
						if err := stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
							cancel()
							t.Fatal(err)
						}
						if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
							cancel()
							t.Fatal(err)
						}
						cancel()
						var after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != before.String() {
							t.Fatal("failed Restore changed catalog/rows/roots", err, after.String())
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						run(fixture.Select("SELECT relname FROM pg_class WHERE relname='hn' OR relname='hp' OR relname='he'", []string{"relname"}, []string{"NAME"}, nil))
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
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
						run(fixture.Exec("DROP SCHEMA portable CASCADE", 0))
						if nonContext {
							_, err = b.DB.Exec(emitted)
						} else {
							_, err = b.DB.ExecContext(p.Ctx, emitted)
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
						heldRows, err = held.QueryContext(p.Ctx, int64(1))
					}
					fixture.DeleteReferenceConsume(t, b.PG, heldRows, err, fixture.Select("SELECT id FROM portable.t WHERE id=$1", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}}))
					fixture.RunStep(t, p.Ctx, retained, b.Recorder, fixture.Select("SELECT nextval('kept')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
					run(fixture.Exec("INSERT INTO portable.t VALUES(4,'Alpha')", 1))
					run(fixture.Select("SELECT id FROM portable.t WHERE lower(payload)='alpha' ORDER BY id", []string{"id"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}}))
					run(fixture.Select("SELECT relname,relnatts FROM pg_class WHERE relname='hn' OR relname='hp' OR relname='he' ORDER BY relname", []string{"relname", "relnatts"}, []string{"NAME", "INT2"}, [][]any{{"he", int64(1)}, {"hn", int64(1)}, {"hp", int64(1)}}))
					run(fixture.Exec("DROP SCHEMA portable CASCADE", 0))
				})
			}
		})
	}
}
