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

func hungarianSnowballSeedCycles(t *testing.T) {
	for _, other := range []string{"simple", "english", "hindi"} {
		t.Run(other, func(t *testing.T) { hungarianSeedCycle(t, other) })
	}
}

func hungarianSeedCycle(t *testing.T, other string) {
	t.Helper()
	otherDict := other + "_stem"
	if other == "simple" {
		otherDict = "simple"
	}
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		type seed struct {
			kind, catalog, column, name, temp string
			id                                int64
			owner                             string
		}
		seeds := []seed{
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", name: "hungarian_stem", temp: "hungarian_cycle_dict_a"},
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", name: otherDict, temp: "hungarian_cycle_dict_other"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", name: "hungarian", temp: "hungarian_cycle_config_a"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", name: other, temp: "hungarian_cycle_config_other"},
		}
		for i := range seeds {
			s := &seeds[i]
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM "+s.catalog+" WHERE "+s.column+"=$1", s.name).Scan(&s.id); err != nil || s.id <= 0 {
				t.Fatal("actual seed", s.name, s.id, err)
			}
			ownerColumn := "dictowner"
			if s.kind == "CONFIGURATION" {
				ownerColumn = "cfgowner"
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT "+ownerColumn+"::regrole::text FROM "+s.catalog+" WHERE oid=$1::oid", s.id).Scan(&s.owner); err != nil {
				t.Fatal("original seed owner", err)
			}
		}
		canonical := func(ctx context.Context) {
			t.Helper()
			for _, s := range seeds {
				var name string
				if err := c.QueryRowContext(ctx, "SELECT "+s.column+"::text FROM "+s.catalog+" WHERE oid=$1::oid", s.id).Scan(&name); err != nil {
					t.Error(err)
					continue
				}
				q := "ALTER TEXT SEARCH " + s.kind + " pg_catalog.\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\" RENAME TO " + s.temp
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("stage canonical seed repair", err)
				}
			}
			for _, s := range seeds {
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+s.kind+" pg_catalog."+s.temp+" RENAME TO "+s.name); err != nil {
					t.Error("restore canonical seed", err)
				}
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+s.kind+" pg_catalog."+s.name+" OWNER TO \""+strings.ReplaceAll(s.owner, "\"", "\"\"")+"\""); err != nil {
					t.Error("restore original seed owner", err)
				}
			}
			if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.hungarian_stem (language=hungarian,stopwords=hungarian)"); err != nil {
				t.Error(err)
			}
		}
		if b.PG {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				canonical(ctx)
			}()
		}
		for _, q := range []string{
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.hungarian_stem (language='HuNgArIaN',stopwords='')",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.hungarian_stem RENAME TO hungarian_cycle_dict_swap",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog." + otherDict + " RENAME TO hungarian_stem",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.hungarian_cycle_dict_swap RENAME TO " + otherDict,
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hungarian RENAME TO hungarian_cycle_config_swap",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog." + other + " RENAME TO hungarian",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hungarian_cycle_config_swap RENAME TO " + other,
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		otherRats := "{rats}"
		var otherOptions any = "language = 'hindi'"
		if other == "simple" {
			otherOptions = nil
		}
		if other == "english" {
			otherRats, otherOptions = "{rat}", "language = 'english', stopwords = 'english'"
		}
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, originalIDs bool) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				for _, s := range []fixture.Step{
					fixture.Select("SELECT ts_lexize('"+otherDict+"','RATS') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{rats}"}}),
					fixture.Select("SELECT ts_lexize('hungarian_stem','RATS') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{otherRats}}),
					fixture.Select("SELECT ts_lexize('"+otherDict+"','házak') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{ház}"}}),
					fixture.Select("SELECT to_tsvector('"+other+"','házak RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ház':1 'rats':2"}}),
					fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='"+otherDict+"'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'HuNgArIaN', stopwords = ''"}}),
					fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='hungarian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{otherOptions}}),
				} {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			if originalIDs {
				regtsSelect(t, p, b, c, "SELECT oid::bigint FROM pg_ts_dict WHERE dictname='"+otherDict+"'", []string{"oid"}, []string{"INT8"}, [][]any{{seeds[0].id}})
				regtsSelect(t, p, b, c, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='"+other+"'", []string{"oid"}, []string{"INT8"}, [][]any{{seeds[2].id}})
			}
		}
		check(p, b, c, true)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native cycle replay required")
			}
			canonical(p.Ctx)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted seed cycle", err)
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
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('hungarian_stem','ONLY') AS lexemes", "{only}")
				regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='hungarian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'hungarian', stopwords = 'hungarian'"}})
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c, true)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("deterministic seed-cycle redump", err)
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
