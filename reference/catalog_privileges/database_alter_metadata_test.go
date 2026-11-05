package reference_test

import (
	"reflect"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseAlterTablespace(t *testing.T) {
	t.Run("builtin-placement", func(t *testing.T) {
		databaseAlterScenario(t, func(c *databaseCreateCase) {
			defer databaseAlterBuiltinACLCleanup(c)()
			c.create("da_source", "OWNER da_owner", false)
			oid := databaseAlterSeed(c, "da_source")
			active, raw, _ := c.pool("da_source", "")
			fixture.State(c.t, active.PingContext(c.p.Ctx), "")
			defer databaseAlterClose(c, active, raw)
			for _, q := range []string{"ALTER DATABASE da_source SET TABLESPACE pg_default", "ALTER DATABASE da_source WITH TABLESPACE = pg_default"} {
				c.run(fixture.Exec(q, 0))
				c.run(fixture.Prepared(fixture.Exec(q, 0)))
				databaseAlterHeld(c, c.conn, q, func(h fixture.HeldStatement) {
					databaseAlterFlags(c, "da_source", false, true, -1)
					databaseAlterRun(c, h, c.backend.Recorder, fixture.Exec(q, 0))
					databaseAlterRun(c, h, c.backend.Recorder, fixture.Exec(q, 0))
				})
			}
			databaseAlterData(c, "da_source", oid, 42)
			c.run(fixture.Exec("SET ROLE da_owner", 0))
			c.error("ALTER DATABASE da_source SET TABLESPACE pg_default", "42501", "permission denied for tablespace pg_default")
			c.error("ALTER DATABASE da_source SET TABLESPACE pg_global", "42501", "permission denied for tablespace pg_global")
			c.run(fixture.Exec("RESET ROLE", 0))
			c.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default,pg_global TO da_owner", 0))
			c.run(fixture.Exec("SET ROLE da_owner", 0))
			c.run(fixture.Exec("ALTER DATABASE da_source SET TABLESPACE pg_default", 0))
			c.error("ALTER DATABASE da_source SET TABLESPACE pg_global", "22023", "pg_global cannot be used as default tablespace")
			c.error("ALTER DATABASE da_source SET TABLESPACE da_missing", "42704", "tablespace \"da_missing\" does not exist")
			c.run(fixture.Exec("RESET ROLE", 0))
			current := c.scalar("SELECT current_database()")
			c.error("ALTER DATABASE "+pq.QuoteIdentifier(current)+" SET TABLESPACE da_missing", "55006", "cannot change the tablespace of the currently open database")
			for _, q := range []string{"ALTER DATABASE da_source SET TABLESPACE pg_default", "ALTER DATABASE da_source WITH TABLESPACE pg_default"} {
				c.run(fixture.Exec("BEGIN", 0))
				c.error(q, "25001", "ALTER DATABASE SET TABLESPACE cannot run inside a transaction block")
				c.run(fixture.Exec("ROLLBACK", 0))
				tx, err := c.conn.BeginTx(c.p.Ctx, nil)
				if err != nil {
					c.t.Fatal(err)
				}
				databaseAlterRun(c, tx, c.backend.Recorder, fixture.ErrorDiagnostic(q, databaseCreateDiagnostic("25001", "ALTER DATABASE SET TABLESPACE cannot run inside a transaction block")))
				if err := tx.Rollback(); err != nil {
					c.t.Fatal(err)
				}
				c.error(q+"; SELECT 1", "25001", "ALTER DATABASE SET TABLESPACE cannot run inside a transaction block")
			}
			var placement any
			if c.backend.PG {
				placement = int64(1663)
			}
			c.run(fixture.Select("SELECT dattablespace::bigint AS dattablespace FROM pg_database WHERE datname='da_source'", []string{"dattablespace"}, []string{"INT8"}, [][]any{{placement}}))
		})
	})
	t.Run("shared-builtin-acl", func(t *testing.T) {
		databaseAlterScenario(t, databaseAlterSharedTablespaceACL)
	})
}

func TestReferenceDatabaseAlterCollation(t *testing.T) {
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		oid := databaseAlterSeed(c, "da_source")
		records := []fixture.Diagnostic{}
		_, conn, rec := fixture.NoticeReferencePool(c.t, c.p, c.backend.PG, &records)
		current := c.scalar("SELECT current_database()")
		unchanged := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "version has not changed"}
		for _, name := range []string{current, "da_source"} {
			q := "ALTER DATABASE " + pq.QuoteIdentifier(name) + " REFRESH COLLATION VERSION"
			for _, prepared := range []bool{false, true} {
				records = nil
				s := fixture.Exec(q, 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				databaseAlterRun(c, conn, rec, s)
				if !reflect.DeepEqual(records, []fixture.Diagnostic{unchanged}) {
					c.t.Fatalf("refresh notices: %#v", records)
				}
			}
			databaseAlterHeld(c, conn, q, func(h fixture.HeldStatement) {
				records = nil
				databaseAlterFlags(c, "da_source", false, true, -1)
				if len(records) != 0 {
					c.t.Fatal("Prepare emitted a notice", records)
				}
				for range 2 {
					records = nil
					databaseAlterRun(c, h, rec, fixture.Exec(q, 0))
					if !reflect.DeepEqual(records, []fixture.Diagnostic{unchanged}) {
						c.t.Fatalf("held notices: %#v", records)
					}
				}
			})
		}
		databaseAlterRun(c, conn, rec, fixture.Exec("BEGIN", 0))
		databaseAlterRun(c, conn, rec, fixture.Exec("ALTER DATABASE da_source REFRESH COLLATION VERSION", 0))
		databaseAlterRun(c, conn, rec, fixture.Exec("ROLLBACK", 0))
		c.run(fixture.Select("SELECT datcollversion,pg_database_collation_actual_version(oid) AS actual FROM pg_database WHERE datname='da_source'", []string{"datcollversion", "actual"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}}))
		databaseAlterRun(c, conn, rec, fixture.Exec("SET client_min_messages TO warning", 0))
		records = nil
		databaseAlterRun(c, conn, rec, fixture.Exec("ALTER DATABASE da_source REFRESH COLLATION VERSION", 0))
		if len(records) != 0 {
			c.t.Fatal("suppressed notice delivered", records)
		}
		databaseAlterRun(c, conn, rec, fixture.Exec("RESET client_min_messages", 0))
		databaseAlterRun(c, conn, rec, fixture.Exec("SET ROLE da_plain", 0))
		records = nil
		databaseAlterRun(c, conn, rec, fixture.ErrorDiagnostic("ALTER DATABASE da_source REFRESH COLLATION VERSION", databaseCreateDiagnostic("42501", "must be owner of database da_source")))
		if len(records) != 0 {
			c.t.Fatal("unauthorized refresh emitted notice", records)
		}
		databaseAlterRun(c, conn, rec, fixture.Exec("RESET ROLE", 0))
		databaseAlterRun(c, conn, rec, fixture.ErrorDiagnostic("ALTER DATABASE da_missing REFRESH COLLATION VERSION", databaseCreateDiagnostic("3D000", "database \"da_missing\" does not exist")))
		databaseAlterData(c, "da_source", oid, 42)
	})
}
