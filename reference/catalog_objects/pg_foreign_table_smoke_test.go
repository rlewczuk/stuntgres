package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGForeignTableFeatureSmoke(t *testing.T) {
	pgftScenario(t, func(c *pgftCase) {
		t := c.t
		held := c.hold(c.conn, pgftQuery)
		c.shape(c.conn, 0, false); c.shape(held, 0, false)
		fixture.OidReferenceRows(t, c.p.Ctx, c.b, c.conn, fixture.Select("SELECT * FROM pg_catalog.pg_foreign_table", pgftColumns, pgftTypes, nil))
		c.seed(); c.facts(); c.identity()
		a, b, server := c.oid("pgft_a", "item"), c.oid("pgft_b", "item"), c.server("pgft_server")
		c.raw(c.conn, a, [][]any{{a, server, pgftArray}}); c.raw(held, a, [][]any{{a, server, pgftArray}})
		c.raw(c.conn, b, [][]any{{b, server, nil}}); c.options(c.conn, a, pgftOptions)
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'smoke')")
		want := "{remote=smoke,empty=,unicode=é🙂}"
		c.raw(held, a, [][]any{{a, server, want}})
		missing := fixture.ImplicitError("SELECT absent FROM pg_catalog.pg_foreign_table", fixture.Diagnostic{Code: "42703", Message: `column "absent" does not exist`, Position: fixture.Position(8)})
		missing.SelectRows, missing.ErrorStage = true, "execute"; c.run(c.conn, missing)
		missing.Prepared, missing.ErrorStage = true, "prepare"; c.run(c.conn, missing)
		q := "ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'wrong'), ALTER COLUMN a OPTIONS(DROP absent)"
		failed := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: `option "absent" not found`}); failed.ErrorStage = "execute"
		c.both(c.conn, failed); c.run(c.hold(c.conn, q), failed)
		c.raw(held, a, [][]any{{a, server, want}})
		c.exec("ALTER FOREIGN TABLE pgft_a.item RENAME TO renamed", "ALTER FOREIGN TABLE pgft_a.renamed SET SCHEMA pgft_b")
		if c.oid("pgft_b", "renamed") != a { t.Fatal("rename/move lost relation identity") }
		c.raw(held, a, [][]any{{a, server, want}})
		scan := fixture.ImplicitError("SELECT a FROM pgft_b.renamed", fixture.Diagnostic{Code: "55000", Message: `foreign-data wrapper "pgft_wrapper" has no handler`}); scan.SelectRows, scan.ErrorStage = true, "execute"
		c.both(c.conn, scan)
		if !c.b.PG {
			old, columns := c.retain(a)
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil { t.Fatal(err) }
			if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil { t.Fatal(err) }
			c.raw(held, a, nil); c.shape(held, a, false)
			if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
			c.consume(old, columns, []any{a, server, want})
			a, b, server = c.oid("pgft_b", "renamed"), c.oid("pgft_b", "item"), c.server("pgft_server")
			c.raw(held, a, [][]any{{a, server, want}})
			c.options(c.conn, a, []string{"remote=smoke", "empty=", "unicode=é🙂"})
			c.both(c.conn, fixture.Select("SELECT f.ftrelid=c.oid AS relation_link,f.ftserver=s.oid AS server_link FROM pg_catalog.pg_foreign_table f JOIN pg_catalog.pg_class c ON c.oid=f.ftrelid JOIN pg_catalog.pg_foreign_server s ON s.oid=f.ftserver WHERE f.ftrelid=$1", []string{"relation_link", "server_link"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, a))
		}
		c.exec("DROP FOREIGN TABLE pgft_b.renamed")
		c.raw(held, a, nil); c.raw(held, b, [][]any{{b, server, nil}})
	})
}
