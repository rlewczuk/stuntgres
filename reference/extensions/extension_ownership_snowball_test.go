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

func ownershipSnowballDictionaryWitness(run func(fixture.Step), dictionary string, stopwords bool) {
	word := "{}"
	if !stopwords {
		word = "{the}"
	}
	for _, tc := range []struct {
		input any
		want  any
	}{{"RATS", "{rat}"}, {"the", word}, {nil, nil}} {
		s := fixture.Select("SELECT ts_lexize('"+dictionary+"'::regdictionary,$1::text) AS value", []string{"value"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input)
		run(s)
		run(fixture.Prepared(s))
	}
}

type ownershipSnowballSeed struct {
	kind, table, namespace, name, original, signature, receiver string
}

// The receivers already exist as actual extension installations. No Snowball
// installation header, callback shim or synthetic bootstrap declaration exists.
var ownershipSnowballSeeds = []ownershipSnowballSeed{
	{"FUNCTION", "pg_proc", "pronamespace", "proname", "dsnowball_init", "(internal)", "unaccent"},
	{"FUNCTION", "pg_proc", "pronamespace", "proname", "dsnowball_lexize", "(internal,internal,internal,internal)", ""},
	{"TEXT SEARCH TEMPLATE", "pg_ts_template", "tmplnamespace", "tmplname", "snowball", "", "plpgsql"},
	{"TEXT SEARCH DICTIONARY", "pg_ts_dict", "dictnamespace", "dictname", "english_stem", "", "unaccent"},
	{"TEXT SEARCH CONFIGURATION", "pg_ts_config", "cfgnamespace", "cfgname", "english", "", "plpgsql"},
}

func (s ownershipSnowballSeed) lookup(moved bool) string {
	schema, name := "pg_catalog", s.original
	if moved {
		name = "eo_" + name
	}
	return "SELECT oid::bigint FROM " + s.table + " WHERE " + s.namespace + "='" + schema + "'::regnamespace AND " + s.name + "='" + name + "'"
}

func (s ownershipSnowballSeed) target(moved bool) string {
	if moved {
		return s.kind + " pg_catalog.eo_" + s.original + s.signature
	}
	return s.kind + " pg_catalog." + s.original + s.signature
}

// Register this before the first source mutation, and on each replay destination
// before Restore. Resolve actual current identities rather than source OIDs after remap.
// No seed is dropped during cleanup, including on assertion failure.
func ownershipSnowballCleanup(t *testing.T, c *sql.Conn) func() {
	t.Helper()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exec := func(q string) {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Error("Snowball seed cleanup", q, err)
			}
		}
		for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
			exec(q)
		}
		for _, s := range ownershipSnowballSeeds {
			var id int64
			var schema, name string
			q := "SELECT o.oid::bigint,n.nspname,o." + s.name + " FROM " + s.table + " o JOIN pg_namespace n ON n.oid=o." + s.namespace + " WHERE n.nspname='pg_catalog' AND o." + s.name + " IN ('" + s.original + "','eo_" + s.original + "')"
			if err := c.QueryRowContext(ctx, q).Scan(&id, &schema, &name); err != nil {
				t.Error("Snowball seed cleanup identity", s.original, err)
				continue
			}
			target := s.kind + " " + schema + "." + name + s.signature
			var receiver string
			err := c.QueryRowContext(ctx, "SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=$1::regclass AND d.objid=$2::oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e'", s.table, id).Scan(&receiver)
			if err == nil {
				exec("ALTER EXTENSION " + receiver + " DROP " + target)
			} else if err != sql.ErrNoRows {
				t.Error("Snowball seed cleanup membership", err)
			}
			if name != s.original {
				exec("ALTER " + s.kind + " pg_catalog." + name + s.signature + " RENAME TO " + s.original)
			}
		}
		// Owners/ACLs and mappings are never mutated; only dictionary options
		// change. Restore those before any receiver/schema teardown can run.
		exec("ALTER TEXT SEARCH DICTIONARY pg_catalog.english_stem (Language=english,StopWords=english)")
	}
}

func ownershipSnowballBootstrapMembership(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ownershipSnowballCleanup(t, c)()
		run := ownershipRun(t, p, b, c)
		ownershipSetup(run)
		ids := make([]int64, len(ownershipSnowballSeeds))
		for i, s := range ownershipSnowballSeeds {
			ids[i] = ownershipID(t, p, c, s.lookup(false))
			if ids[i] <= 0 {
				t.Fatal("missing real Snowball seed", s.original)
			}
		}
		owners := ownershipSnowballOwners(t, p, c, false)
		mappings := ownershipSnapshot(t, p, c, "SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='pg_catalog.english'::regconfig ORDER BY maptokentype,mapseqno")
		ownershipSnowballDictionaryWitness(run, "pg_catalog.english_stem", true)
		run(fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='pg_catalog.english_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'english', stopwords = 'english'"}}))
		// Keep a real held, parameterized consumer across membership and rename.
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS value"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, s := range ownershipSnowballSeeds {
			if s.receiver != "" {
				run(fixture.Exec("ALTER EXTENSION "+s.receiver+" ADD "+s.target(false), 0))
			}
		}
		ownershipSnowballDictionaryWitness(run, "pg_catalog.english_stem", true)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Select(q, []string{"value"}, []string{"_TEXT"}, [][]any{{"{rat}"}}, "pg_catalog.english_stem", "RATS"))
		for i, s := range ownershipSnowballSeeds {
			run(fixture.Exec("ALTER "+s.target(false)+" RENAME TO eo_"+s.original, 0))
			if ownershipID(t, p, c, s.lookup(true)) != ids[i] {
				t.Fatal("seed ALTER replaced identity", s.original)
			}
		}
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY pg_catalog.eo_english_stem (stopwords)", 0))
		ownershipSnowballSeedWitness(run)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Select(q, []string{"value"}, []string{"_TEXT"}, [][]any{{"{the}"}}, "pg_catalog.eo_english_stem", "the"))
		ownershipClassMetadataEqual(t, owners, ownershipSnowballOwners(t, p, c, true))
		ownershipUnchanged(t, p, c, "SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='pg_catalog.eo_english'::regconfig ORDER BY maptokentype,mapseqno", mappings)
		metadata := ownershipSnowballMetadata(t, p, c)
		// Exercise lawful detach and reattachment on the real source as well,
		// without restoring options, callbacks or mappings from a copy.
		for _, s := range ownershipSnowballSeeds {
			if s.receiver != "" {
				run(fixture.Exec("ALTER EXTENSION "+s.receiver+" DROP "+s.target(true), 0))
			}
			run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+s.table+"'::regclass AND objid=("+s.lookup(true)+") AND refclassid='pg_extension'::regclass AND deptype='e'", 0))
		}
		ownershipSnowballDictionaryWitness(run, "pg_catalog.eo_english_stem", false)
		ownershipSnowballDetachedDrops(run)
		ownershipClassMetadataEqual(t, metadata, ownershipSnowballMetadata(t, p, c))
		for _, s := range ownershipSnowballSeeds {
			if s.receiver != "" {
				run(fixture.Exec("ALTER EXTENSION "+s.receiver+" ADD "+s.target(true), 0))
			}
		}
		ownershipSnowballSeedWitness(run)
		if !b.PG {
			ownershipSnowballSeedControls(t, p, b, c, metadata)
		}
	})
}

func ownershipSnowballSeedWitness(run func(fixture.Step)) {
	ownershipSnowballDictionaryWitness(run, "pg_catalog.eo_english_stem", false)
	for _, tc := range []struct {
		input any
		want  any
	}{{"the RATS", "'rat':2 'the':1"}, {nil, nil}} {
		s := fixture.Select("SELECT to_tsvector('pg_catalog.eo_english'::regconfig,$1::text) AS value", []string{"value"}, []string{"TSVECTOR"}, [][]any{{tc.want}}, tc.input)
		run(s)
		run(fixture.Prepared(s))
	}
	for _, s := range ownershipSnowballSeeds {
		id := "(" + s.lookup(true) + ")"
		n := int64(1)
		if s.receiver == "" {
			n = 0
		}
		run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+s.table+"'::regclass AND objid="+id+" AND refclassid='pg_extension'::regclass AND deptype='e'", n))
		run(ownershipCount("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='"+s.table+"'::regclass AND d.objid="+id+" AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname=$1", n, s.receiver))
		// pg_catalog is pinned; its namespace dependency has no physical row.
		run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+s.table+"'::regclass AND objid="+id+" AND refclassid='pg_namespace'::regclass AND deptype='n'", 0))
		if s.receiver != "" {
			description := strings.ToLower(s.kind) + " eo_" + s.original + s.signature
			blocked := ownershipError("DROP "+s.target(true)+" CASCADE", "2BP01", "cannot drop "+description+" because extension "+s.receiver+" requires it")
			blocked.Diagnostic.Hint = "You can drop extension " + s.receiver + " instead."
			run(blocked)
		} else {
			// The detached helper is normally required by the real template.
			run(fixture.Error("DROP "+s.target(true)+" RESTRICT", "2BP01"))
		}
	}
	run(ownershipCount("SELECT count(*) FROM pg_ts_dict WHERE oid='pg_catalog.eo_english_stem'::regdictionary AND dicttemplate=(SELECT oid FROM pg_ts_template WHERE tmplnamespace='pg_catalog'::regnamespace AND tmplname='eo_snowball') AND dictinitoption=$1", 1, "language = 'english'"))
	run(ownershipCount("SELECT count(*) FROM pg_ts_template WHERE tmplnamespace='pg_catalog'::regnamespace AND tmplname='eo_snowball' AND tmplinit='pg_catalog.eo_dsnowball_init(internal)'::regprocedure AND tmpllexize='pg_catalog.eo_dsnowball_lexize(internal,internal,internal,internal)'::regprocedure", 1))
	run(ownershipCount("SELECT count(*) FROM pg_ts_config_map WHERE mapcfg='pg_catalog.eo_english'::regconfig AND maptokentype=1 AND mapseqno=1 AND mapdict='pg_catalog.eo_english_stem'::regdictionary", 1))
	// Allocated Snowball callbacks/template/dictionary are not pinned. These
	// normal edges remain independent of the deliberately mixed e receivers.
	for i, s := range ownershipSnowballSeeds[2:] {
		n := int64(1)
		if i == 0 {
			n = 2
		}
		run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+s.table+"'::regclass AND objid=("+s.lookup(true)+") AND refclassid IN ('pg_proc'::regclass,'pg_ts_template'::regclass,'pg_ts_dict'::regclass) AND deptype='n'", n))
	}
	for _, s := range ownershipSnowballSeeds[:2] {
		args := "2281"
		if s.original == "dsnowball_lexize" {
			args = "2281 2281 2281 2281"
		}
		run(ownershipCount("SELECT count(*) FROM pg_proc WHERE oid=("+s.lookup(true)+") AND proargtypes::text=$1 AND prorettype=2281 AND proisstrict AND provolatile='v' AND proparallel='u' AND prosrc=$2 AND probin='$libdir/dict_snowball' AND prolang=(SELECT oid FROM pg_language WHERE lanname='c')", 1, args, s.original))
	}
}

func ownershipSnowballDetachedDrops(run func(fixture.Step)) {
	// PostgreSQL has additional language dictionaries under snowball. Restrict
	// their transitive closure without pretending native's English-only inventory
	// has the same DETAIL list; exact represented normal edges are checked above.
	for _, s := range ownershipSnowballSeeds[:3] {
		run(fixture.Error("DROP "+s.target(true)+" RESTRICT", "2BP01"))
	}
	blocked := ownershipError("DROP TEXT SEARCH DICTIONARY pg_catalog.eo_english_stem RESTRICT", "2BP01", "cannot drop text search dictionary eo_english_stem because other objects depend on it")
	blocked.Diagnostic.Detail = "text search configuration eo_english depends on text search dictionary eo_english_stem"
	blocked.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	// The released cluster also maps hindi/russian tokens through english_stem.
	// Preserve their actual pinned-18.6 diagnostic inventory without inventing
	// those unrepresented native configurations or weakening native diagnostics.
	pg := *blocked.Diagnostic
	pg.Detail += "\ntext search configuration hindi depends on text search dictionary eo_english_stem\ntext search configuration russian depends on text search dictionary eo_english_stem"
	blocked.PostgresDiagnostic = &pg
	run(blocked)
	// A detached, unreferenced configuration has no pin and can actually drop.
	// Roll back ordinary DDL so all later identity/mapping/control checks remain.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION pg_catalog.eo_english RESTRICT", 0))
	run(fixture.Exec("ROLLBACK", 0))
}

// Named principals and ACLs remain exact while ordinary ALTER changes names.
// The template is genuinely ownerless and has no ACL column.
func ownershipSnowballOwners(t *testing.T, p *fixture.Pair, c *sql.Conn, moved bool) [][][]any {
	var out [][][]any
	for _, s := range ownershipSnowballSeeds {
		columns, owner := "NULL::text", ""
		switch s.table {
		case "pg_proc":
			columns, owner = "o.proacl::text", "proowner"
		case "pg_ts_dict":
			owner = "dictowner"
		case "pg_ts_config":
			owner = "cfgowner"
		default:
			continue
		}
		out = append(out, ownershipSnapshot(t, p, c, "SELECT r.rolname,"+columns+" FROM "+s.table+" o JOIN pg_roles r ON r.oid=o."+owner+" WHERE o.oid=("+s.lookup(moved)+")"))
	}
	return out
}

func ownershipSnowballMetadata(t *testing.T, p *fixture.Pair, c *sql.Conn) [][][]any {
	var out [][][]any
	for _, s := range ownershipSnowballSeeds {
		id := ownershipID(t, p, c, s.lookup(true))
		tc := ownershipClassCase{class: map[string]int64{"pg_proc": 1255, "pg_ts_template": 3764, "pg_ts_dict": 3600, "pg_ts_config": 3602}[s.table]}
		out = append(out, ownershipClassMetadata(t, p, c, tc, id)...)
		out = append(out, ownershipSnapshot(t, p, c, "SELECT n.nspname,o."+s.name+" FROM "+s.table+" o JOIN pg_namespace n ON n.oid=o."+s.namespace+" WHERE o.oid="+ownershipNumber(id)))
	}
	return out
}

func ownershipSnowballSeedControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, metadata [][][]any) {
	saved := extensionDump(t, p.Ctx, p.Native)
	// Preserve two actual source row resources, one for each destination. Their
	// metadata and values must remain valid after the source graph is replaced.
	var oldRows []*sql.Rows
	for range 2 {
		rows, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('pg_catalog.eo_english_stem','RATS') AS value")
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
		// LIFO: seeds are restored/detached before stock receiver teardown.
		defer ownershipSnowballCleanup(t, conn)()
		if err := stuntgres.Reset(p.Ctx, control); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
			t.Fatal("Snowball seed replay", err)
		}
		run := ownershipRunWithRecorder(t, p, conn, recorder)
		const q = "SELECT ts_lexize('pg_catalog.eo_english_stem'::regdictionary,$1::text) AS value"
		held, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		checkHeld := func() {
			for _, tc := range []struct{ input, want any }{{"RATS", "{rat}"}, {"the", "{the}"}, {nil, nil}} {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, recorder, fixture.Select(q, []string{"value"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input))
			}
		}
		checkHeld()
		ownershipSnowballSeedWitness(run)
		ownershipClassMetadataEqual(t, metadata, ownershipSnowballMetadata(t, p, conn))
		if again := extensionDump(t, p.Ctx, control); again != saved {
			t.Fatal("Snowball seed replay changed dump bytes")
		}
		for _, invalid := range []string{strings.Replace(saved, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1), saved + "\nCREATE EXTENSION missing_provider;\n"} {
			if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(invalid)); err == nil {
				t.Fatal("invalid Snowball seed restore accepted")
			}
			if after := extensionDump(t, p.Ctx, control); after != saved {
				t.Fatal("failed Snowball seed restore changed destination")
			}
			ownershipSnowballSeedWitness(run)
			ownershipClassMetadataEqual(t, metadata, ownershipSnowballMetadata(t, p, conn))
			checkHeld()
		}
		ownershipSnowballOldRows(t, oldRows[0])
		oldRows = oldRows[1:]
		for _, s := range ownershipSnowballSeeds {
			if s.receiver != "" {
				run(fixture.Exec("ALTER EXTENSION "+s.receiver+" DROP "+s.target(true), 0))
			}
		}
		ownershipSnowballDictionaryWitness(run, "pg_catalog.eo_english_stem", false)
		ownershipSnowballDetachedDrops(run)
		checkHeld()
		for _, s := range ownershipSnowballSeeds {
			run(ownershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+s.table+"'::regclass AND objid=("+s.lookup(true)+") AND refclassid='pg_extension'::regclass AND deptype='e'", 0))
		}
		ownershipClassMetadataEqual(t, metadata, ownershipSnowballMetadata(t, p, conn))
	}
	restore(p.Native, b.DB, b.Recorder)
	connector, err := stuntgres.NewDriver().OpenConnector("eo_snowball_seed")
	if err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	other := sql.OpenDB(connector)
	defer other.Close()
	restore(other, recorded, recorder)
}

func ownershipSnowballOldRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 || columns[0].Name() != "value" || columns[0].DatabaseTypeName() != "_TEXT" {
		t.Fatal("retained Snowball metadata", columns, err)
	}
	var value string
	if !rows.Next() {
		t.Fatal("lost retained Snowball row", rows.Err())
	}
	if err := rows.Scan(&value); err != nil || value != "{rat}" {
		t.Fatal("retained Snowball value", value, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained Snowball EOF", rows.Err())
	}
}
