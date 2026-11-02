package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPolicyPrepared(t *testing.T) {
	t.Run("HeldDynamicCallsAndMetadata", func(t *testing.T) {
		pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("CREATE TABLE policy_prepared(id integer,p text,s text)", 0))
			run(fixture.Exec("INSERT INTO policy_prepared VALUES(1,'foox','NB'),(2,NULL,'bad')", 2))
			for _, q := range []string{"SELECT pgcrypto_part.crypt($1,$2) AS value", "SELECT pgcrypto_part.crypt(p,s) AS value FROM policy_prepared WHERE id=$1"} {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				args, nullArgs := []any{"foox", "NB"}, []any{nil, "bad"}
				column := q != "SELECT pgcrypto_part.crypt($1,$2) AS value"
				if column { args, nullArgs = []any{1}, []any{2} }
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if err != nil { t.Fatal(err) }
				defer rows.Close()
				pgcryptoPolicyTextLength(t, rows)
				if err := rows.Close(); err != nil { t.Fatal(err) }
				for _, policy := range []string{"on", "off", "on", "fips", "off", "on"} {
					run(fixture.Exec("SET "+pgcryptoPolicyName+"='"+policy+"'", 0))
					var s fixture.Step
					if policy == "off" || policy == "fips" && pgcryptoRandomFIPSExpected(pg) {
						message := pgcryptoPolicyOff
						if policy == "fips" { message = pgcryptoPolicyFIPS }
						s = pgcryptoPolicyFailure(q, message, args...)
					} else {
						s = pgcryptoPolicyText(r, q, "NB53EGGqrrb5E", args...)
					}
					fixture.RunStep(t, p.Ctx, held, r, s)
					fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, nil, nullArgs...))
					if column {
						empty := pgcryptoPolicyText(r, q, nil, 9)
						empty.Rows = [][]any{}
						fixture.RunStep(t, p.Ctx, held, r, empty)
					}
				}
			}
			for _, q := range []string{"SELECT pgcrypto_part.gen_salt($1) AS value", "SELECT pgcrypto_part.gen_salt($1,$2) AS value"} {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				args, nullArgs := []any{"des"}, []any{nil}
				if q == "SELECT pgcrypto_part.gen_salt($1,$2) AS value" { args, nullArgs = []any{"des", 25}, []any{nil, -1} }
				for _, policy := range []string{"on", "off", "on", "fips", "on"} {
					run(fixture.Exec("SET "+pgcryptoPolicyName+"='"+policy+"'", 0))
					if policy == "off" || policy == "fips" && pgcryptoRandomFIPSExpected(pg) {
						message := pgcryptoPolicyOff
						if policy == "fips" { message = pgcryptoPolicyFIPS }
						fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyFailure(q, message, args...))
					} else {
						rows, err := stmt.QueryContext(p.Ctx, args...)
						if err != nil { t.Fatal(err) }
						defer rows.Close()
						pgcryptoPolicyTextLength(t, rows)
						if !rows.Next() { rows.Close(); t.Fatal("missing salt", rows.Err()) }
						var salt string
						if err := rows.Scan(&salt); err != nil { rows.Close(); t.Fatal(err) }
						pgcryptoPasswordAlgorithmSaltShape(t, salt, "des", 25)
						if rows.Next() || rows.Err() != nil { rows.Close(); t.Fatal("salt tail", rows.Err()) }
						rows.Close()
					}
					fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, nil, nullArgs...))
				}
			}
			pgcryptoPolicyNotices(t, notices)
		})
	})
	t.Run("AcceptedLiteralVersusNewEvaluation", func(t *testing.T) {
		pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("SET plan_cache_mode=force_generic_plan", 0))
			q := "SELECT pgcrypto_part.crypt('foox','NB') AS value"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, "NB53EGGqrrb5E"))
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			// crypt is IMMUTABLE: this is an accepted value, not a fresh body call.
			fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, "NB53EGGqrrb5E"))
			run(pgcryptoPolicyFailure(q, pgcryptoPolicyOff))
			fresh := fixture.Prepared(pgcryptoPolicyFailure(q, pgcryptoPolicyOff))
			fresh.ErrorStage = "execute"
			run(fresh)
			run(pgcryptoPolicyText(r, "SELECT pgcrypto_part.crypt(NULL::text,'invalid') AS value", nil))
			run(fixture.Prepared(pgcryptoPolicyText(r, "SELECT pgcrypto_part.crypt(NULL::text,'invalid') AS value", nil)))
			pgcryptoPolicyNotices(t, notices)
		})
	})
	for _, mode := range []string{"force_generic_plan", "force_custom_plan"} {
		t.Run("SQLPrepare_"+mode, func(t *testing.T) {
			pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("SET plan_cache_mode="+mode, 0))
				run(fixture.Exec("PREPARE policy_literal AS SELECT pgcrypto_part.crypt('foox','NB') AS value", 0))
				run(fixture.Exec("PREPARE policy_parameter(text,text) AS SELECT pgcrypto_part.crypt($1,$2) AS value", 0))
				run(fixture.Exec("PREPARE policy_salt(text,integer) AS SELECT pgcrypto_part.gen_salt($1,$2) AS value", 0))
				run(pgcryptoPolicyText(r, "EXECUTE policy_literal", "NB53EGGqrrb5E"))
				run(pgcryptoPolicyText(r, "EXECUTE policy_parameter('foox','NB')", "NB53EGGqrrb5E"))
				run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
				run(pgcryptoPolicyText(r, "EXECUTE policy_literal", "NB53EGGqrrb5E"))
				run(pgcryptoPolicyFailure("EXECUTE policy_parameter('foox','NB')", pgcryptoPolicyOff))
				run(pgcryptoPolicyFailure("EXECUTE policy_salt('des',25)", pgcryptoPolicyOff))
				run(pgcryptoPolicyText(r, "EXECUTE policy_parameter(NULL,'invalid')", nil))
				run(pgcryptoPolicyText(r, "EXECUTE policy_salt(NULL,-1)", nil))
				run(fixture.Exec("SET "+pgcryptoPolicyName+"=on", 0))
				run(pgcryptoPolicyText(r, "EXECUTE policy_parameter('foox','NB')", "NB53EGGqrrb5E"))
				for _, name := range []string{"policy_literal", "policy_parameter", "policy_salt"} { run(fixture.Exec("DEALLOCATE "+name, 0)) }
				pgcryptoPolicyNotices(t, notices)
			})
		})
	}
}
