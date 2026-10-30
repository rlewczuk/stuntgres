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

func TestReferenceArmenianSnowballSeedCycles(t *testing.T) {
	for _, other := range []string{"english", "arabic"} {
		t.Run(other, func(t *testing.T) { armenianSeedCycle(t, other) })
	}
}

func armenianSeedCycle(t *testing.T, other string) {
	t.Helper()
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		type seed struct {
			kind, catalog, column, name, temp string
			id                                int64
		}
		seeds := []seed{
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", name: "armenian_stem", temp: "armenian_cycle_dict_a"},
			{kind: "DICTIONARY", catalog: "pg_ts_dict", column: "dictname", name: other + "_stem", temp: "armenian_cycle_dict_other"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", name: "armenian", temp: "armenian_cycle_config_a"},
			{kind: "CONFIGURATION", catalog: "pg_ts_config", column: "cfgname", name: other, temp: "armenian_cycle_config_other"},
		}
		for i := range seeds {
			s := &seeds[i]
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM "+s.catalog+" WHERE "+s.column+"=$1", s.name).Scan(&s.id); err != nil || s.id <= 0 {
				t.Fatal("actual seed", s.name, s.id, err)
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
			}
			if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.armenian_stem (language=armenian,stopwords)"); err != nil {
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
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.armenian_stem (language='ArMeNiAn',stopwords='')",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.armenian_stem RENAME TO armenian_cycle_dict_swap",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog." + other + "_stem RENAME TO armenian_stem",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.armenian_cycle_dict_swap RENAME TO " + other + "_stem",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.armenian RENAME TO armenian_cycle_config_swap",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog." + other + " RENAME TO armenian",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.armenian_cycle_config_swap RENAME TO " + other,
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		otherRats, otherOptions := "{rats}", "language = 'arabic'"
		if other == "english" {
			otherRats, otherOptions = "{rat}", "language = 'english', stopwords = 'english'"
		}
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, originalIDs bool) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				for _, s := range []fixture.Step{
					fixture.Select("SELECT ts_lexize('"+other+"_stem','RATS') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{rats}"}}),
					fixture.Select("SELECT ts_lexize('armenian_stem','RATS') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{otherRats}}),
					fixture.Select("SELECT ts_lexize('"+other+"_stem','աղոթքը') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{աղոթ}"}}),
					fixture.Select("SELECT to_tsvector('"+other+"','աղոթքը RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'rats':2 'աղոթ':1"}}),
					fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='"+other+"_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'ArMeNiAn', stopwords = ''"}}),
					fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='armenian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{otherOptions}}),
				} {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			if originalIDs {
				regtsSelect(t, p, b, c, "SELECT oid::bigint FROM pg_ts_dict WHERE dictname='"+other+"_stem'", []string{"oid"}, []string{"INT8"}, [][]any{{seeds[0].id}})
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
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('armenian_stem','ONLY') AS lexemes", "{only}")
				regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='armenian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'armenian'"}})
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
