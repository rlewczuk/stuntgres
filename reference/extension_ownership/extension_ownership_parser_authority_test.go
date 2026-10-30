package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func ownershipParserAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"eo_parser_actor", "eo_parser_other"})
	fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := fixture.OwnershipRun(t, p, b, c)
		defer ownershipStockCleanup(t, c)()
		for _, q := range []string{
			`CREATE ROLE eo_parser_actor SUPERUSER`, `CREATE ROLE eo_parser_other`,
			`CREATE SCHEMA eo_old AUTHORIZATION eo_parser_actor`, `CREATE SCHEMA eo_new AUTHORIZATION eo_parser_actor`, `CREATE SCHEMA eo_other`,
			`SET SESSION AUTHORIZATION eo_parser_actor`, `CREATE EXTENSION unaccent SCHEMA eo_old`, `RESET SESSION AUTHORIZATION`,
			`ALTER ROLE eo_parser_actor NOSUPERUSER`,
		} {
			run(fixture.Exec(q, 0))
		}
		for _, q := range ownershipParserSetup() {
			run(fixture.Exec(q, 0))
		}
		id := fixture.OwnershipID(t, p, c, `SELECT oid::bigint FROM pg_ts_parser WHERE prsname='item' AND prsnamespace='eo_old'::regnamespace`)
		original := ownershipParserSnapshot(t, p, c, id)
		// Both ownerless kinds use objectaddress.c's bare membership gate.
		// Probe the genuine template on either side of the lawful SU detach.
		templateAuthority := func(verb string) {
			queries := []string{
				ownershipClassMemberSet, ownershipEdges,
				`SELECT oid::bigint,extowner::bigint,extnamespace::bigint FROM pg_extension WHERE extname='unaccent'`,
				`SELECT oid::bigint,tmplnamespace::bigint,tmplname,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace='eo_old'::regnamespace`,
				`SELECT d.refclassid::bigint,d.refobjid::bigint,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_ts_template t ON t.oid=d.objid WHERE d.classid='pg_ts_template'::regclass AND t.tmplname='unaccent' AND t.tmplnamespace='eo_old'::regnamespace ORDER BY d.refclassid,d.refobjid,d.refobjsubid,d.deptype`,
			}
			before := make([][][]any, len(queries))
			for i, q := range queries {
				before[i] = fixture.OwnershipSnapshot(t, p, c, q)
			}
			run(fixture.OwnershipError("ALTER EXTENSION unaccent "+verb+" TEXT SEARCH TEMPLATE eo_old.unaccent", "42501", "must be superuser"))
			for i, q := range queries {
				fixture.OwnershipUnchanged(t, p, c, q, before[i])
			}
		}
		run(fixture.Exec(`SET ROLE eo_parser_other`, 0))
		run(fixture.OwnershipError(`ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER eo_old.item`, "42501", `must be owner of extension unaccent`))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`SET ROLE eo_parser_actor`, 0))
		// The actual extension owner also owns the schema, but not an invented
		// parser owner: this ownerless class requires bare superuser authority.
		run(fixture.OwnershipError(`ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER eo_old.item`, "42501", `must be superuser`))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER eo_old.item`, 0))
		run(fixture.Exec(`SET ROLE eo_parser_actor`, 0))
		run(fixture.OwnershipError(`ALTER EXTENSION unaccent DROP TEXT SEARCH PARSER eo_old.item`, "42501", `must be superuser`))
		templateAuthority("DROP")
		run(fixture.Exec(`ALTER EXTENSION unaccent SET SCHEMA eo_old`, 0))
		run(fixture.Exec(`RESET ROLE`, 0))
		// Remove every earlier authority veto through genuine public DDL, while
		// retaining the dictionary's normal dependency on its detached template.
		for _, target := range []string{
			"FUNCTION eo_old.unaccent(text)", "FUNCTION eo_old.unaccent(regdictionary,text)",
			"FUNCTION eo_old.unaccent_init(internal)", "FUNCTION eo_old.unaccent_lexize(internal,internal,internal,internal)",
			"TEXT SEARCH DICTIONARY eo_old.unaccent",
		} {
			run(fixture.Exec("ALTER "+target+" OWNER TO eo_parser_actor", 0))
		}
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP TEXT SEARCH TEMPLATE eo_old.unaccent`, 0))
		run(fixture.Exec(`SET ROLE eo_parser_actor`, 0))
		templateAuthority("ADD")
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_ts_dict x ON x.oid=d.objid WHERE d.classid='pg_ts_dict'::regclass AND d.refclassid='pg_ts_template'::regclass AND d.refobjid=x.dicttemplate AND d.deptype='n' AND x.dictname='unaccent' AND x.dictnamespace='eo_old'::regnamespace`, 1))
		members := fixture.OwnershipSnapshot(t, p, c, ownershipClassMemberSet)
		ownershipMemberCount(run, int64(len(members)))
		queries := []string{ownershipClassMemberSet, ownershipEdges, `SELECT oid::bigint,extnamespace::bigint,extowner::bigint FROM pg_extension WHERE extname='unaccent'`, `SELECT oid::bigint,pronamespace::bigint,proowner::bigint FROM pg_proc WHERE pronamespace IN ('eo_old'::regnamespace,'eo_new'::regnamespace) ORDER BY oid`, `SELECT oid::bigint,dictnamespace::bigint,dictowner::bigint,dicttemplate::bigint FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace IN ('eo_old'::regnamespace,'eo_new'::regnamespace)`, `SELECT oid::bigint,prsnamespace::bigint FROM pg_ts_parser WHERE oid=` + fixture.OwnershipNumber(id)}
		before := make([][][]any, len(queries))
		for i, q := range queries {
			before[i] = fixture.OwnershipSnapshot(t, p, c, q)
		}
		oldRows, err := b.DB.QueryContext(p.Ctx, `SELECT * FROM ts_parse('eo_old.item','Word')`)
		if err != nil {
			t.Fatal(err)
		}
		defer oldRows.Close()
		// A collision exists too: the real ownerless authority check wins first.
		run(fixture.Exec(ownershipParserCreate("eo_new.item"), 0))
		run(fixture.Exec(`SET ROLE eo_parser_actor`, 0))
		run(fixture.OwnershipError(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, "42501", `must be superuser to set schema of text search parser eo_old.item`))
		run(fixture.Exec(`RESET ROLE`, 0))
		for i, q := range queries {
			fixture.OwnershipUnchanged(t, p, c, q, before[i])
		}
		ownershipParserWitness(run, "eo_old")
		run(fixture.Exec(`DROP TEXT SEARCH PARSER eo_new.item`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, 0))
		ownershipMemberCount(run, int64(len(members)))
		ownershipClassMetadataEqual(t, original, ownershipParserSnapshot(t, p, c, id))
		ownershipParserWitness(run, "eo_new")
		run(fixture.OwnershipCount(`SELECT count(*) FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate JOIN pg_depend dep ON dep.classid='pg_ts_dict'::regclass AND dep.objid=d.oid AND dep.refclassid='pg_ts_template'::regclass AND dep.refobjid=t.oid AND dep.deptype='n' WHERE d.dictname='unaccent' AND d.dictnamespace='eo_new'::regnamespace AND t.tmplname='unaccent' AND t.tmplnamespace='eo_old'::regnamespace`, 1))
		run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend dep JOIN pg_ts_template t ON t.oid=dep.objid WHERE dep.classid='pg_ts_template'::regclass AND dep.refclassid='pg_extension'::regclass AND dep.deptype='e' AND t.tmplname='unaccent' AND t.tmplnamespace='eo_old'::regnamespace`, 0))
		ownershipParserOldRows(t, oldRows)
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP TEXT SEARCH PARSER eo_new.item`, 0))
		run(ownershipParserRestrict("eo_new"))
	})
}

func ownershipParserSeedMembership(t *testing.T) {
	fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := fixture.OwnershipRun(t, p, b, c)
		// This runs before the scenario's extension/schema teardown on every
		// failure. Never retire an extension while its member is the pinned seed.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, q := range []string{`ROLLBACK`, `RESET ROLE`, `RESET SESSION AUTHORIZATION`} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("seed cleanup", q, err)
				}
			}
			var member bool
			if err := c.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_ts_parser'::regclass AND d.objid=3722 AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname='unaccent')`).Scan(&member); err != nil {
				t.Error("seed cleanup membership", err)
			} else if member {
				if _, err := c.ExecContext(ctx, `ALTER EXTENSION unaccent DROP TEXT SEARCH PARSER pg_catalog.default`); err != nil {
					t.Error("seed cleanup detach", err)
				}
			}
		}()
		ownershipSetup(run)
		state := `SELECT oid::bigint,prsnamespace::bigint,prsname,prsstart::oid::bigint,prstoken::oid::bigint,prsend::oid::bigint,prsheadline::oid::bigint,prslextype::oid::bigint FROM pg_ts_parser WHERE oid=3722`
		before := fixture.OwnershipSnapshot(t, p, c, state)
		run(fixture.OwnershipCount(`SELECT count(*) FROM pg_ts_parser WHERE oid=3722 AND prsnamespace='pg_catalog'::regnamespace AND prsname='default' AND prsstart::oid=3717 AND prstoken::oid=3718 AND prsend::oid=3719 AND prsheadline::oid=3720 AND prslextype::oid=3721`, 1))
		dependencies := `SELECT refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid='pg_ts_parser'::regclass AND objid=3722 ORDER BY refclassid,refobjid,refobjsubid,deptype`
		edges := fixture.OwnershipSnapshot(t, p, c, dependencies)
		members := fixture.OwnershipSnapshot(t, p, c, ownershipClassMemberSet)
		for range 2 {
			run(fixture.Exec(`ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER pg_catalog.default`, 0))
			ownershipMemberCount(run, 7)
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_ts_parser'::regclass AND objid=3722 AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND deptype='e'`, 1))
			for _, suffix := range []string{"", " CASCADE"} {
				// dependency.c rejects a pinned target before inspecting its e link.
				run(fixture.OwnershipError(`DROP TEXT SEARCH PARSER pg_catalog.default`+suffix, "2BP01", `cannot drop text search parser "default" because it is required by the database system`))
			}
			run(fixture.Exec(`ALTER EXTENSION unaccent DROP TEXT SEARCH PARSER pg_catalog.default`, 0))
			ownershipMemberCount(run, 6)
			fixture.OwnershipUnchanged(t, p, c, ownershipClassMemberSet, members)
			fixture.OwnershipUnchanged(t, p, c, state, before)
			fixture.OwnershipUnchanged(t, p, c, dependencies, edges)
			for _, suffix := range []string{"", " CASCADE"} {
				run(fixture.OwnershipError(`DROP TEXT SEARCH PARSER pg_catalog.default`+suffix, "2BP01", `cannot drop text search parser "default" because it is required by the database system`))
			}
			run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_ts_parser'::regclass AND objid=3722 AND refclassid='pg_extension'::regclass AND deptype='e'`, 0))
			for _, s := range []fixture.Step{
				fixture.Select(`SELECT * FROM ts_parse('pg_catalog.default','Word')`, []string{"tokid", "token"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Word"}}),
				fixture.Select(`SELECT to_tsvector('pg_catalog.simple','Word') AS value`, []string{"value"}, []string{"TSVECTOR"}, [][]any{{"'word':1"}}),
			} {
				run(s)
				run(fixture.Prepared(s))
			}
		}
	})
}
