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

func TestReferenceArabicSnowballSeedCycles(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		type seed struct {
			kind, catalog, column, name, temp string
			id                                int64
		}
		seeds := []seed{
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", name: "arabic_stem", temp: "arabic_cycle_dict_a"},
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", name: "english_stem", temp: "arabic_cycle_dict_e"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", name: "arabic", temp: "arabic_cycle_config_a"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", name: "english", temp: "arabic_cycle_config_e"},
		}
		for i := range seeds {
			s := &seeds[i]
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM "+s.catalog+" WHERE "+s.column+"=$1", s.name).Scan(&s.id); err != nil || s.id <= 0 {
				t.Fatal("actual seed", s.name, s.id, err)
			}
		}
		canonical := func(ctx context.Context) {
			t.Helper()
			// Resolve each current name by the original identity. This also repairs
			// an interrupted rename sequence rather than assuming a complete swap.
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
			}
			if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.arabic_stem (language=arabic,stopwords)"); err != nil {
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
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.arabic_stem (language='ArAbIc',stopwords='')",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.arabic_stem RENAME TO arabic_cycle_dict_swap",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.english_stem RENAME TO arabic_stem",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.arabic_cycle_dict_swap RENAME TO english_stem",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.arabic RENAME TO arabic_cycle_config_swap",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.english RENAME TO arabic",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.arabic_cycle_config_swap RENAME TO english",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		check := func() {
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					q              string
					columns, types []string
					rows           [][]any
				}{
					{"SELECT ts_lexize('english_stem','RATS') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{rats}"}}},
					{"SELECT ts_lexize('arabic_stem','RATS') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{rat}"}}},
					{"SELECT to_tsvector('english','الكتاب RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':2 'كتاب':1"}}},
					{"SELECT dictinitoption FROM pg_ts_dict WHERE oid='english_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'ArAbIc', stopwords = ''"}}},
				} {
					s := fixture.Select(tc.q, tc.columns, tc.types, tc.rows)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			regtsSelect(t, p, b, c, "SELECT oid::bigint FROM pg_ts_dict WHERE dictname='english_stem'", []string{"oid"}, []string{"INT8"}, [][]any{{seeds[0].id}})
			regtsSelect(t, p, b, c, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='english'", []string{"oid"}, []string{"INT8"}, [][]any{{seeds[2].id}})
		}
		check()
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native cycle replay required")
			}
			canonical(p.Ctx)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted seed cycle", err)
			}
			check()
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
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('arabic_stem','ONLY') AS lexemes", "{only}")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("deterministic seed-cycle redump", err)
			}
		}
	})
}
