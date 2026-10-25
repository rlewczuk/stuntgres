package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceMetadata(t *testing.T) {
	tablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		tablespaceBuiltins(t, p, b, c)
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT t.spcname,r.rolname FROM pg_tablespace t JOIN pg_roles r ON t.spcowner=r.oid WHERE t.oid=1663 ORDER BY t.spcname", []string{"spcname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"pg_default", owner}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, relation := range []string{"pg_tablespace", "pg_catalog.pg_tablespace"} {
			for _, tc := range []struct {
				predicate string
				rows      [][]any
			}{
				{"TRUE", tablespaceRows(b)},
				{"FALSE", [][]any{}},
				{"oid=0", [][]any{}},
				{"spcacl IS NULL AND spcoptions IS NULL", tablespaceRows(b)},
				{"spcacl IS NOT NULL OR spcoptions IS NOT NULL", [][]any{}},
			} {
				for _, prepared := range []bool{false, true} {
					s := tablespaceStep(b, "SELECT * FROM "+relation+" WHERE "+tc.predicate+" ORDER BY oid", tc.rows)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT spcacl FROM pg_tablespace WHERE oid=1663", []string{"spcacl"}, []string{"_ACLITEM"}, [][]any{{nil}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		tablespaceHeldReads(t, p, b, c)
		tablespaceExec(t, p, b, c, "CREATE VIEW ts_contract_catalog_view AS SELECT oid,spcname,spcowner,spcacl,spcoptions FROM pg_catalog.pg_tablespace")
		for _, prepared := range []bool{false, true} {
			s := tablespaceStep(b, "SELECT * FROM ts_contract_catalog_view ORDER BY oid", tablespaceRows(b))
			// View result nullability is not the base relation's NOT NULL contract.
			s.CheckMetadata = nil
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT attnum,attndims FROM pg_attribute WHERE attrelid=1213 AND attnum>0 ORDER BY attnum", []string{"attnum", "attndims"}, []string{"INT2", "INT2"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(0)}, {int64(3), int64(0)}, {int64(4), int64(1)}, {int64(5), int64(1)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT tablespace FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_tablespace'", []string{"tablespace"}, []string{"NAME"}, [][]any{{"pg_global"}}))
		tablespaceRole(t, p, b, c, "ts_contract_catalog_reader", "")
		tablespaceExec(t, p, b, c, "SET ROLE ts_contract_catalog_reader")
		tablespaceBuiltins(t, p, b, c)
		tablespaceExec(t, p, b, c, "SET search_path TO public")
		tablespaceBuiltins(t, p, b, c)
		tablespaceHeldReads(t, p, b, c)
		tablespaceExec(t, p, b, c, "RESET search_path")
		tablespaceExec(t, p, b, c, "RESET ROLE")
	})
}

func TestReferenceTablespaceRealDuplicate(t *testing.T) {
	tablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const directory = "/tmp/stuntgres_tablespace_contract_owned"
		if b.PG {
			tablespaceContainer(t, p, "test ! -e "+directory+" && mkdir "+directory+" && chown postgres:postgres "+directory)
			t.Cleanup(func() { tablespaceContainer(t, p, "rmdir "+directory) })
		}
		tablespaceRole(t, p, b, c, "ts_contract_physical_owner", "")
		if b.PG {
			t.Cleanup(func() {
				if _, err := b.DB.ExecContext(p.Ctx, "DROP TABLESPACE IF EXISTS ts_contract_existing"); err != nil {
					t.Error(err)
				}
			})
		}
		tablespaceExec(t, p, b, c, "CREATE TABLE ts_contract_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ts_contract_sentinel VALUES(73)", 1))
		q := tablespaceSQL("ts_contract_existing", "ts_contract_physical_owner", directory)
		if b.PG {
			tablespaceExec(t, p, b, c, q)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT t.spcname,r.rolname FROM pg_tablespace t JOIN pg_roles r ON t.spcowner=r.oid WHERE t.spcname='ts_contract_existing'", []string{"spcname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"ts_contract_existing", "ts_contract_physical_owner"}}))
			tablespaceError(t, p, b, c, q, tablespaceDiagnostic("42710", "tablespace \"ts_contract_existing\" already exists"), false)
		} else {
			for range 2 {
				tablespaceError(t, p, b, c, q, tablespacePhysical(b, directory), false)
			}
			tablespaceNoUserRows(t, p, b, c)
		}
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_existing", "ts_contract_missing", "relative"), tablespaceDiagnostic("42704", "role \"ts_contract_missing\" does not exist"), false)
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_existing", "", "relative"), tablespaceDiagnostic("42P17", "tablespace location must be an absolute path"), false)
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_failed", "", tablespaceAbsent), tablespacePhysical(b, tablespaceAbsent), false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT spcname FROM pg_tablespace WHERE spcname='ts_contract_failed'", []string{"spcname"}, []string{"NAME"}, [][]any{}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a FROM ts_contract_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(73)}}))
	})
}
