package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pgcryptoArmorConsumerCase(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE USER pgcrypto_owner SUPERUSER", 0))
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part AUTHORIZATION pgcrypto_owner", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE TABLE armor_consumers(id integer,a text DEFAULT pgcrypto_part.armor('test'::bytea),h text DEFAULT pgcrypto_part.armor('zooka'::bytea,ARRAY['foo'],ARRAY['bar']),b bytea GENERATED ALWAYS AS(pgcrypto_part.dearmor(a)) STORED,CONSTRAINT armor_valid CHECK(pgcrypto_part.dearmor(a)='test'::bytea))", 0))
		run(fixture.Exec("CREATE VIEW armor_scalar_view AS SELECT id,pgcrypto_part.dearmor(a) AS decoded FROM armor_consumers", 0))
		run(fixture.Exec("CREATE VIEW armor_source_view AS SELECT key,value FROM pgcrypto_part.pgp_armor_headers('"+fixture.PgcryptoArmorZooka+"')", 0))
		run(fixture.Exec("CREATE TABLE armor_stored_atomic(v text CONSTRAINT armor_stored_valid CHECK(pgcrypto_part.dearmor(v)='test'::bytea))", 0))
		run(fixture.Exec("INSERT INTO armor_consumers(id) VALUES(1)", 1))
		body(t, p, c, r, pg)
	})
}

func pgcryptoArmorConsumerSteps() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT id,a,h,b FROM armor_consumers ORDER BY id", []string{"id", "a", "h", "b"}, []string{"INT4", "TEXT", "TEXT", "BYTEA"}, [][]any{{int64(1), pgcryptoArmorTest, fixture.PgcryptoArmorZooka, "test"}}),
		fixture.Select("SELECT id,decoded FROM armor_scalar_view ORDER BY id", []string{"id", "decoded"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "test"}}),
		fixture.Select("SELECT key,value FROM armor_source_view ORDER BY key,value", []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"foo", "bar"}}),
	}
}

func pgcryptoArmorDependencies(t *testing.T, p *fixture.Pair, c *sql.Conn, ids map[string]int64) {
	t.Helper()
	for _, consumer := range []struct {
		query, routine string
		class          int64
	}{
		{"SELECT oid FROM pg_attrdef WHERE adrelid='armor_consumers'::regclass::oid AND adnum=2", "pgcrypto_part.armor(bytea)", 2604},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='armor_consumers'::regclass::oid AND adnum=3", "pgcrypto_part.armor(bytea,text[],text[])", 2604},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='armor_consumers'::regclass::oid AND adnum=4", "pgcrypto_part.dearmor(text)", 2604},
		{"SELECT oid FROM pg_constraint WHERE conrelid='armor_consumers'::regclass::oid AND conname='armor_valid'", "pgcrypto_part.dearmor(text)", 2606},
		{"SELECT oid FROM pg_rewrite WHERE ev_class='armor_scalar_view'::regclass::oid AND rulename='_RETURN'", "pgcrypto_part.dearmor(text)", 2618},
		{"SELECT oid FROM pg_rewrite WHERE ev_class='armor_source_view'::regclass::oid AND rulename='_RETURN'", "pgcrypto_part.pgp_armor_headers(text)", 2618},
	} {
		var dependent, count int64
		if err := c.QueryRowContext(p.Ctx, consumer.query).Scan(&dependent); err != nil || dependent <= 0 {
			t.Fatal("consumer identity", consumer.query, dependent, err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=$1 AND objid=$2 AND objsubid=0 AND refclassid=1255 AND refobjid=$3 AND refobjsubid=0 AND deptype='n'", consumer.class, dependent, ids[consumer.routine]).Scan(&count); err != nil || count != 1 {
			t.Fatal("exact armor consumer dependency", consumer.routine, dependent, count, err)
		}
	}
}

func TestReferencePgcryptoArmorIdentityAndConsumers(t *testing.T) {
	t.Run("retained_identity", func(t *testing.T) {
		pgcryptoArmorConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			ids := pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")
			pgcryptoArmorDependencies(t, p, c, ids)
			for _, step := range pgcryptoArmorConsumerSteps() {
				pgcryptoArmorBoth(t, p, c, r, step)
			}
			queries := []string{"SELECT pgcrypto_part.armor($1)", "SELECT pgcrypto_part.armor($1,$2,$3)", "SELECT pgcrypto_part.dearmor($1)", "SELECT * FROM pgcrypto_part.pgp_armor_headers($1)"}
			steps := []fixture.Step{
				pgcryptoArmorText(queries[0], pgcryptoArmorTest, []byte("test")),
				pgcryptoArmorText(queries[1], fixture.PgcryptoArmorZooka, []byte("zooka"), "{foo}", "{bar}"),
				pgcryptoArmorBytes(queries[2], []byte("test"), pgcryptoArmorTest),
				fixture.PgcryptoArmorHeaders(queries[3], [][]any{{"foo", "bar"}}, fixture.PgcryptoArmorZooka),
			}
			held := make([]*sql.Stmt, len(queries))
			for i, query := range queries {
				var err error
				held[i], err = c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held[i].Close()
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held[i], SQL: query}, r, steps[i])
			}
			for _, change := range []struct{ old, renamed, args string }{
				{"armor", "armor_one", "bytea"},
				{"armor", "armor_three", "bytea,text[],text[]"},
				{"dearmor", "dearmor_live", "text"},
				{"pgp_armor_headers", "headers_live", "text"},
			} {
				run(fixture.Exec("ALTER FUNCTION pgcrypto_part."+change.old+"("+change.args+") RENAME TO "+change.renamed, 0))
				run(fixture.Exec("ALTER FUNCTION pgcrypto_part."+change.renamed+"("+change.args+") SET SCHEMA public", 0))
				var current int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE oid=$1::regprocedure::oid", "public."+change.renamed+"("+change.args+")").Scan(&current); err != nil || current != ids["pgcrypto_part."+change.old+"("+change.args+")"] {
					t.Fatal("renamed member identity", change, current, err)
				}
			}
			// DDL invalidates prepared query trees, so execution reparses the old
			// spelling. Stored consumers below retain their genuine OID dependencies.
			for i, tc := range []struct{ name, args string }{
				{"pgcrypto_part.armor", "bytea"},
				{"pgcrypto_part.armor", "bytea, text[], text[]"},
				{"pgcrypto_part.dearmor", "text"},
				{"pgcrypto_part.pgp_armor_headers", "text"},
			} {
				missing := extensionMissing(queries[i], tc.name, tc.args)
				if tc.name == "pgcrypto_part.pgp_armor_headers" {
					missing.Diagnostic.Position = "15"
				}
				missing.Args, missing.SelectRows, missing.ErrorStage = steps[i].Args, true, "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held[i], SQL: queries[i]}, r, missing)
			}
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT public.armor_one($1) AS armor", pgcryptoArmorTest, []byte("test")))
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT public.armor_three($1,$2,$3) AS armor", fixture.PgcryptoArmorZooka, []byte("zooka"), "{foo}", "{bar}"))
			pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT public.dearmor_live($1) AS dearmor", []byte("test"), pgcryptoArmorTest))
			pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorHeaders("SELECT * FROM public.headers_live($1)", [][]any{{"foo", "bar"}}, fixture.PgcryptoArmorZooka))
			for _, tc := range []struct{ query, name, args string }{
				{"SELECT pgcrypto_part.armor('test'::bytea)", "pgcrypto_part.armor", "bytea"},
				{"SELECT pgcrypto_part.armor('zooka'::bytea,ARRAY['foo'],ARRAY['bar'])", "pgcrypto_part.armor", "bytea, text[], text[]"},
				{"SELECT pgcrypto_part.dearmor('packet'::text)", "pgcrypto_part.dearmor", "text"},
				{"SELECT * FROM pgcrypto_part.pgp_armor_headers('packet'::text)", "pgcrypto_part.pgp_armor_headers", "text"},
			} {
				s := extensionMissing(tc.query, tc.name, tc.args)
				if tc.name == "pgcrypto_part.pgp_armor_headers" {
					s.Diagnostic.Position = "15"
				}
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				run(s)
			}
			for _, step := range pgcryptoArmorConsumerSteps() {
				pgcryptoArmorBoth(t, p, c, r, step)
			}
			pgcryptoArmorDependencies(t, p, c, ids)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO armor_consumers(id) VALUES(2)", 1))
			run(fixture.Select("SELECT a,h,b FROM armor_consumers WHERE id=2", []string{"a", "h", "b"}, []string{"TEXT", "TEXT", "BYTEA"}, [][]any{{pgcryptoArmorTest, fixture.PgcryptoArmorZooka, "test"}}))
			run(fixture.Exec("ROLLBACK", 0))
			failure := fixture.PgcryptoArmorCorrupt("INSERT INTO armor_stored_atomic VALUES($1),($2)", pgcryptoArmorTest, "invalid")
			failure.SelectRows = false
			pgcryptoArmorBoth(t, p, c, r, failure)
			run(fixture.Select("SELECT count(*) FROM armor_stored_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
	t.Run("live_execute_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pgcrypto_armor_reader"})
		pgcryptoArmorConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("CREATE USER pgcrypto_armor_reader", 0))
			if pg {
				run(fixture.Exec("GRANT USAGE ON SCHEMA pgcrypto_part TO pgcrypto_armor_reader", 0))
			} else {
				run(fixture.Exec("ALTER SCHEMA pgcrypto_part OWNER TO pgcrypto_armor_reader", 0))
			}
			if pg {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pgcrypto_armor_reader", 0))
			}
			run(fixture.Exec("GRANT SELECT ON armor_source_view TO pgcrypto_armor_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_armor_reader", 0))
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			for _, tc := range []struct {
				signature, name, query string
				step                   fixture.Step
			}{
				{"armor(bytea)", "armor", "SELECT pgcrypto_part.armor($1)", pgcryptoArmorText("SELECT pgcrypto_part.armor($1)", pgcryptoArmorTest, []byte("test"))},
				{"armor(bytea,text[],text[])", "armor", "SELECT pgcrypto_part.armor($1,$2,$3)", pgcryptoArmorText("SELECT pgcrypto_part.armor($1,$2,$3)", fixture.PgcryptoArmorZooka, []byte("zooka"), "{foo}", "{bar}")},
				{"dearmor(text)", "dearmor", "SELECT pgcrypto_part.dearmor($1)", pgcryptoArmorBytes("SELECT pgcrypto_part.dearmor($1)", []byte("test"), pgcryptoArmorTest)},
				{"pgp_armor_headers(text)", "pgp_armor_headers", "SELECT * FROM pgcrypto_part.pgp_armor_headers($1)", fixture.PgcryptoArmorHeaders("SELECT * FROM pgcrypto_part.pgp_armor_headers($1)", [][]any{{"foo", "bar"}}, fixture.PgcryptoArmorZooka)},
			} {
				stmt, err := c.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
				fixture.RunStep(t, p.Ctx, held, r, tc.step)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pgcrypto_part."+tc.signature+" FROM PUBLIC", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_armor_reader", 0))
				denied := fixture.PgcryptoArmorError(tc.query, "42501", "permission denied for function "+tc.name, tc.step.Args...)
				fixture.RunStep(t, p.Ctx, held, r, denied)
				pgcryptoArmorBoth(t, p, c, r, denied)
				nullArgs := append([]any(nil), tc.step.Args...)
				nullArgs[0] = nil
				// evaluate_function folds strict scalar constant-NULL calls before
				// execution ACL checks, including these early custom prepared plans.
				// SRFs are not folded; init_sexpr checks ACLs even for NULL input.
				nullResult := fixture.Select(tc.query, tc.step.Columns, tc.step.Types, [][]any{{nil}}, nullArgs...)
				if tc.name == "pgp_armor_headers" {
					nullResult = fixture.PgcryptoArmorError(tc.query, "42501", "permission denied for function "+tc.name, nullArgs...)
				}
				fixture.RunStep(t, p.Ctx, held, r, nullResult)
				pgcryptoArmorBoth(t, p, c, r, nullResult)
				if tc.name == "pgp_armor_headers" {
					pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorError("SELECT key,value FROM armor_source_view", "42501", "permission denied for function pgp_armor_headers"))
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pgcrypto_part."+tc.signature+" TO PUBLIC", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_armor_reader", 0))
				fixture.RunStep(t, p.Ctx, held, r, tc.step)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	})
	t.Run("source_context_restrictions", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			for _, tc := range []struct{ query, message string }{
				{"SELECT 1 WHERE pgp_armor_headers(NULL::text) IS NULL", "set-returning functions are not allowed in WHERE"},
				{"CREATE TABLE armor_bad_default(v text DEFAULT pgp_armor_headers(NULL::text))", "set-returning functions are not allowed in DEFAULT expressions"},
				{"CREATE TABLE armor_bad_check(v text CHECK(pgp_armor_headers(v) IS NULL))", "set-returning functions are not allowed in check constraints"},
				{"SELECT count(pgp_armor_headers(NULL::text))", "aggregate function calls cannot contain set-returning function calls"},
			} {
				position := strconv.Itoa(strings.Index(tc.query, "pgp_armor_headers") + 1)
				d := fixture.Diagnostic{Code: "0A000", Message: tc.message, Position: position}
				if strings.HasPrefix(tc.query, "SELECT count") {
					d.Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, d)
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						// CREATE TABLE transforms DEFAULT/CHECK during utility
						// execution; ordinary SELECT contexts fail during Prepare.
						if !strings.HasPrefix(tc.query, "CREATE TABLE") {
							s.ErrorStage = "prepare"
						}
					}
					t.Run("rejected_context", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
				}
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN ('armor_bad_default','armor_bad_check')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
}
