package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegTSNamespace(t *testing.T) {
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsInstall(t, p, b, c)
		oid := regtsDictionaryOID(t, p, c)
		for _, input := range []string{` "Reg.TS" . UNACCENT `, `"Reg.TS".unaccent`} {
			regtsSelect(t, p, b, c, "SELECT "+regtsLiteral(input)+"::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{oid}})
		}
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		input := `"` + database + `"."Reg.TS".unaccent`
		regtsSelect(t, p, b, c, "SELECT "+regtsLiteral(input)+"::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{oid}})
		regtsExec(t, p, b, c, `SET search_path TO "Reg.TS",public`, 0)
		regtsSelect(t, p, b, c, "SELECT 'UNACCENT'::regdictionary AS d", []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{"unaccent"}})
		regtsExec(t, p, b, c, "SET search_path TO public", 0)
		regtsSelect(t, p, b, c, fmt.Sprintf("SELECT '%d'::regdictionary AS d", oid), []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS".unaccent`}})
		regtsExec(t, p, b, c, `ALTER SCHEMA "Reg.TS" RENAME TO "Reg.TS.Renamed"`, 0)
		regtsSelect(t, p, b, c, fmt.Sprintf("SELECT '%d'::regdictionary AS d", oid), []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS.Renamed".unaccent`}})
		if !b.PG {
			return
		}
		// Genuine PostgreSQL-only custom dictionary DDL. No native provider or
		// configuration DDL is implied; native units cover path/quote algorithms.
		for _, q := range []string{"CREATE SCHEMA reg_ts_left", "CREATE SCHEMA reg_ts_right", "CREATE TEXT SEARCH DICTIONARY reg_ts_left.reg_ts_shadow (TEMPLATE=pg_catalog.simple)", "CREATE TEXT SEARCH DICTIONARY reg_ts_right.reg_ts_shadow (TEMPLATE=pg_catalog.simple)", `CREATE TEXT SEARCH DICTIONARY reg_ts_left."odd"".Name" (TEMPLATE=pg_catalog.simple)`} {
			regtsExec(t, p, b, c, q, 0)
		}
		var leftOID, rightOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'reg_ts_left.reg_ts_shadow'::regdictionary::oid::bigint,'reg_ts_right.reg_ts_shadow'::regdictionary::oid::bigint").Scan(&leftOID, &rightOID); err != nil {
			t.Fatal(err)
		}
		if leftOID <= 0 || rightOID <= 0 || leftOID >= 4294967295 || rightOID >= 4294967295 || leftOID == rightOID {
			t.Fatalf("competing dictionary identities are not valid and distinct: left=%d right=%d", leftOID, rightOID)
		}
		for _, schemas := range [][2]string{{"reg_ts_left", "reg_ts_right"}, {"reg_ts_right", "reg_ts_left"}} {
			schema, shadow := schemas[0], schemas[1]
			winnerOID, shadowOID := leftOID, rightOID
			if schema == "reg_ts_right" {
				winnerOID, shadowOID = rightOID, leftOID
			}
			regtsExec(t, p, b, c, "SET search_path TO "+schema+","+shadow+",public", 0)
			regtsSelect(t, p, b, c, "SELECT 'reg_ts_shadow'::regdictionary::oid='"+schema+".reg_ts_shadow'::regdictionary::oid AS first", []string{"first"}, []string{"BOOL"}, [][]any{{true}})
			regtsSelect(t, p, b, c, "SELECT 'reg_ts_shadow'::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{winnerOID}})
			regtsSelect(t, p, b, c, "SELECT $1::oid::regdictionary AS winner,$2::oid::regdictionary AS shadow", []string{"winner", "shadow"}, []string{"REGDICTIONARY", "REGDICTIONARY"}, [][]any{{"reg_ts_shadow", shadow + ".reg_ts_shadow"}}, winnerOID, shadowOID)
		}
		regtsExec(t, p, b, c, "SET search_path TO public", 0)
		regtsSelect(t, p, b, c, `SELECT 'reg_ts_left."odd"".Name"'::regdictionary AS d`, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`reg_ts_left."odd"".Name"`}})
	})
}
