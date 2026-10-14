package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegTSNamespaceAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"reg_ts_reader"})
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsInstall(t, p, b, c)
		regtsExec(t, p, b, c, "CREATE ROLE reg_ts_reader", 0)
		if b.PG {
			regtsExec(t, p, b, c, `REVOKE ALL ON SCHEMA "Reg.TS" FROM PUBLIC`, 0)
		}
		regtsExec(t, p, b, c, "SET ROLE reg_ts_reader", 0)
		regtsDiagnosticRoutes(t, p, b, c, "regdictionary", `"Reg.TS".unaccent`, "42501", `permission denied for schema Reg.TS`)
		regtsExec(t, p, b, c, `SET search_path TO "Reg.TS",public`, 0)
		regtsDiagnosticRoutes(t, p, b, c, "regdictionary", "unaccent", "42704", `text search dictionary "unaccent" does not exist`)
		regtsExec(t, p, b, c, "RESET ROLE", 0)
		if b.PG {
			regtsExec(t, p, b, c, `GRANT USAGE ON SCHEMA "Reg.TS" TO reg_ts_reader`, 0)
		} else {
			// Native schema ACL SQL is absent; ownership grants reachability
			// without changing the extension dictionary's independent owner.
			regtsExec(t, p, b, c, `ALTER SCHEMA "Reg.TS" OWNER TO reg_ts_reader`, 0)
		}
		regtsExec(t, p, b, c, "SET ROLE reg_ts_reader", 0)
		regtsSelect(t, p, b, c, "SELECT 'unaccent'::regdictionary AS d", []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{"unaccent"}})
		regtsExec(t, p, b, c, "RESET ROLE", 0)
	})
}

func TestReferenceRegTSRetainedRows(t *testing.T) {
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsInstall(t, p, b, c)
		q := `SELECT '"Reg.TS".unaccent'::regdictionary AS d,'-'::regconfig AS c`
		rows, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if b.PG {
			// pq streaming cursors must be drained before reusing their owner.
			regtsAssertAliasRows(t, rows, [][]any{{`"Reg.TS".unaccent`, "-"}}, true)
		}
		regtsExec(t, p, b, c, `ALTER SCHEMA "Reg.TS" RENAME TO "Reg.TS.Renamed"`, 0)
		regtsSelect(t, p, b, c, `SELECT '"Reg.TS.Renamed".unaccent'::regdictionary AS d`, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS.Renamed".unaccent`}})
		if !b.PG {
			regtsAssertAliasRows(t, rows, [][]any{{`"Reg.TS".unaccent`, "-"}}, false)
		}
		if metadata[0].DatabaseTypeName() != "REGDICTIONARY" || metadata[1].DatabaseTypeName() != "REGCONFIG" {
			t.Fatal("retained metadata changed")
		}
	})
}
