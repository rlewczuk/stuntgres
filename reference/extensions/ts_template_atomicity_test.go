package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTSTemplateAtomicity(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var db *sql.DB
		for _, b := range p.Backends() { if b.PG == pg { db = b.DB } }
		peer, err := db.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q,0)) }
		run("CREATE SCHEMA ext_locked")
		run("BEGIN")
		templateDefinition(t, p, c, r, "public.template_atomic")
		private := templateIdentity(t, p, c, "template_atomic")
		templateAbsent(t, p, peer, r, "template_atomic")
		run("SAVEPOINT template_created")
		run("ALTER TEXT SEARCH TEMPLATE public.template_atomic RENAME TO template_private")
		run("ALTER TEXT SEARCH TEMPLATE public.template_private SET SCHEMA ext_locked")
		moved := templateIdentity(t, p, c, "template_private")
		if moved.oid != private.oid || moved.namespace == private.namespace { t.Fatal("private ALTER identity", private, moved) }
		run("ROLLBACK TO template_created")
		if templateIdentity(t, p, c, "template_atomic") != private { t.Fatal("savepoint did not restore descriptor") }
		run("ROLLBACK")
		templateAbsent(t, p, c, r, "template_atomic")
		run("BEGIN")
		templateDefinition(t, p, c, r, "public.template_atomic")
		run("CREATE TEXT SEARCH DICTIONARY public.template_atomic_dict (TEMPLATE=public.template_atomic,STOPWORDS=english)")
		run("COMMIT")
		committed := templateIdentity(t, p, c, "template_atomic")
		if templateIdentity(t, p, peer, "template_atomic") != committed { t.Fatal("commit did not publish exact identity") }
		run("BEGIN")
		run("SAVEPOINT before_drop")
		run("DROP TEXT SEARCH TEMPLATE public.template_atomic CASCADE")
		templateAbsent(t, p, c, r, "template_atomic")
		if templateIdentity(t, p, peer, "template_atomic") != committed { t.Fatal("private DROP leaked") }
		fixture.TSTemplateLexemes(t, p, peer, r, pg, "SELECT ts_lexize('public.template_atomic_dict','WORD') AS lexemes", "{word}")
		run("ROLLBACK TO before_drop")
		if templateIdentity(t, p, c, "template_atomic") != committed { t.Fatal("drop savepoint replaced identity") }
		run("COMMIT")
		fixture.TSTemplateLexemes(t, p, c, r, pg, "SELECT ts_lexize('public.template_atomic_dict','WORD') AS lexemes", "{word}")
		if !pg {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, q := range []string{"CREATE TEXT SEARCH TEMPLATE public.template_cancel ("+fixture.TSTemplateSimpleMethods+")", "ALTER TEXT SEARCH TEMPLATE public.template_atomic RENAME TO template_cancel", "DROP TEXT SEARCH TEMPLATE public.template_atomic CASCADE"} {
				_, err := c.ExecContext(ctx, q)
				var state interface{ SQLState() string }
				if !errors.Is(err,context.Canceled) || errors.As(err,&state) { t.Fatal("ordinary cancellation boundary",err) }
				if templateIdentity(t,p,c,"template_atomic") != committed { t.Fatal("cancellation changed identity") }
				templateAbsent(t,p,c,r,"template_cancel")
			}
		}
	})
}
