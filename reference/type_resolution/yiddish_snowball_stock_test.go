package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func yiddishStockOptions(t *testing.T) {
	for _, tc := range []struct { name, alter, options, running, the string }{
		{"canonical", "stopwords", "language = 'yiddish'", "{running}", "{the}"},
		{"removed-list", "stopwords", "language = 'yiddish'", "{running}", "{the}"},
		{"reordered", "stopwords=english,language='YiDdIsH'", "stopwords = 'english', language = 'YiDdIsH'", "{running}", "{}"},
		{"explicit-empty", "stopwords=''", "language = 'yiddish', stopwords = ''", "{running}", "{the}"},
		{"mixed-language", "language='YiDdIsH'", "language = 'YiDdIsH'", "{running}", "{the}"},
		{"explicit-english", "stopwords=english", "language = 'yiddish', stopwords = 'english'", "{running}", "{}"},
		{"changed-provider", "language=english", "language = 'english'", "{run}", "{the}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var emitted []byte
			nativeDumped := false
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cleanupPair := *p
				if b.PG {
					restored := lithuanianDefinitionSnapshot(t, &cleanupPair, b, c, "yiddish_stem", "yiddish")
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel()
						if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem (language=yiddish,stopwords)"); err != nil { t.Error(err) }
						cleanupPair.Ctx = ctx
						restored()
					}()
				}
				canonical := func() { regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem (language=yiddish,stopwords)", 0) }
				if tc.name == "removed-list" { regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_stem (stopwords=english)", 0) }
				regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_stem ("+tc.alter+")", 0)
				check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='yiddish_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.options}})
					for _, pair := range [][2]string{{"RUNNING", tc.running}, {"THE", tc.the}} { lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('yiddish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{pair[1]}}, pair[0])) }
				}
				check(p, b, c)
				if b.PG {
					if !nativeDumped { t.Fatal("missing native stock dump") }
					canonical()
					if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil { t.Fatal("actual stock replay", err) }
					check(p, b, c)
					return
				}
				var out bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil { t.Fatal(err) }
				emitted, nativeDumped = bytes.Clone(out.Bytes()), true
				if (len(emitted) == 0) != (tc.name == "canonical" || tc.name == "removed-list") { t.Fatalf("noncanonical stock delta %q", emitted) }
				for _, reset := range []bool{false, true} {
					if reset { if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) } }
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
					check(p, b, c)
					var again bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) { t.Fatal("ordered stock redump", err) }
				}
				fresh := fixture.NewFixture(t, false, true)
				fc, err := fresh.Memory.Conn(fresh.Ctx)
				if err != nil { t.Fatal(err) }
				defer fc.Close()
				if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
				check(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc)
			})
		})
	}
}

func yiddishStockIdentities(t *testing.T) {
	for _, other := range []string{"simple", "english"} {
		t.Run("cycle-"+other, func(t *testing.T) { yiddishSeedCycle(t, other) })
	}
	t.Run("moved-with-maps", yiddishMovedStock)
}

func yiddishSeedCycle(t *testing.T, other string) {
	t.Helper()
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		dictOther := other + "_stem"
		if other == "simple" { dictOther = "simple" }
		type seed struct { kind, catalog, column, ownerColumn, name, temporary string; id int64; owner string }
		seeds := []seed{
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", ownerColumn: "dictowner", name: "yiddish_stem", temporary: "yiddish_cycle_dict_a"},
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", ownerColumn: "dictowner", name: dictOther, temporary: "yiddish_cycle_dict_b"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", ownerColumn: "cfgowner", name: "yiddish", temporary: "yiddish_cycle_config_a"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", ownerColumn: "cfgowner", name: other, temporary: "yiddish_cycle_config_b"},
		}
		for i := range seeds {
			s := &seeds[i]
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,"+s.ownerColumn+"::regrole::text FROM "+s.catalog+" WHERE "+s.column+"=$1", s.name).Scan(&s.id, &s.owner); err != nil || s.id <= 0 { t.Fatal(s.name, s.id, err) }
		}
		cleanupPair := *p
		restored := func() {}
		if b.PG {
			a := lithuanianDefinitionSnapshot(t, &cleanupPair, b, c, "yiddish_stem", "yiddish")
			z := lithuanianDefinitionSnapshot(t, &cleanupPair, b, c, dictOther, other)
			restored = func() { a(); z() }
		}
		canonical := func(ctx context.Context) {
			for _, s := range seeds {
				var name string
				if err := c.QueryRowContext(ctx, "SELECT "+s.column+"::text FROM "+s.catalog+" WHERE oid=$1::oid", s.id).Scan(&name); err != nil { t.Error(err); continue }
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+s.kind+" pg_catalog.\""+strings.ReplaceAll(name, "\"", "\"\"")+"\" RENAME TO "+s.temporary); err != nil { t.Error(err) }
			}
			for _, s := range seeds {
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+s.kind+" pg_catalog."+s.temporary+" RENAME TO "+s.name); err != nil { t.Error(err) }
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+s.kind+" pg_catalog."+s.name+" OWNER TO \""+strings.ReplaceAll(s.owner, "\"", "\"\"")+"\""); err != nil { t.Error(err) }
			}
			cleanupPair.Ctx = ctx
			restored()
		}
		if b.PG { defer func() { ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel(); canonical(ctx) }() }
		for _, q := range []string{
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem RENAME TO yiddish_swap_dict",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog."+dictOther+" RENAME TO yiddish_stem",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.yiddish_swap_dict RENAME TO "+dictOther,
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.yiddish RENAME TO yiddish_swap_config",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog."+other+" RENAME TO yiddish",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.yiddish_swap_config RENAME TO "+other,
		} { regtsExec(t, p, b, c, q, 0) }
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, same bool) {
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('"+dictOther+"','קינדער') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{קינד}"}}))
			otherOnly := "{only}"
			if other == "english" { otherOnly = "{}" }
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','ONLY') AS lexemes", otherOnly)
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('"+other+"','קינדער RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':2 'קינד':1"}}))
			if same { regtsSelect(t, p, b, c, "SELECT $1::regdictionary::oid::bigint AS dictionary,$2::regconfig::oid::bigint AS configuration", []string{"dictionary", "configuration"}, []string{"INT8", "INT8"}, [][]any{{seeds[0].id, seeds[2].id}}, dictOther, other) }
		}
		check(p, b, c, true)
		if b.PG {
			if len(emitted) == 0 { t.Fatal("missing actual cycle stream") }
			canonical(p.Ctx)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil { t.Fatal(err) }
			check(p, b, c, true)
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil { t.Fatal(err) }
		emitted = bytes.Clone(out.Bytes())
		for _, reset := range []bool{false, true} {
			if reset { if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) } }
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
			check(p, b, c, true)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) { t.Fatal("cycle redump", err) }
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil { t.Fatal(err) }
		defer fc.Close()
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
		check(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc, false)
	})
}
