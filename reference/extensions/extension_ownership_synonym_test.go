package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Reuse the owning seed identity/target helpers, not the Snowball morphology
// corpus. These are fixed pins; synonym dictionaries themselves are ordinary.
var ownershipSynonymSeeds = []ownershipSnowballSeed{
	{"FUNCTION", "pg_proc", "pronamespace", "proname", "dsynonym_init", "(internal)", "unaccent"},
	{"FUNCTION", "pg_proc", "pronamespace", "proname", "dsynonym_lexize", "(internal,internal,internal,internal)", ""},
	{"TEXT SEARCH TEMPLATE", "pg_ts_template", "tmplnamespace", "tmplname", "synonym", "", "plpgsql"},
}

func ownershipSynonymCleanup(t *testing.T, c *sql.Conn) func() {
	t.Helper()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Error("synonym seed cleanup", q, err)
			}
		}
		for _, s := range ownershipSynonymSeeds {
			var id int64
			var name string
			if err := c.QueryRowContext(ctx, "SELECT oid::bigint,"+s.name+" FROM "+s.table+" WHERE "+s.namespace+"='pg_catalog'::regnamespace AND "+s.name+" IN ($1,$2)", s.original, "eo_"+s.original).Scan(&id, &name); err != nil {
				t.Error("synonym seed cleanup identity", s.original, err)
				continue
			}
			target := s.kind + " pg_catalog." + name + s.signature
			var receiver string
			err := c.QueryRowContext(ctx, "SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=$1::regclass AND d.objid=$2::oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e'", s.table, id).Scan(&receiver)
			if err == nil {
				if _, err := c.ExecContext(ctx, "ALTER EXTENSION "+receiver+" DROP "+target); err != nil {
					t.Error("synonym seed cleanup detach", err)
				}
			} else if err != sql.ErrNoRows {
				t.Error("synonym seed cleanup membership", err)
			}
			if name != s.original {
				if _, err := c.ExecContext(ctx, "ALTER "+target+" RENAME TO "+s.original); err != nil {
					t.Error("synonym seed cleanup rename", err)
				}
			}
		}
	}
}

func ownershipSynonymValues(run func(fixture.Step)) {
	for _, tc := range []struct{ input, want any }{{"POSTGRES", "{pgsql}"}, {"indices", "{index}"}, {nil, nil}} {
		s := fixture.Select("SELECT ts_lexize('eo_old.synonym_item'::regdictionary,$1::text) AS value", []string{"value"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input)
		run(s)
		run(fixture.Prepared(s))
	}
}

func ownershipSynonymWitness(run func(fixture.Step)) {
	ownershipSynonymValues(run)
	run(ownershipCount("SELECT count(*) FROM pg_ts_dict WHERE oid='eo_old.synonym_item'::regdictionary AND dictowner=current_user::regrole AND dicttemplate=3730 AND dictinitoption=$1", 1, "synonyms = 'synonym_sample'"))
	run(ownershipCount("SELECT count(*) FROM pg_ts_template WHERE oid=3730 AND tmplnamespace='pg_catalog'::regnamespace AND tmplname='eo_synonym' AND tmplinit=3728 AND tmpllexize=3729", 1))
	for i, s := range ownershipSynonymSeeds {
		n := int64(1)
		if s.receiver == "" {
			n = 0
		}
		run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+s.table+"'::regclass AND objid=("+s.lookup(true)+") AND refclassid='pg_extension'::regclass AND deptype='e'", n))
		run(ownershipCount("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='"+s.table+"'::regclass AND d.objid=("+s.lookup(true)+") AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname=$1", n, s.receiver))
		// Fixed callbacks are pinned references; their real template has no n
		// rows to them. Pinning also precedes e restrictions on object DROP.
		run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+s.table+"'::regclass AND objid=("+s.lookup(true)+") AND deptype='n'", 0))
		blocked := ownershipError("DROP "+s.target(true)+" CASCADE", "2BP01", "cannot drop "+strings.ToLower(s.kind)+" eo_"+s.original+s.signature+" because it is required by the database system")
		run(blocked)
		if i < 2 {
			args := "2281"
			if i == 1 {
				args = "2281 2281 2281 2281"
			}
			run(ownershipCount("SELECT count(*) FROM pg_proc WHERE oid=("+s.lookup(true)+") AND proargtypes::text=$1 AND prorettype=2281 AND proisstrict AND provolatile='i' AND proparallel='s' AND prosrc=$2 AND prolang=(SELECT oid FROM pg_language WHERE lanname='internal')", 1, args, s.original))
		}
	}
	run(ownershipCount("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_ts_dict'::regclass AND d.objid='eo_old.synonym_item'::regdictionary AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname='unaccent'", 1))
	blocked := ownershipError("DROP TEXT SEARCH DICTIONARY eo_old.synonym_item CASCADE", "2BP01", "cannot drop text search dictionary eo_old.synonym_item because extension unaccent requires it")
	blocked.Diagnostic.Hint = "You can drop extension unaccent instead."
	run(blocked)
	ownershipDictionaryTemplateDependency(run, "eo_old.synonym_item")
	run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='pg_ts_dict'::regclass AND objid='eo_old.synonym_item'::regdictionary AND refclassid='pg_namespace'::regclass AND refobjid='eo_old'::regnamespace AND deptype='n'", 1))
}

func ownershipSynonymMetadata(t *testing.T, p *fixture.Pair, c *sql.Conn) [][][]any {
	var out [][][]any
	for _, s := range ownershipSynonymSeeds {
		class := int64(1255)
		if s.table == "pg_ts_template" {
			class = 3764
		}
		out = append(out, ownershipClassMetadata(t, p, c, ownershipClassCase{class: class}, ownershipID(t, p, c, s.lookup(true)))...)
	}
	out = append(out, ownershipClassMetadata(t, p, c, ownershipClassCase{class: 3600}, ownershipID(t, p, c, "SELECT 'eo_old.synonym_item'::regdictionary::bigint"))...)
	return out
}

func ownershipSynonymBootstrapMembership(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ownershipSynonymCleanup(t, c)()
		run := ownershipRun(t, p, b, c)
		ownershipSetup(run)
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY eo_old.synonym_item (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample,CaseSensitive=true)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY eo_old.synonym_item (casesensitive)", 0))
		ownershipSynonymValues(run)
		const q = "SELECT ts_lexize('eo_old.synonym_item'::regdictionary,$1::text) AS value"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		checkHeld := func() {
			for _, tc := range []struct{ input, want any }{{"POSTGRES", "{pgsql}"}, {nil, nil}} {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Select(q, []string{"value"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input))
			}
		}
		checkHeld()
		for _, s := range ownershipSynonymSeeds {
			id := ownershipID(t, p, c, s.lookup(false))
			if s.receiver != "" {
				run(fixture.Exec("ALTER EXTENSION "+s.receiver+" ADD "+s.target(false), 0))
			}
			run(fixture.Exec("ALTER "+s.target(false)+" RENAME TO eo_"+s.original, 0))
			if ownershipID(t, p, c, s.lookup(true)) != id {
				t.Fatal("synonym seed mutation replaced fixed identity", s.original)
			}
		}
		run(fixture.Exec("ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY eo_old.synonym_item", 0))
		ownershipSynonymWitness(run)
		metadata := ownershipSynonymMetadata(t, p, c)
		checkHeld()
		ownershipSynonymDetach(run)
		checkHeld()
		ownershipClassMetadataEqual(t, metadata, ownershipSynonymMetadata(t, p, c))
		for _, s := range ownershipSynonymSeeds {
			if s.receiver != "" {
				run(fixture.Exec("ALTER EXTENSION "+s.receiver+" ADD "+s.target(true), 0))
			}
		}
		run(fixture.Exec("ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY eo_old.synonym_item", 0))
		ownershipSynonymWitness(run)
		if !b.PG {
			ownershipSynonymControls(t, p, b, c, metadata)
		}
	})
}

func ownershipSynonymControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, metadata [][][]any) {
	saved := extensionDump(t, p.Ctx, p.Native)
	if !strings.Contains(saved, `CREATE TEXT SEARCH DICTIONARY "eo_old"."synonym_item"`) {
		t.Fatal("ordinary synonym member dictionary CREATE omitted")
	}
	var oldRows []*sql.Rows
	for range 2 {
		rows, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('eo_old.synonym_item','POSTGRES') AS value")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		oldRows = append(oldRows, rows)
	}
	restore := func(control, recorded *sql.DB, recorder *fixture.Recorder) {
		conn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer ownershipStockCleanup(t, conn)()
		defer ownershipSynonymCleanup(t, conn)()
		if err := stuntgres.Reset(p.Ctx, control); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
			t.Fatal("synonym membership replay", err)
		}
		run := ownershipRunWithRecorder(t, p, conn, recorder)
		const q = "SELECT ts_lexize('eo_old.synonym_item'::regdictionary,$1::text) AS value"
		held, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func() {
			ownershipSynonymWitness(run)
			ownershipClassMetadataEqual(t, metadata, ownershipSynonymMetadata(t, p, conn))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, recorder, fixture.Select(q, []string{"value"}, []string{"_TEXT"}, [][]any{{"{pgsql}"}}, "POSTGRES"))
		}
		check()
		if again := extensionDump(t, p.Ctx, control); again != saved {
			t.Fatal("synonym membership replay changed dump bytes")
		}
		for _, invalid := range []string{strings.Replace(saved, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1), saved + "\nCREATE EXTENSION missing_provider;\n"} {
			if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(invalid)); err == nil {
				t.Fatal("invalid synonym membership restore accepted")
			}
			if after := extensionDump(t, p.Ctx, control); after != saved {
				t.Fatal("failed synonym membership restore changed destination")
			}
			check()
		}
		rows := oldRows[0]
		oldRows = oldRows[1:]
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 || columns[0].Name() != "value" || columns[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal("retained synonym metadata", columns, err)
		}
		var value string
		if !rows.Next() {
			t.Fatal("lost retained synonym row", rows.Err())
		}
		if err := rows.Scan(&value); err != nil || value != "{pgsql}" {
			t.Fatal("retained synonym value", value, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("retained synonym EOF", rows.Err())
		}
		ownershipSynonymDetach(run)
		ownershipClassMetadataEqual(t, metadata, ownershipSynonymMetadata(t, p, conn))
	}
	restore(p.Native, b.DB, b.Recorder)
	connector, err := stuntgres.NewDriver().OpenConnector("eo_synonym_membership")
	if err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	other := sql.OpenDB(connector)
	defer other.Close()
	restore(other, recorded, recorder)
}

func ownershipSynonymDetach(run func(fixture.Step)) {
	for _, s := range ownershipSynonymSeeds {
		if s.receiver != "" {
			run(fixture.Exec("ALTER EXTENSION "+s.receiver+" DROP "+s.target(true), 0))
		}
		run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+s.table+"'::regclass AND objid=("+s.lookup(true)+") AND refclassid='pg_extension'::regclass AND deptype='e'", 0))
		run(ownershipError("DROP "+s.target(true)+" CASCADE", "2BP01", "cannot drop "+strings.ToLower(s.kind)+" eo_"+s.original+s.signature+" because it is required by the database system"))
	}
	run(fixture.Exec("ALTER EXTENSION unaccent DROP TEXT SEARCH DICTIONARY eo_old.synonym_item", 0))
	run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='pg_ts_dict'::regclass AND objid='eo_old.synonym_item'::regdictionary AND refclassid='pg_extension'::regclass AND deptype='e'", 0))
	ownershipSynonymValues(run)
}
