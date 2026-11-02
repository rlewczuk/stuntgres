package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLInsertFeatureSmoke(t *testing.T) {
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE "Insert Smoke"("K""id" text,k2 text,payload text,note text)`, 0))
		run(fixture.Exec(`INSERT INTO "Insert Smoke" VALUES('O''Brien',NULL,E'path\\tail',NULL),('other',NULL,'decoy1','keep1'),('O''Brien','other','decoy2','keep2')`, 3))
		held := insertHeld(t, p, c, insertCall)
		const want = `INSERT INTO "Insert Smoke"("K""id",k2,payload,note) VALUES('remote''s',NULL,E'path\\tail',NULL)`
		s := insertSelect(insertCall, want, `"Insert Smoke"`, "1 2", int64(2), `{"O'Brien",NULL}`, `{"remote's",NULL}`)
		generated := insertRead(t, p, b, held, s)
		// Execute exactly the observed value after independently asserting it;
		// a handwritten INSERT cannot satisfy this end-to-end witness.
		run(fixture.Exec(generated, 1))
		run(fixture.Select(`SELECT "K""id",k2,payload,note FROM "Insert Smoke" ORDER BY "K""id",k2 NULLS FIRST`,
			[]string{"K\"id", "k2", "payload", "note"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"},
			[][]any{{"O'Brien", nil, `path\tail`, nil}, {"O'Brien", "other", "decoy2", "keep2"}, {"other", nil, "decoy1", "keep1"}, {"remote's", nil, `path\tail`, nil}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, insertError(insertCall, "2202E", "target key array length must match number of key attributes", `"Insert Smoke"`, "1 2", int64(2), `{"O'Brien",NULL}`, "{a}"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, insertError(insertCall, "21000", "source row not found", `"Insert Smoke"`, "1", int64(1), "{absent}", "{remote}"))
		run(fixture.Exec("CREATE TABLE insert_smoke_logical(gone text,id integer,payload text)", 0))
		run(fixture.Exec("ALTER TABLE insert_smoke_logical DROP COLUMN gone", 0))
		run(fixture.Exec("INSERT INTO insert_smoke_logical VALUES(1,'live')", 1))
		logical := insertSelect(insertCall, "INSERT INTO insert_smoke_logical(id,payload) VALUES('9','live')", "insert_smoke_logical", "1", int64(1), "{1}", "{9}")
		insertBoth(t, p, b, c, logical)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, logical)
		if !b.PG {
			run(fixture.Exec("CREATE VIEW insert_smoke_saved AS SELECT dbins_ext.dblink_build_sql_insert('insert_smoke_logical','1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql", 0))
			run(fixture.Exec("CREATE TEMP TABLE insert_smoke_temp(id integer)", 0))
			run(fixture.Exec("INSERT INTO insert_smoke_temp VALUES(73)", 1))
			run(fixture.Exec("SET application_name='insert-smoke-session'", 0))
			saved := insertSelect("SELECT sql FROM insert_smoke_saved", "INSERT INTO insert_smoke_logical(id,payload) VALUES('9','live')")
			old, err := c.QueryContext(p.Ctx, saved.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			columns, err := old.Columns()
			if err != nil {
				t.Fatal(err)
			}
			types, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			insertBoth(t, p, b, c, saved)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, logical)
			insertBoth(t, p, b, c, insertMember("dblink_build_sql_insert", "dbins_ext"))
			run(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='dblink' AND n.nspname='dbins_ext' AND p.proname='dblink_build_sql_delete' AND p.proargtypes::text='25 22 23 1009'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT id FROM insert_smoke_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(73)}}))
			run(fixture.Select("SELECT current_setting('application_name') AS name", []string{"name"}, []string{"TEXT"}, [][]any{{"insert-smoke-session"}}))
			insertOldText(t, old, columns, types, "INSERT INTO insert_smoke_logical(id,payload) VALUES('9','live')")
		}
	})
}
