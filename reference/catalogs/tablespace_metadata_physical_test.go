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
		cols := []string{"oid", "spcname", "spcowner", "spcoptions"}
		types := []string{"OID", "NAME", "OID", "_TEXT"}
		if b.PG {
			cols = []string{"oid", "spcname", "spcowner", "spcacl", "spcoptions"}
			types = []string{"OID", "NAME", "OID", "_ACLITEM", "_TEXT"}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT * FROM pg_tablespace WHERE FALSE", cols, types, [][]any{}))
		if !b.PG {
			for _, prepared := range []bool{false, true} {
				s := fixture.Error("SELECT spcacl FROM pg_tablespace", "42703")
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		} else {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT spcacl FROM pg_tablespace WHERE oid=1663", []string{"spcacl"}, []string{"_ACLITEM"}, [][]any{{nil}}))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT tablespace FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_tablespace'", []string{"tablespace"}, []string{"NAME"}, [][]any{{"pg_global"}}))
		tablespaceRole(t, p, b, c, "ts_contract_catalog_reader", "")
		tablespaceExec(t, p, b, c, "SET ROLE ts_contract_catalog_reader")
		tablespaceBuiltins(t, p, b, c)
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
