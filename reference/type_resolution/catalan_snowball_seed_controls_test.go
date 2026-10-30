package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCatalanSnowballSeedControls(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var dictOwner, configOwner string
		var originalDict, originalConfig int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dictowner::regrole::text FROM pg_ts_dict WHERE oid='pg_catalog.catalan_stem'::regdictionary").Scan(&originalDict, &dictOwner); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,cfgowner::regrole::text FROM pg_ts_config WHERE oid='pg_catalog.catalan'::regconfig").Scan(&originalConfig, &configOwner); err != nil {
			t.Fatal(err)
		}
		regtsExec(t, p, b, c, "CREATE ROLE dictionary_creator", 0)
		check := func() {
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.catalan_seed','GATS') AS lexemes", "{gat}")
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.catalan_seed','ONLY') AS lexemes", "{only}")
			regtsSelect(t, p, b, c, "SELECT dictinitoption,dictowner='dictionary_creator'::regrole::oid AS owned FROM pg_ts_dict WHERE oid='dictionary_right.catalan_seed'::regdictionary", []string{"dictinitoption", "owned"}, []string{"TEXT", "BOOL"}, [][]any{{"stopwords = '', language = 'CaTaLaN'", true}})
			regtsSelect(t, p, b, c, "SELECT d='dictionary_right.catalan_seed'::regdictionary AS d,c='dictionary_right.catalan_seed_config'::regconfig AS c,da[1]='dictionary_right.catalan_seed'::regdictionary AS da,ca[1]='dictionary_right.catalan_seed_config'::regconfig AS ca FROM catalan_seed_rows", []string{"d", "c", "da", "ca"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('dictionary_right.catalan_seed_config','GATS café') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'caf':2 'gats':1"}})
			regtsSelect(t, p, b, c, "SELECT cfgowner='dictionary_creator'::regrole::oid AS owned FROM pg_ts_config WHERE oid='dictionary_right.catalan_seed_config'::regconfig", []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
		}
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("native emitted stream required")
			}
			// Restore authenticated stock owners, names, options and maps before shared cleanup.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				exec := func(q string) {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("restore stock Catalan state", q, err)
					}
				}
				exec("DROP TABLE IF EXISTS catalan_seed_rows")
				// Never drop a canonical stock identity if replay failed before its rename.
				for _, tc := range []struct {
					query, drop string
					original    int64
				}{
					{"SELECT oid::bigint FROM pg_ts_config WHERE cfgname='catalan' AND cfgnamespace='pg_catalog'::regnamespace", "DROP TEXT SEARCH CONFIGURATION pg_catalog.catalan", originalConfig},
					{"SELECT oid::bigint FROM pg_ts_dict WHERE dictname='catalan_stem' AND dictnamespace='pg_catalog'::regnamespace", "DROP TEXT SEARCH DICTIONARY pg_catalog.catalan_stem", originalDict},
				} {
					var current int64
					err := c.QueryRowContext(ctx, tc.query).Scan(&current)
					if err == nil && current != tc.original {
						exec(tc.drop)
					} else if err != nil && err != sql.ErrNoRows {
						t.Error(err)
					}
				}
				for _, q := range []string{
					"ALTER TEXT SEARCH CONFIGURATION dictionary_right.catalan_seed_config ALTER MAPPING FOR asciiword WITH dictionary_right.catalan_seed",
					"ALTER TEXT SEARCH CONFIGURATION dictionary_right.catalan_seed_config OWNER TO \"" + configOwner + "\"",
					"ALTER TEXT SEARCH CONFIGURATION dictionary_right.catalan_seed_config SET SCHEMA pg_catalog",
					"ALTER TEXT SEARCH CONFIGURATION pg_catalog.catalan_seed_config RENAME TO catalan",
					"ALTER TEXT SEARCH DICTIONARY dictionary_right.catalan_seed (stopwords,language=catalan)",
					"ALTER TEXT SEARCH DICTIONARY dictionary_right.catalan_seed OWNER TO \"" + dictOwner + "\"",
					"ALTER TEXT SEARCH DICTIONARY dictionary_right.catalan_seed SET SCHEMA pg_catalog",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.catalan_seed RENAME TO catalan_stem",
				} {
					exec(q)
				}
			}()
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual edited seed stream replay", err)
			}
			check()
			return
		}
		for _, tc := range []struct{ options, metadata, want string }{
			{"stopwords=english", "language = 'catalan', stopwords = 'english'", "{}"},
			{"stopwords", "language = 'catalan'", "{only}"},
			{"stopwords=''", "language = 'catalan', stopwords = ''", "{only}"},
			{"language=catalan,language='CaTaLaN'", "stopwords = '', language = 'CaTaLaN'", "{only}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY pg_catalog.catalan_stem ("+tc.options+")", 0)
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('pg_catalog.catalan_stem','ONLY') AS lexemes", tc.want)
			regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='pg_catalog.catalan_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.metadata}})
		}
		for _, q := range []string{
			"CREATE SCHEMA dictionary_right",
			"CREATE TABLE catalan_seed_rows(d regdictionary DEFAULT 'pg_catalog.catalan_stem'::regdictionary,c regconfig DEFAULT 'pg_catalog.catalan'::regconfig,da regdictionary[] DEFAULT ARRAY['pg_catalog.catalan_stem'::regdictionary],ca regconfig[] DEFAULT ARRAY['pg_catalog.catalan'::regconfig])",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.catalan_stem RENAME TO catalan_seed",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.catalan_seed SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH DICTIONARY dictionary_right.catalan_seed OWNER TO dictionary_creator",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.catalan RENAME TO catalan_seed_config",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.catalan_seed_config SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH CONFIGURATION dictionary_right.catalan_seed_config OWNER TO dictionary_creator",
			"ALTER TEXT SEARCH CONFIGURATION dictionary_right.catalan_seed_config ALTER MAPPING FOR asciiword WITH simple",
			"CREATE TEXT SEARCH DICTIONARY pg_catalog.catalan_stem (TEMPLATE=simple,accept=false)",
			"CREATE TEXT SEARCH CONFIGURATION pg_catalog.catalan (COPY=simple)",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO catalan_seed_rows DEFAULT VALUES", 1)
		check()
		old, err := c.QueryContext(p.Ctx, "SELECT d,c FROM catalan_seed_rows")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil || len(meta) != 2 || meta[0].DatabaseTypeName() != "REGDICTIONARY" || meta[1].DatabaseTypeName() != "REGCONFIG" {
			t.Fatal(meta, err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		for _, reset := range []bool{false, true} {
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='pg_catalog.catalan_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'catalan'"}})
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='pg_catalog.catalan'::regconfig", []string{"n"}, []string{"INT8"}, [][]any{{int64(19)}})
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("seed deterministic redump", err)
			}
		}
		if !old.Next() {
			t.Fatal(old.Err())
		}
		var d, config any
		if err := old.Scan(&d, &config); err != nil || d != "dictionary_right.catalan_seed" || config != "dictionary_right.catalan_seed_config" {
			t.Fatal(d, config, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
	})
}
