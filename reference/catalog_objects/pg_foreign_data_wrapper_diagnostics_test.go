package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGForeignDataWrapperDiagnostics(t *testing.T) {
	pgfdwScenario(t, func(c *pgfdwCase) {
		for _, vector := range []struct{ query, position string }{
			{"SELECT missing FROM pg_catalog.pg_foreign_data_wrapper", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_foreign_data_wrapper", "14"},
		} {
			for _, prepared := range []bool{false, true} {
				step := fixture.ImplicitError(vector.query, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: vector.position})
				step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
				if prepared { step.ErrorStage = "prepare" }
				c.run(c.conn, step)
			}
		}
		c.exec(pgfdwSeed)
		oid := c.oid("pgfdw_wrapper")
		before := [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=private=value,empty=,remote=é=🙂}"}}
		held := c.hold(c.conn, pgfdwQuery)
		for _, prepared := range []bool{false, true} {
			for _, vector := range []struct{ query, code, message string }{
				{"CREATE FOREIGN DATA WRAPPER pgfdw_wrapper", "42710", `foreign-data wrapper "pgfdw_wrapper" already exists`},
				{"CREATE FOREIGN DATA WRAPPER pgfdw_bad OPTIONS(password 'one',password 'two')", "42710", `option "password" provided more than once`},
				{"ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(SET password 'wrong',ADD password 'duplicate')", "42710", `option "password" provided more than once`},
				{"ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(SET password 'wrong',DROP absent)", "42704", `option "absent" not found`},
				{`ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(SET password 'wrong',ADD "x=y" 'invalid')`, "22023", `invalid option name "x=y": must not contain "="`},
			} {
				c.error(c.conn, vector.query, vector.code, vector.message, prepared)
				c.raw(held, "pgfdw_wrapper", before)
				c.raw(held, "pgfdw_bad", nil)
			}
		}
		c.exec("CREATE ROLE pgfdw_reader")
		const mutation = "DELETE FROM pg_catalog.pg_foreign_data_wrapper WHERE FALSE"
		heldMutation := c.hold(c.conn, mutation)
		c.exec("SET ROLE pgfdw_reader")
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError("CREATE FOREIGN DATA WRAPPER pgfdw_bad", fixture.Diagnostic{Code: "42501", Message: `permission denied to create foreign-data wrapper "pgfdw_bad"`, Hint: "Must be superuser to create a foreign-data wrapper."})
			step.Prepared, step.ErrorStage = prepared, "execute"
			c.run(c.conn, step)
			for _, query := range []string{
				"INSERT INTO pg_catalog.pg_foreign_data_wrapper VALUES(0,'pgfdw_bad',0,0,0,NULL,NULL)",
				"UPDATE pg_catalog.pg_foreign_data_wrapper SET fdwname='pgfdw_bad' WHERE FALSE",
				mutation,
				"TRUNCATE pg_catalog.pg_foreign_data_wrapper",
			} {
				message := "permission denied for table pg_foreign_data_wrapper"
				if query == "TRUNCATE pg_catalog.pg_foreign_data_wrapper" { message = `permission denied: "pg_foreign_data_wrapper" is a system catalog` }
				c.error(c.conn, query, "42501", message, prepared)
				c.raw(held, "pgfdw_wrapper", before)
			}
		}
		c.error(heldMutation, mutation, "42501", "permission denied for table pg_foreign_data_wrapper", false)
		c.raw(held, "pgfdw_bad", nil)
		c.exec("RESET ROLE")
		if !c.b.PG {
			for _, query := range []string{
				"INSERT INTO pg_catalog.pg_foreign_data_wrapper VALUES(0,'pgfdw_bad',0,0,0,NULL,NULL)",
				"UPDATE pg_catalog.pg_foreign_data_wrapper SET fdwname='pgfdw_bad' WHERE FALSE",
				mutation,
				"TRUNCATE pg_catalog.pg_foreign_data_wrapper",
			} {
				for _, prepared := range []bool{false, true} {
					c.error(c.conn, query, "0A000", "catalog source mutation is unsupported", prepared)
					c.raw(held, "pgfdw_wrapper", before)
				}
			}
		}
	})
}
