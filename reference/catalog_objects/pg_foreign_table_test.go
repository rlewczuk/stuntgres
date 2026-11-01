package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGForeignTableShape(t *testing.T) {
	pgftScenario(t, func(c *pgftCase) {
		t := c.t
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT * FROM pg_catalog.pg_foreign_table", pgftColumns, pgftTypes, nil)
			step.Prepared = prepared
			fixture.OidReferenceRows(t, c.p.Ctx, c.b, c.conn, step)
		}
		held := c.hold(c.conn, pgftQuery)
		c.shape(c.conn, 0, false); c.shape(held, 0, false)
		fresh := c.hold(c.conn, pgftQuery); c.shape(fresh, 0, false)
		c.facts()
		c.seed()
		a, b, server := c.oid("pgft_a", "item"), c.oid("pgft_b", "item"), c.server("pgft_server")
		if a == b { t.Fatal("same-name tables share identity") }
		c.raw(c.conn, a, [][]any{{a, server, pgftArray}})
		c.raw(held, a, [][]any{{a, server, pgftArray}})
		c.raw(c.conn, b, [][]any{{b, server, nil}})
		for _, e := range []fixture.Executor{c.conn, held, fresh} { c.shape(e, a, true); c.shape(e, 0, false) }
		all := fixture.Select("SELECT * FROM pg_catalog.pg_foreign_table ORDER BY ftrelid", pgftColumns, pgftTypes, [][]any{{a, server, pgftArray}, {b, server, nil}})
		if b < a { all.Rows[0], all.Rows[1] = all.Rows[1], all.Rows[0] }
		fixture.OidReferenceRows(t, c.p.Ctx, c.b, c.conn, all)
		fixture.OidReferenceRows(t, c.p.Ctx, c.b, c.conn, fixture.Prepared(all))
	})
}

func TestReferencePGForeignTableIdentityOptions(t *testing.T) {
	pgftScenario(t, func(c *pgftCase) {
		t := c.t
		c.seed(); c.identity()
		a, b, server := c.oid("pgft_a", "item"), c.oid("pgft_b", "item"), c.server("pgft_server")
		if a == b { t.Fatal("namespace identity collision") }
		c.options(c.conn, a, pgftOptions); c.options(c.conn, b, nil)
		c.raw(c.conn, c.oid("pgft_a", "ordinary"), nil); c.raw(c.conn, c.oid("pgft_a", "decoy"), nil)
		// Individual decoded values, not implementation-derived array escaping.
		c.exec(`ALTER FOREIGN TABLE pgft_b.item OPTIONS(ADD comma 'a,b',ADD braces '{x}',ADD quote 'a"b',ADD apostrophe 'it''s',ADD slash 'a\b',ADD equals 'x=y')`)
		c.options(c.conn, b, []string{"comma=a,b", "braces={x}", "quote=a\"b", "apostrophe=it's", `slash=a\b`, "equals=x=y"})
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(ADD tail 'last')")
		c.options(c.conn, a, []string{"remote=private=value", "empty=", "unicode=é🙂", "tail=last"})
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET empty 'filled')")
		c.options(c.conn, a, []string{"remote=private=value", "empty=filled", "unicode=é🙂", "tail=last"})
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(DROP empty)")
		c.options(c.conn, a, []string{"remote=private=value", "unicode=é🙂", "tail=last"})
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(DROP remote,ADD remote 'moved')")
		c.options(c.conn, a, []string{"unicode=é🙂", "tail=last", "remote=moved"})
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(DROP unicode,DROP tail)")
		c.options(c.conn, a, []string{"remote=moved"})
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(DROP remote)"); c.options(c.conn, a, nil)
		c.exec("ALTER FOREIGN TABLE pgft_a.item RENAME TO renamed", "ALTER FOREIGN TABLE pgft_a.renamed SET SCHEMA pgft_b", "ALTER SERVER pgft_server RENAME TO pgft_renamed")
		if c.oid("pgft_b", "renamed") != a || c.server("pgft_renamed") != server { t.Fatal("rename/move changed identity") }
		c.raw(c.conn, a, [][]any{{a, server, nil}})
		c.exec("DROP FOREIGN TABLE pgft_b.renamed", "CREATE FOREIGN TABLE pgft_b.renamed() SERVER pgft_renamed OPTIONS(new 'definition')")
		fresh := c.oid("pgft_b", "renamed")
		if fresh == a { t.Fatal("recreation reused identity") }
		c.raw(c.conn, a, nil); c.raw(c.conn, fresh, [][]any{{fresh, server, "{new=definition}"}})
	})
}

func TestReferencePGForeignTableLifecycle(t *testing.T) {
	pgftScenario(t, func(c *pgftCase) {
		c.seed()
		a, b, server := c.oid("pgft_a", "item"), c.oid("pgft_b", "item"), c.server("pgft_server")
		held := c.hold(c.conn, pgftQuery)
		c.raw(held, a, [][]any{{a, server, pgftArray}})
		c.exec("ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'committed')")
		committed := "{remote=committed,empty=,unicode=é🙂}"
		c.raw(held, a, [][]any{{a, server, committed}})
		c.exec("BEGIN", "CREATE FOREIGN TABLE pgft_a.transient() SERVER pgft_server", "ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'private')")
		transient := c.oid("pgft_a", "transient")
		c.raw(held, transient, [][]any{{transient, server, nil}})
		c.raw(held, a, [][]any{{a, server, "{remote=private,empty=,unicode=é🙂}"}})
		c.exec("ROLLBACK")
		c.raw(held, transient, nil); c.raw(held, a, [][]any{{a, server, committed}})
		c.exec("BEGIN", "ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'surviving')", "SAVEPOINT pgft_save", "ALTER FOREIGN TABLE pgft_a.item OPTIONS(DROP empty,ADD tail 'discard')")
		c.raw(held, a, [][]any{{a, server, "{remote=surviving,unicode=é🙂,tail=discard}"}})
		c.exec("ROLLBACK TO pgft_save")
		c.raw(held, a, [][]any{{a, server, "{remote=surviving,empty=,unicode=é🙂}"}})
		c.exec("COMMIT")
		c.raw(held, a, [][]any{{a, server, "{remote=surviving,empty=,unicode=é🙂}"}})
		q := "ALTER FOREIGN TABLE pgft_a.item OPTIONS(SET remote 'wrong'), ALTER COLUMN a OPTIONS(DROP absent)"
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: `option "absent" not found`}); step.ErrorStage = "execute"
		c.both(c.conn, step)
		c.raw(held, a, [][]any{{a, server, "{remote=surviving,empty=,unicode=é🙂}"}})
		c.both(c.conn, fixture.Select("SELECT attfdwoptions[1] AS option FROM pg_catalog.pg_attribute WHERE attrelid=$1 AND attnum=1", []string{"option"}, []string{"TEXT"}, [][]any{{"scope=column"}}, a))
		c.exec("CREATE SERVER pgft_other FOREIGN DATA WRAPPER pgft_wrapper", "CREATE FOREIGN TABLE pgft_a.unrelated() SERVER pgft_other")
		other, otherServer := c.oid("pgft_a", "unrelated"), c.server("pgft_other")
		c.exec("DROP FOREIGN TABLE pgft_a.item")
		c.raw(held, a, nil); c.raw(held, b, [][]any{{b, server, nil}})
		c.exec("DROP SERVER pgft_server CASCADE")
		c.raw(held, b, nil); c.raw(held, other, [][]any{{other, otherServer, nil}})
	})
}
