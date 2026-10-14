package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegTSPreparedLifetime(t *testing.T) {
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsInstall(t, p, b, c)
		for _, alias := range []string{"regconfig", "regdictionary"} {
			q := "SELECT $1::" + alias + " AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, arg := range []any{"-", []byte("-"), nil, []byte(nil), "4294967295", "-"} {
					var want any = "-"
					if arg == nil {
						want = nil
					}
					if bytes, ok := arg.([]byte); ok && bytes == nil {
						want = nil
					}
					if arg == "4294967295" {
						want = "4294967295"
					}
					typ := "REGCONFIG"
					if alias == "regdictionary" {
						typ = "REGDICTIONARY"
					}
					regtsSelect(t, p, b, held, q, []string{"v"}, []string{typ}, [][]any{{want}}, arg)
				}
			}()
		}
		q := `SELECT '"Reg.TS".unaccent'::regdictionary AS d`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		regtsSelect(t, p, b, held, q, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS".unaccent`}})
		regtsExec(t, p, b, c, "CREATE TABLE reg_ts_unrelated(v integer)", 0)
		regtsSelect(t, p, b, held, q, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS".unaccent`}})
		regtsExec(t, p, b, c, `SET search_path TO "Reg.TS",public`, 0)
		regtsSelect(t, p, b, held, q, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{"unaccent"}})
		parameterSQL := "SELECT $1::text::regdictionary AS d"
		parameter, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer parameter.Close()
		parameterHeld := fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterSQL}
		regtsSelect(t, p, b, parameterHeld, parameterSQL, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{"unaccent"}}, "UNACCENT")
		bad := regtsError(parameterSQL, "42704", `text search dictionary "reg_ts_missing" does not exist`, "", "", "reg_ts_missing")
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, parameterHeld, b.Recorder, bad)
		regtsSelect(t, p, b, parameterHeld, parameterSQL, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{nil}}, nil)
		regtsExec(t, p, b, c, "SET search_path TO public", 0)
		regtsSelect(t, p, b, parameterHeld, parameterSQL, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS".unaccent`}}, `"Reg.TS".unaccent`)
		regtsExec(t, p, b, c, `ALTER SCHEMA "Reg.TS" RENAME TO "Reg.TS.Renamed"`, 0)
		regtsSelect(t, p, b, parameterHeld, parameterSQL, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS.Renamed".unaccent`}}, `"Reg.TS.Renamed".unaccent`)
		if b.PG {
			// Real same-name dictionary objects characterize literal invalidation
			// versus per-execution TEXT input without fabricating native objects.
			for _, sql := range []string{"CREATE SCHEMA reg_ts_left", "CREATE SCHEMA reg_ts_right", "CREATE TEXT SEARCH DICTIONARY reg_ts_left.reg_ts_capture (TEMPLATE=pg_catalog.simple)", "CREATE TEXT SEARCH DICTIONARY reg_ts_right.reg_ts_capture (TEMPLATE=pg_catalog.simple)", "SET search_path TO reg_ts_left,reg_ts_right,public"} {
				regtsExec(t, p, b, c, sql, 0)
			}
			var leftOID, rightOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'reg_ts_left.reg_ts_capture'::regdictionary::oid::bigint,'reg_ts_right.reg_ts_capture'::regdictionary::oid::bigint").Scan(&leftOID, &rightOID); err != nil {
				t.Fatal(err)
			}
			if leftOID <= 0 || rightOID <= 0 || leftOID >= 4294967295 || rightOID >= 4294967295 || leftOID == rightOID {
				t.Fatalf("competing prepared dictionary identities are not valid and distinct: left=%d right=%d", leftOID, rightOID)
			}
			identitySQL := "SELECT $1::text::regdictionary::oid::bigint AS id"
			identity, err := c.PrepareContext(p.Ctx, identitySQL)
			if err != nil {
				t.Fatal(err)
			}
			defer identity.Close()
			identityHeld := fixture.HeldStatement{Executor: c, Statement: identity, SQL: identitySQL}
			literalSQL := "SELECT 'reg_ts_capture'::regdictionary::oid='reg_ts_left.reg_ts_capture'::regdictionary::oid AS same"
			literal, err := c.PrepareContext(p.Ctx, literalSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer literal.Close()
			literalHeld := fixture.HeldStatement{Executor: c, Statement: literal, SQL: literalSQL}
			regtsSelect(t, p, b, literalHeld, literalSQL, []string{"same"}, []string{"BOOL"}, [][]any{{true}})
			regtsSelect(t, p, b, identityHeld, identitySQL, []string{"id"}, []string{"INT8"}, [][]any{{leftOID}}, "reg_ts_capture")
			regtsSelect(t, p, b, parameterHeld, parameterSQL, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{"reg_ts_capture"}}, "reg_ts_capture")
			regtsExec(t, p, b, c, "SET search_path TO reg_ts_right,reg_ts_left,public", 0)
			regtsSelect(t, p, b, literalHeld, literalSQL, []string{"same"}, []string{"BOOL"}, [][]any{{false}})
			regtsSelect(t, p, b, parameterHeld, parameterSQL, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{"reg_ts_capture"}}, "reg_ts_capture")
			regtsSelect(t, p, b, identityHeld, identitySQL, []string{"id"}, []string{"INT8"}, [][]any{{rightOID}}, "reg_ts_capture")
		}
	})
}
