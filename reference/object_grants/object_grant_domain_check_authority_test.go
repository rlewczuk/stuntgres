package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent release expectation: REL_18_0 execExpr.c ExecInitCoerceToDomain
// initializes the surviving check_expr, including ExecInitFunc's current-user
// EXECUTE check. Domain ownership and type USAGE do not supply routine authority.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/execExpr.c
// The root must establish the exact diagnostic and held-program timing against
// pinned PostgreSQL before changing production admission.
func TestReferenceObjectGrantDomainCheckAuthorityOperator(t *testing.T) {
	objectGrantDomainCheckAuthority(t, "integer", "VALUE>0", "pg_catalog.int4gt(integer,integer)", "int4gt", int64(7), int64(0), "INT4")
}

func TestReferenceObjectGrantDomainCheckAuthorityFunction(t *testing.T) {
	objectGrantDomainCheckAuthority(t, "json", "pg_catalog.json_array_length(VALUE)>0", "pg_catalog.json_array_length(json)", "json_array_length", "[1,2]", "[]", "JSON")
}

func objectGrantDomainCheckAuthority(t *testing.T, base, check, target, routine string, valid, invalid any, databaseType string) {
	t.Helper()
	objectGrantScenario(t, []string{"og_domain_x"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{target})
		run(fixture.Exec("CREATE SCHEMA og_domain_x AUTHORIZATION og_object_other", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_domain_x TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("CREATE DOMAIN og_domain_x.checked AS "+base+" CONSTRAINT checked_positive CHECK ("+check+")", 0))
			run(fixture.Exec("CREATE DOMAIN og_domain_x.child AS og_domain_x.checked", 0))
			run(fixture.Exec("CREATE TABLE og_domain_x.data(id integer,v og_domain_x.checked)", 0))
		})
		run(fixture.Exec("GRANT SELECT,INSERT,UPDATE ON og_domain_x.data TO og_object_reader", 0))
		run(fixture.Exec("GRANT USAGE ON DOMAIN og_domain_x.checked,og_domain_x.child TO og_object_reader", 0))

		// Explicit base typing places conversion in the executor, rather than the
		// wire parameter's domain input function. The bare-domain case below
		// separately covers the public parameter-input route and its Context.
		conversion := "SELECT ($1::" + base + ")::og_domain_x.checked AS v"
		input := "SELECT $1::og_domain_x.checked AS v"
		write := "INSERT INTO og_domain_x.data VALUES($1,$2::" + base + ")"
		var heldConversion, heldWrite fixture.HeldStatement
		run(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
		for i, query := range []string{conversion, write} {
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			// Register immediately, so failure preparing the second statement
			// still closes the first before the connection/fixture teardown.
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			if i == 0 {
				heldConversion = h
			} else {
				heldWrite = h
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		selectValue := func(query string, value any) fixture.Step {
			return fixture.Select(query, []string{"v"}, []string{databaseType}, [][]any{{value}}, value)
		}
		denial := func(query string, rows bool, args ...any) fixture.Step {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + routine}, args...)
			s.SelectRows, s.ErrorStage = rows, "execute"
			return s
		}
		assertData := func(ids ...int64) {
			rows := make([][]any, 0, len(ids))
			for _, id := range ids {
				rows = append(rows, []any{id, valid})
			}
			run(fixture.Select("SELECT id,v FROM og_domain_x.data ORDER BY id", []string{"id", "v"}, []string{"INT4", databaseType}, rows))
			run(fixture.Select("SELECT count(*) AS n FROM og_domain_x.data", []string{"n"}, []string{"INT8"}, [][]any{{int64(len(ids))}}))
		}
		readHeld := func(value any) {
			fixture.RunStep(t, p.Ctx, heldConversion, b.Recorder, selectValue(conversion, value))
		}
		writeHeld := func(id int64) {
			fixture.RunStep(t, p.Ctx, heldWrite, b.Recorder, fixture.Exec(write, 1, id, valid))
		}
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(selectValue(conversion, valid))
			run(selectValue(input, valid))
			readHeld(valid)
			writeHeld(1)
			assertData(1)
		})

		// Leave the domain/table owner authorized while denying the invoker.
		// This rules out borrowing domain-owner EXECUTE for stored checks.
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_other", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
		denyConsumers := func() {
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(denial(conversion, true, valid))
				s := denial(input, true, valid)
				s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
				run(s)
				fixture.RunStep(t, p.Ctx, heldConversion, b.Recorder, denial(conversion, true, valid))
				fixture.RunStep(t, p.Ctx, heldWrite, b.Recorder, denial(write, false, int64(9), valid))
				run(denial(write, false, int64(10), valid))
				run(denial("INSERT INTO og_domain_x.data VALUES(11,$1::"+base+"),(12,$1::"+base+")", false, valid))
				run(denial("UPDATE og_domain_x.data SET v=$1::"+base+" WHERE id=1", false, valid))
				// VALUE remains a variable in the recipe even when the outer datum
				// is NULL. Initializing a surviving domain check still requires X.
				run(denial(conversion, true, nil))
				run(denial("SELECT ($1::"+base+")::og_domain_x.child AS v", true, valid))
				run(denial("SELECT (ARRAY[$1::"+base+"])::og_domain_x.checked[] AS v", true, valid))
				// The stored datum is already checked; reading is not conversion.
				run(fixture.Select("SELECT v FROM og_domain_x.data WHERE id=1", []string{"v"}, []string{databaseType}, [][]any{{valid}}))
			})
		}
		denyConsumers()
		assertData(1)
		fixture.RoleGrantActor(run, "og_object_other", func() { run(selectValue(conversion, valid)) })

		// Reverse owner/invoker routine authority. Reader success must not need
		// EXECUTE held by the domain owner, nor CREATE in its namespace.
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_other", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { run(denial(conversion, true, valid)) })
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(selectValue(conversion, valid))
			run(selectValue(input, valid))
			readHeld(valid)
			readHeld(nil)
			writeHeld(2)
			run(fixture.Exec(write, 1, int64(3), valid))
			run(selectValue("SELECT ($1::"+base+")::og_domain_x.child AS v", valid))
			run(fixture.Select("SELECT array_length((ARRAY[$1::"+base+",NULL])::og_domain_x.checked[],1) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}, valid))
		})
		assertData(1, 2, 3)

		// Authorized false truth is a domain violation, not permission denial.
		// Base typing avoids parameter-input Context and pins structured fields.
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			d := fixture.Diagnostic{
				Code: "23514", Message: `value for domain og_domain_x.checked violates check constraint "checked_positive"`,
				Schema: "og_domain_x", DataType: "checked", Constraint: "checked_positive",
			}
			for _, q := range []string{conversion, "INSERT INTO og_domain_x.data VALUES(20,$1::" + base + "),(21,$2::" + base + ")"} {
				args := []any{invalid}
				if q != conversion {
					args = []any{valid, invalid}
				}
				s := fixture.ImplicitError(q, d, args...)
				s.SelectRows, s.ErrorStage = q == conversion, "execute"
				run(s)
			}
		})
		assertData(1, 2, 3)

		// USAGE governs new persistent type dependencies, not invoking an
		// already supported cast; routine EXECUTE is a separate requirement.
		run(fixture.Exec("REVOKE USAGE ON DOMAIN og_domain_x.checked FROM PUBLIC,og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(selectValue(conversion, valid))
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_domain_x.no_type(v og_domain_x.checked)", "type", "og_domain_x.checked")
		})
		run(fixture.Exec("GRANT USAGE ON DOMAIN og_domain_x.checked TO og_object_reader", 0))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA og_domain_x FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT id FROM og_domain_x.data", "schema", "og_domain_x", prepared)
		})
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_domain_x TO og_object_reader", 0))

		// A second revocation must deny the SAME held consumers after success.
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
		denyConsumers()
		assertData(1, 2, 3)
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			readHeld(valid)
			writeHeld(4)
			assertData(1, 2, 3, 4)
		})
		run(fixture.Exec("DROP SCHEMA og_domain_x CASCADE", 0))
	})
}
