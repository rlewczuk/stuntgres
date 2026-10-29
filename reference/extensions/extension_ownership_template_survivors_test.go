package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Each corpus/state owns one bounded source scenario shared by both native
// destinations. All shared SQL runs on both backends.
func ownershipTemplateSurvivors(t *testing.T, controls bool) {
	phase := "source"
	if controls {
		phase = ""
	}
	for _, family := range []string{"ordinary_simple", "ordinary_unaccent", "installed"} {
		t.Run(family, func(t *testing.T) {
			for _, foreign := range []bool{false, true} {
				state := "detached"
				if foreign {
					state = "foreign"
				}
				t.Run(state, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
					ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						ownershipTemplateSurvivor(t, p, b, c, family, foreign, phase)
					})
				})
			}
		})
	}
}

func ownershipTemplateSurvivor(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, family string, foreign bool, phase string) {
	run := ownershipRun(t, p, b, c)
	ownershipSurvivorRoles(run)
	run(fixture.Exec(`CREATE EXTENSION unaccent SCHEMA eo_old`, 0))
	name, methods, options := "eo_old.ordinary", templateSimpleMethods, "STOPWORDS=english"
	init, lexize := "pg_catalog.dsimple_init(internal)", "pg_catalog.dsimple_lexize(internal,internal,internal,internal)"
	input, want := "WORD", "{word}"
	if family != "ordinary_simple" {
		methods, options = "INIT=eo_old.unaccent_init,LEXIZE=eo_old.unaccent_lexize", "RULES=unaccent"
		init, lexize = "eo_old.unaccent_init(internal)", "eo_old.unaccent_lexize(internal,internal,internal,internal)"
		input, want = "Hôtel", "{Hotel}"
	}
	if family == "installed" {
		name = "eo_old.unaccent"
	} else {
		run(fixture.Prepared(fixture.Exec(`CREATE TEXT SEARCH TEMPLATE `+name+` (`+methods+`)`, 0)))
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD TEXT SEARCH TEMPLATE `+name, 0))
	}
	idQuery := `SELECT oid::bigint,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE tmplname='` + strings.TrimPrefix(name, "eo_old.") + `' AND tmplnamespace='eo_old'::regnamespace`
	ids := ownershipSnapshot(t, p, c, idQuery)
	if len(ids) != 1 {
		t.Fatal("missing genuine template", ids)
	}
	for _, receiver := range []string{"unaccent", `"uuid-ossp"`} {
		run(ownershipError(`ALTER EXTENSION `+receiver+` ADD TEXT SEARCH TEMPLATE `+name, "55000", `text search template `+name+` is already a member of extension "unaccent"`))
	}
	run(ownershipError(`ALTER EXTENSION "uuid-ossp" DROP TEXT SEARCH TEMPLATE `+name, "55000", `text search template `+name+` is not a member of extension "uuid-ossp"`))
	for _, suffix := range []string{"", " CASCADE"} {
		s := ownershipError(`DROP TEXT SEARCH TEMPLATE `+name+suffix, "2BP01", `cannot drop text search template `+name+` because extension unaccent requires it`)
		s.Diagnostic.Hint = "You can drop extension unaccent instead."
		run(s)
	}
	run(fixture.Exec(`CREATE TEXT SEARCH DICTIONARY eo_new.witness (TEMPLATE=`+name+`,`+options+`)`, 0))
	query := `SELECT ts_lexize('eo_new.witness',$1::text) AS lexemes`
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	templateLexemes(t, p, held, b.Recorder, b.PG, query, want, input)
	// Pinned builtins have no normal pg_depend row, but their actual identities
	// remain executable while lawfully receiving ordinary extension membership.
	if family == "ordinary_simple" {
		run(fixture.Exec(`BEGIN`, 0))
		defer c.ExecContext(p.Ctx, `ROLLBACK`)
		for _, signature := range []string{init, lexize} {
			run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION `+signature, 0))
			run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='uuid-ossp') AND deptype='e'`, 1))
		}
		templateLexemes(t, p, held, b.Recorder, b.PG, query, want, input)
		run(fixture.Exec(`CREATE TEXT SEARCH DICTIONARY eo_new.member_witness (TEMPLATE=`+name+`,`+options+`)`, 0))
		templateLexemes(t, p, c, b.Recorder, b.PG, `SELECT ts_lexize('eo_new.member_witness',$1::text) AS lexemes`, want, input)
		run(fixture.Exec(`ROLLBACK`, 0))
	}
	// Membership rollback preserves all three actual descriptor identities.
	run(fixture.Exec(`BEGIN`, 0))
	run(fixture.Exec(`ALTER EXTENSION unaccent DROP TEXT SEARCH TEMPLATE `+name, 0))
	run(fixture.Exec(`ROLLBACK`, 0))
	ownershipUnchanged(t, p, c, idQuery, ids)
	run(fixture.Exec(`ALTER EXTENSION unaccent DROP TEXT SEARCH TEMPLATE `+name, 0))
	run(fixture.Exec(`ALTER TEXT SEARCH TEMPLATE `+name+` RENAME TO survivor`, 0))
	run(fixture.Exec(`ALTER TEXT SEARCH TEMPLATE eo_old.survivor SET SCHEMA eo_new`, 0))
	name = "eo_new.survivor"
	if family != "ordinary_simple" {
		for _, callback := range []struct{ old, renamed, args string }{
			{"unaccent_init", "survivor_init", "internal"},
			{"unaccent_lexize", "survivor_lexize", "internal,internal,internal,internal"},
		} {
			signature := "eo_old." + callback.old + "(" + callback.args + ")"
			run(fixture.Exec(`ALTER EXTENSION unaccent DROP FUNCTION `+signature, 0))
			run(fixture.Exec(`ALTER FUNCTION `+signature+` RENAME TO `+callback.renamed, 0))
			run(fixture.Exec(`ALTER FUNCTION eo_old.`+callback.renamed+`(`+callback.args+`) SET SCHEMA eo_new`, 0))
			signature = "eo_new." + callback.renamed + "(" + callback.args + ")"
			ownershipSurvivorACL(run, signature)
			if foreign {
				run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION `+signature, 0))
			}
		}
		init, lexize = "eo_new.survivor_init(internal)", "eo_new.survivor_lexize(internal,internal,internal,internal)"
	}
	if foreign {
		run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TEXT SEARCH TEMPLATE `+name, 0))
	}
	run(fixture.Exec(`DROP EXTENSION unaccent`, 0))
	finalIDs := `SELECT oid::bigint,tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE tmplname='survivor' AND tmplnamespace='eo_new'::regnamespace`
	ownershipUnchanged(t, p, c, finalIDs, ids)
	templateLexemes(t, p, held, b.Recorder, b.PG, query, want, input)
	if family == "ordinary_simple" {
		// Optional INIT is a definition-only contract: dsimple_lexize cannot
		// safely run without the genuine initialization state.
		run(fixture.Exec(`CREATE TEXT SEARCH TEMPLATE eo_new.optional (LEXIZE=pg_catalog.dsimple_lexize)`, 0))
		run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TEXT SEARCH TEMPLATE eo_new.optional`, 0))
	}
	declarations := []string{}
	if family != "ordinary_simple" {
		declarations = []string{init, lexize}
	}
	check := func(conn *sql.Conn, recorder *fixture.Recorder) {
		r := ownershipRunWithRecorder(t, p, conn, recorder)
		r(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent'`, 0))
		if family == "ordinary_simple" {
			r(ownershipCount(`SELECT count(*) FROM pg_ts_template t JOIN pg_depend d ON d.classid='pg_ts_template'::regclass AND d.objid=t.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE t.tmplname='optional' AND t.tmplnamespace='eo_new'::regnamespace AND t.tmplinit::oid=0 AND t.tmpllexize='pg_catalog.dsimple_lexize(internal,internal,internal,internal)'::regprocedure AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname='uuid-ossp'`, 1))
		}
		r(ownershipCount(`SELECT count(*) FROM pg_ts_template WHERE tmplname='survivor' AND tmplnamespace='eo_new'::regnamespace AND tmplinit='`+init+`'::regprocedure AND tmpllexize='`+lexize+`'::regprocedure`, 1))
		templateID := `(SELECT oid FROM pg_ts_template WHERE tmplname='survivor' AND tmplnamespace='eo_new'::regnamespace)`
		member := ""
		if foreign {
			member = "uuid-ossp"
		}
		r(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of FROM pg_ts_template t LEFT JOIN pg_depend d ON d.classid='pg_ts_template'::regclass AND d.objid=t.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE t.oid=`+templateID, []string{"member_of"}, []string{"TEXT"}, [][]any{{member}}))
		r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_ts_template'::regclass AND objid=`+templateID+` AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1))
		helperCount := int64(0)
		if family != "ordinary_simple" {
			helperCount = 2
		}
		r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_ts_template'::regclass AND objid=`+templateID+` AND refclassid='pg_proc'::regclass AND deptype='n' AND (refobjid='`+init+`'::regprocedure OR refobjid='`+lexize+`'::regprocedure)`, helperCount))
		r(ownershipCount(`SELECT count(*) FROM pg_ts_dict d JOIN pg_depend dep ON dep.classid='pg_ts_dict'::regclass AND dep.objid=d.oid AND dep.refclassid='pg_ts_template'::regclass AND dep.refobjid=d.dicttemplate AND dep.deptype='n' WHERE d.dictname='witness' AND d.dictnamespace='eo_new'::regnamespace AND d.dicttemplate=`+templateID, 1))
		for i, signature := range declarations {
			ownershipSurvivorLinks(r, signature, foreign)
			args, source := "2281", "unaccent_init"
			if i == 1 {
				args, source = "2281 2281 2281 2281", "unaccent_lexize"
			}
			r(fixture.Select(`SELECT p.proargtypes::text,p.prorettype::bigint,p.prosrc,p.probin,l.lanname,p.proisstrict,p.provolatile,p.proretset FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='`+signature+`'::regprocedure`, []string{"proargtypes", "prorettype", "prosrc", "probin", "lanname", "proisstrict", "provolatile", "proretset"}, []string{"TEXT", "INT8", "TEXT", "TEXT", "NAME", "BOOL", "CHAR", "BOOL"}, [][]any{{args, int64(2281), source, "$libdir/unaccent", "c", false, "v", false}}))
			if foreign {
				ownershipSurvivorMemberRestriction(r, signature, "uuid-ossp")
			} else {
				s := ownershipError("DROP FUNCTION "+signature, "2BP01", "cannot drop function "+signature+" because other objects depend on it")
				s.Diagnostic.Detail = "text search template eo_new.survivor depends on function " + signature + "\ntext search dictionary eo_new.witness depends on text search template eo_new.survivor"
				s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				r(s)
			}
		}
		for _, value := range []struct{ input, want any }{{input, want}, {nil, nil}} {
			templateLexemes(t, p, conn, recorder, b.PG, query, value.want, value.input)
			r(fixture.Prepared(fixture.Select(query, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{value.want}}, value.input)))
			s, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			templateLexemes(t, p, fixture.HeldStatement{Executor: conn, Statement: s, SQL: query}, recorder, b.PG, query, value.want, value.input)
			s.Close()
		}
		r(fixture.Exec(`SET ROLE eo_survivor_acl`, 0))
		r(ownershipError(`ALTER TEXT SEARCH TEMPLATE eo_new.survivor RENAME TO forbidden`, "42501", "must be superuser to rename text search template eo_new.survivor"))
		r(fixture.Exec(`RESET ROLE`, 0))
		if foreign {
			for _, suffix := range []string{"", " CASCADE"} {
				s := ownershipError(`DROP TEXT SEARCH TEMPLATE eo_new.survivor`+suffix, "2BP01", `cannot drop text search template eo_new.survivor because extension uuid-ossp requires it`)
				s.Diagnostic.Hint = "You can drop extension uuid-ossp instead."
				r(s)
			}
			r(fixture.Exec(`BEGIN`, 0))
			defer conn.ExecContext(p.Ctx, `ROLLBACK`)
			r(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP TEXT SEARCH TEMPLATE eo_new.survivor`, 0))
			// Each rejected route aborts PostgreSQL's transaction. Restore the
			// savepoint before the next route while retaining the detached state.
			for _, prepared := range []bool{false, true} {
				r(fixture.Exec(`SAVEPOINT template_restrict`, 0))
				s := ownershipError(`DROP TEXT SEARCH TEMPLATE eo_new.survivor RESTRICT`, "2BP01", "cannot drop text search template eo_new.survivor because other objects depend on it")
				s.Diagnostic.Detail = "text search dictionary eo_new.witness depends on text search template eo_new.survivor"
				s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				s.Prepared, s.ErrorStage = prepared, "execute"
				r(s)
				r(fixture.Exec(`ROLLBACK TO SAVEPOINT template_restrict`, 0))
				r(fixture.Exec(`RELEASE SAVEPOINT template_restrict`, 0))
			}
			r(fixture.Exec(`ROLLBACK`, 0))
		} else {
			templateRestrict(t, p, conn, recorder, name, "eo_new.witness")
		}
	}
	ownershipTemplateControls(t, p, b, c, phase, family, query, input, want, declarations, check)
}

func ownershipTemplateControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, phase, family, query, input, want string, signatures []string, check func(*sql.Conn, *fixture.Recorder)) {
	t.Helper()
	check(c, b.Recorder)
	if b.PG || phase == "source" {
		return
	}
	saved := extensionDump(t, p.Ctx, p.Native)
	before := make([][][]any, len(signatures))
	for i, signature := range signatures {
		before[i] = ownershipSnapshot(t, p, c, ownershipSurvivorDeclaration(signature))
	}
	var retainedRows []*sql.Rows
	// Both resources belong to the original source, not a restored reconstruction.
	for range 2 {
		oldRows, err := c.QueryContext(p.Ctx, query, input)
		if err != nil {
			t.Fatal(err)
		}
		defer oldRows.Close()
		retainedRows = append(retainedRows, oldRows)
	}
	restore := func(independent bool) {
		control, recorded, recorder := p.Native, b.DB, b.Recorder
		if independent {
			connector, err := stuntgres.NewDriver().OpenConnector("eo_template_" + family)
			if err != nil {
				t.Fatal(err)
			}
			recorded, recorder = fixture.RecordedNativePool(t, connector)
			control = sql.OpenDB(connector)
			defer control.Close()
			for _, q := range []string{`CREATE ROLE eo_survivor_owner SUPERUSER`, `CREATE ROLE eo_survivor_acl`} {
				if _, err := control.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := stuntgres.Reset(p.Ctx, control); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		conn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		check(conn, recorder)
		for i, signature := range signatures {
			ownershipUnchanged(t, p, conn, ownershipSurvivorDeclaration(signature), before[i])
		}
		if again := extensionDump(t, p.Ctx, control); again != saved {
			t.Fatal("template survivor redump changed bytes")
		}
		for _, invalid := range []string{strings.Replace(saved, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1), saved + "\nCREATE EXTENSION missing_provider;\n"} {
			if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(invalid)); err == nil {
				t.Fatal("invalid template restore accepted")
			}
			if after := extensionDump(t, p.Ctx, control); after != saved {
				t.Fatal("failed template restore published partial state")
			}
			check(conn, recorder)
		}
	}
	if phase == "" || phase == "same_driver" {
		restore(false)
	}
	if phase == "" || phase == "independent_driver" {
		restore(true)
	}
	for _, oldRows := range retainedRows {
		columns, err := oldRows.ColumnTypes()
		if err != nil || len(columns) != 1 || columns[0].Name() != "lexemes" || columns[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal("retained template metadata", columns, err)
		}
		var got string
		if !oldRows.Next() {
			t.Fatal("lost retained template row", oldRows.Err())
		}
		if err := oldRows.Scan(&got); err != nil || got != want {
			t.Fatal("retained template value", got, err)
		}
		if oldRows.Next() || oldRows.Err() != nil {
			t.Fatal("retained template EOF", oldRows.Err())
		}
	}
}
