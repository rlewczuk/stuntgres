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

func lithuanianSimpleSeedCycle(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var dictionary, config int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'lithuanian_stem'::regdictionary::oid::bigint,'lithuanian'::regconfig::oid::bigint").Scan(&dictionary, &config); err != nil {
			t.Fatal(err)
		}
		type seed struct {
			kind, catalog, column, name, temp string
			id                                int64
			owner                             string
		}
		seeds := []seed{
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", name: "lithuanian_stem", temp: "lithuanian_simple_dict_a"},
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", name: "simple", temp: "lithuanian_simple_dict_other"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", name: "lithuanian", temp: "lithuanian_simple_config_a"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", name: "simple", temp: "lithuanian_simple_config_other"},
		}
		for i := range seeds {
			s := &seeds[i]
			ownerColumn := "dictowner"
			if s.kind == "CONFIGURATION" {
				ownerColumn = "cfgowner"
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,"+ownerColumn+"::regrole::text FROM "+s.catalog+" WHERE "+s.column+"=$1", s.name).Scan(&s.id, &s.owner); err != nil || s.id <= 0 {
				t.Fatal("original seed identity and owner", s.name, s.id, err)
			}
		}
		cleanupPair := *p
		restored := func() {}
		if b.PG {
			lithuanianRestored := lithuanianDefinitionSnapshot(t, &cleanupPair, b, c, "lithuanian_stem", "lithuanian")
			simpleRestored := lithuanianDefinitionSnapshot(t, &cleanupPair, b, c, "simple", "simple")
			restored = func() {
				lithuanianRestored()
				simpleRestored()
			}
		}
		canonical := func(ctx context.Context) {
			// Resolve each original object by identity: a failed rename or
			// partially executed replay need not leave a complete swap.
			for _, s := range seeds {
				var name string
				if err := c.QueryRowContext(ctx, "SELECT "+s.column+"::text FROM "+s.catalog+" WHERE oid=$1::oid", s.id).Scan(&name); err != nil {
					t.Error("find original seed", err)
					continue
				}
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+s.kind+" pg_catalog.\""+strings.ReplaceAll(name, "\"", "\"\"")+"\" RENAME TO "+s.temp); err != nil {
					t.Error("stage canonical seed repair", err)
				}
			}
			for _, s := range seeds {
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+s.kind+" pg_catalog."+s.temp+" RENAME TO "+s.name); err != nil {
					t.Error("restore canonical seed", err)
				}
				// Native replay carries native ownership; PostgreSQL's pinned
				// simple objects must retain their actual original owner.
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+s.kind+" pg_catalog."+s.name+" OWNER TO \""+strings.ReplaceAll(s.owner, "\"", "\"\"")+"\""); err != nil {
					t.Error("restore original seed owner", err)
				}
			}
			cleanupPair.Ctx = ctx
			restored()
		}
		if b.PG {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				canonical(ctx)
			}()
		}
		for _, q := range []string{
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.lithuanian_stem RENAME TO lithuanian_swap_dict",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.simple RENAME TO lithuanian_stem",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.lithuanian_swap_dict RENAME TO simple",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.lithuanian RENAME TO lithuanian_swap_config",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple RENAME TO lithuanian",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.lithuanian_swap_config RENAME TO simple",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, same bool) {
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('simple','gervė') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{gerv}"}}))
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('lithuanian_stem','gervė') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{gervė}"}}))
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('simple','VYRAS gervė') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'gerv':2 'vyr':1"}}))
			lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('lithuanian','VYRAS gervė') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'gervė':2 'vyras':1"}}))
			if same {
				regtsSelect(t, p, b, c, "SELECT 'simple'::regdictionary::oid::bigint AS dictionary_oid,'simple'::regconfig::oid::bigint AS config_oid", []string{"dictionary_oid", "config_oid"}, []string{"INT8", "INT8"}, [][]any{{dictionary, config}})
			}
		}
		check(p, b, c, true)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native simple-cycle stream required")
			}
			canonical(p.Ctx)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual simple-cycle replay", err)
			}
			check(p, b, c, true)
			return
		}
		var out bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(out.Bytes())
		for _, reset := range []bool{false, true} {
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c, true)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("simple cycle redump", err)
			}
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc, false)
	})
}