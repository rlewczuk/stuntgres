package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const rkForeignSetting = "restrict_nonsystem_relation_kind"
const rkForeignRead = "SELECT id,label,flag FROM fdw_data ORDER BY id"

func rkForeignBaseline() [][]any {
	return [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}
}

func rkForeignDenied(q string, prepared bool) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fdwDiagnostic("55000", "access to non-system foreign table is restricted"))
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, strings.HasPrefix(q, "SELECT"), "execute"
	// Public utility Prepare is metadata-only; execution admits every target.
	return s
}

func rkForeignSet(run func(fixture.Step), value string) {
	run(fixture.Exec("SET "+rkForeignSetting+"='"+value+"'", 0))
}

func TestReferenceRestrictRelationKindForeign(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, _ func(fixture.Step)) {
		run := func(step fixture.Step) {
			t.Helper()
			route := "direct/"
			if step.Prepared {
				route = "prepared/"
			}
			if !t.Run(route+step.Query, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}) {
				t.FailNow()
			}
		}
		fdwLive(t, p, b, r, run)
		deny := func(q string, prepared bool) {
			t.Helper()
			run(rkForeignDenied(q, prepared))
		}
		baseline := rkForeignBaseline()
		run(fdwRows(rkForeignRead, baseline))
		for _, value := range []string{"", "view"} {
			rkForeignSet(run, value)
			for _, prepared := range []bool{false, true} {
				s := fdwRows(rkForeignRead, baseline)
				s.Prepared = prepared
				run(s)
				s = fixture.Exec("INSERT INTO fdw_data VALUES(4,'four',true)", 1)
				s.Prepared = prepared
				run(s)
				withFour := append(rkForeignBaseline(), []any{int64(4), "four", true})
				fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", withFour))
				s = fixture.Exec("UPDATE fdw_data SET label='changed' WHERE id=4", 1)
				s.Prepared = prepared
				run(s)
				withFour[3][1] = "changed"
				fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", withFour))
				s = fixture.Exec("DELETE FROM fdw_data WHERE id=4", 1)
				s.Prepared = prepared
				run(s)
				fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", baseline))
			}
		}
		rkForeignSet(run, "")
		run(fixture.Exec("TRUNCATE fdw_data", 0))
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data", nil))
		fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES(1,'one',true),(2,NULL,false),(3,'three',NULL)", 3)
		run(fixture.Exec("CREATE TABLE rk_local(id integer)", 0))
		run(fixture.Exec("INSERT INTO rk_local VALUES(8)", 1))
		run(fixture.Exec("CREATE VIEW rk_local_v AS SELECT id FROM rk_local", 0))
		run(fixture.Exec("CREATE VIEW rk_foreign_v AS SELECT id,label,flag FROM fdw_data", 0))
		for _, value := range []string{"foreign-table", "view,foreign-table"} {
			rkForeignSet(run, value)
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{rkForeignRead, "INSERT INTO fdw_data VALUES(4,'four',true)", "UPDATE fdw_data SET label='lost' WHERE id=1", "DELETE FROM fdw_data WHERE id=1", "TRUNCATE fdw_data", "TRUNCATE rk_local,fdw_data"} {
					deny(q, prepared)
					fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", baseline))
					run(fixture.Select("SELECT id FROM rk_local", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
				}
			}
		}
		rkForeignSet(run, "foreign-table")
		run(fixture.Select("SELECT id FROM rk_local_v", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{
				"SELECT id,label,flag FROM rk_foreign_v ORDER BY id",
				"INSERT INTO rk_foreign_v VALUES(4,'lost',true)",
				"UPDATE rk_foreign_v SET label='lost' WHERE id=1",
				"DELETE FROM rk_foreign_v WHERE id=1",
				"WITH changed AS (INSERT INTO rk_local VALUES(9) RETURNING id) INSERT INTO fdw_data VALUES(4,'lost',true)",
				"WITH changed AS (INSERT INTO fdw_data VALUES(4,'lost',true) RETURNING id) INSERT INTO rk_local SELECT id FROM changed",
			} {
				deny(q, prepared)
				fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", baseline))
				run(fixture.Select("SELECT id FROM rk_local", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
			}
		}
		// Catalog rows themselves do not acquire handlers; demanded capability does.
		run(fixture.Select("SELECT relname FROM pg_class WHERE oid='fdw_data'::regclass", []string{"relname"}, []string{"NAME"}, [][]any{{"fdw_data"}}))
		run(fixture.Select("SELECT setting FROM pg_settings WHERE name='"+rkForeignSetting+"'", []string{"setting"}, []string{"TEXT"}, [][]any{{"foreign-table"}}))
		// A genuinely new descriptor has never acquired a handler. Both
		// backends must deny its ANALYZE and demanded capability inquiry.
		run(fixture.Exec("CREATE FOREIGN TABLE rk_analyze_cold(id integer,label text,flag boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		deny("ANALYZE rk_analyze_cold", false)
		coldCapability := "SELECT is_updatable FROM information_schema.columns WHERE table_schema='public' AND table_name='rk_analyze_cold' AND column_name='id'"
		deny(coldCapability, false)
		run(fixture.Exec("RESET "+rkForeignSetting, 0))
		run(fixture.Select(coldCapability, []string{"is_updatable"}, []string{"VARCHAR"}, [][]any{{"YES"}}))
		run(fdwRows("SELECT id,label,flag FROM rk_analyze_cold ORDER BY id", baseline))
		run(fdwRows(rkForeignRead, baseline))
	})
}

func TestReferenceRestrictRelationKindForeignImport(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwImportSource(t, p, b, c, r, run)
		rkForeignSet(run, "foreign-table")
		q := "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO(import_a) FROM SERVER fdw_live INTO fdw_import"
		run(rkForeignDenied(q, false))
		fdwImportBag(run)
		// Metadata-only DDL and its validator are not handler admission.
		run(fixture.Exec("CREATE FOREIGN TABLE rk_metadata(id integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
		run(fixture.Exec("DROP FOREIGN TABLE rk_metadata", 0))
		run(fixture.Exec("RESET "+rkForeignSetting, 0))
		run(fixture.Exec(q, 0))
		fdwImportBag(run, "import_a")
		run(fixture.Select("SELECT id FROM fdw_import.import_a ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}

func TestReferenceRestrictRelationKindForeignPrivileges(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		run(fixture.Exec("CREATE ROLE fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_live TO fdw_reader", 0))
		options := "user 'postgres'"
		if b.PG {
			options = "user 'stuntgres',password 'stuntgres',password_required 'false'"
		}
		run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS("+options+")", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		rkForeignSet(run, "foreign-table")
		run(rkForeignDenied(rkForeignRead, false))
		run(fixture.Exec("RESET "+rkForeignSetting, 0))
		s := fixture.ErrorDiagnostic(rkForeignRead, fdwDiagnostic("42501", "permission denied for foreign table fdw_data"))
		s.SelectRows = true
		run(s)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT SELECT ON fdw_data TO fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		run(fdwRows(rkForeignRead, rkForeignBaseline()))
		run(fixture.Exec("RESET ROLE", 0))
	})
}
