package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGForeignTableDiagnostics(t *testing.T) {
	pgftScenario(t, func(c *pgftCase) {
		c.seed(); c.exec("CREATE TABLE public.pgft_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.pgft_sentinel VALUES(91)", 1))
		a, server := c.oid("pgft_a", "item"), c.server("pgft_server")
		held := c.hold(c.conn, pgftQuery)
		unchanged := func() {
			c.raw(held, a, [][]any{{a, server, pgftArray}})
			c.options(c.conn, a, pgftOptions)
			c.both(c.conn, fixture.Select("SELECT attfdwoptions[1] AS option FROM pg_catalog.pg_attribute WHERE attrelid=$1 AND attnum=1", []string{"option"}, []string{"TEXT"}, [][]any{{"scope=column"}}, a))
			c.both(c.conn, fixture.Select("SELECT v FROM public.pgft_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		}
		for _, vector := range []struct{ query, code, message string }{
			{"ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'wrong', ADD remote 'duplicate')", "42710", `option "remote" provided more than once`},
			{"ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET absent 'wrong')", "42704", `option "absent" not found`},
			{"ALTER FOREIGN TABLE pgft_a.item OPTIONS(DROP absent)", "42704", `option "absent" not found`},
			{`ALTER FOREIGN TABLE pgft_a.item OPTIONS(ADD "x=y" 'wrong')`, "22023", `invalid option name "x=y": must not contain "="`},
			{"ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'wrong'), ALTER COLUMN a OPTIONS(DROP absent)", "42704", `option "absent" not found`},
		} {
			step := fixture.ImplicitError(vector.query, fixture.Diagnostic{Code: vector.code, Message: vector.message}); step.ErrorStage = "execute"
			c.run(c.conn, step); unchanged()
			c.run(c.conn, fixture.Prepared(step)); unchanged()
			c.run(c.hold(c.conn, vector.query), step); unchanged()
		}
		for _, vector := range []struct{ query, column string; position int }{
			{"SELECT absent FROM pg_catalog.pg_foreign_table", "absent", 8},
			{"SELECT oid FROM pg_catalog.pg_foreign_table", "oid", 8},
			{"SELECT /*é*/ absent FROM pg_catalog.pg_foreign_table", "absent", 14},
		} {
			d := fixture.Diagnostic{Code: "42703", Message: `column "`+vector.column+`" does not exist`, Position: fixture.Position(vector.position)}
			step := fixture.ImplicitError(vector.query, d); step.SelectRows, step.ErrorStage = true, "execute"
			c.run(c.conn, step)
			step.Prepared, step.ErrorStage = true, "prepare"; c.run(c.conn, step)
		}
		q := "SELECT a FROM pgft_a.item"
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "55000", Message: `foreign-data wrapper "pgft_wrapper" has no handler`}); step.SelectRows, step.ErrorStage = true, "execute"
		c.both(c.conn, step)
		unchanged()
	})
}

func TestReferencePGForeignTablePermissions(t *testing.T) {
	pgftScenario(t, func(c *pgftCase) {
		t := c.t
		c.seed(); c.exec("CREATE ROLE pgft_reader")
		a, server := c.oid("pgft_a", "item"), c.server("pgft_server")
		reader, err := c.b.DB.Conn(c.p.Ctx); if err != nil { t.Fatal(err) }; defer reader.Close()
		held := c.hold(reader, pgftQuery); defer held.Statement.Close()
		c.run(reader, fixture.Exec("SET ROLE pgft_reader", 0)); defer reader.ExecContext(context.Background(), "RESET ROLE")
		visible := func() { c.raw(reader, a, [][]any{{a, server, pgftArray}}); c.raw(held, a, [][]any{{a, server, pgftArray}}); c.options(reader, a, pgftOptions) }
		visible()
		c.exec("GRANT USAGE ON FOREIGN SERVER pgft_server TO pgft_reader", "GRANT SELECT ON pgft_a.item TO pgft_reader"); visible()
		c.exec("REVOKE SELECT ON pgft_a.item FROM pgft_reader", "REVOKE USAGE ON FOREIGN SERVER pgft_server FROM pgft_reader"); visible()
		c.run(reader, fixture.Exec("RESET ROLE", 0)); visible(); c.run(reader, fixture.Exec("SET ROLE pgft_reader", 0)); visible()
		c.exec("GRANT USAGE ON FOREIGN SERVER pgft_server TO pgft_reader", "REVOKE SELECT ON pg_catalog.pg_foreign_table FROM PUBLIC")
		defer func() {
			for _, q := range []string{"REVOKE SELECT(ftrelid) ON pg_catalog.pg_foreign_table FROM pgft_reader", "GRANT SELECT ON pg_catalog.pg_foreign_table TO PUBLIC"} {
				if _, err := c.conn.ExecContext(context.Background(), q); err != nil { t.Error(err) }
			}
		}()
		for _, oid := range []int64{a, 0} {
			step := fixture.ImplicitError(pgftQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_foreign_table"}, oid)
			step.SelectRows, step.ErrorStage = true, "execute"
			c.both(reader, step); c.run(held, step)
		}
		c.exec("GRANT SELECT(ftrelid) ON pg_catalog.pg_foreign_table TO pgft_reader")
		step := fixture.Select("SELECT ftrelid FROM pg_catalog.pg_foreign_table WHERE ftrelid=$1", []string{"ftrelid"}, []string{"OID"}, [][]any{{a}}, a)
		fixture.OidReferenceRows(t, c.p.Ctx, c.b, reader, step); fixture.OidReferenceRows(t, c.p.Ctx, c.b, reader, fixture.Prepared(step))
		for _, q := range []string{"SELECT ftoptions FROM pg_catalog.pg_foreign_table WHERE ftrelid=$1", pgftQuery} {
			denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_foreign_table"}, a)
			denied.SelectRows, denied.ErrorStage = true, "execute"; c.both(reader, denied)
		}
		c.exec("REVOKE SELECT(ftrelid) ON pg_catalog.pg_foreign_table FROM pgft_reader", "GRANT SELECT ON pg_catalog.pg_foreign_table TO PUBLIC")
		visible()
	})
}
