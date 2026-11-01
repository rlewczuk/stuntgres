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

func russianEnglishBackupIdentity(t *testing.T) {
	var emitted []byte
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var original int64
		var originalOwner string
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dictowner::regrole::text FROM pg_ts_dict WHERE oid='english_stem'::regdictionary").Scan(&original, &originalOwner); err != nil {
			t.Fatal(err)
		}
		if b.PG {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				// Locate the actual allocated English object, not a name-shaped decoy.
				var namespace, name string
				if err := c.QueryRowContext(ctx, "SELECT n.nspname::text,d.dictname::text FROM pg_ts_dict d JOIN pg_namespace n ON n.oid=d.dictnamespace WHERE d.oid=$1::oid", original).Scan(&namespace, &name); err != nil {
					t.Error("locate real English seed for repair", err)
					return
				}
				if namespace == "pg_catalog" && name == "english_stem" {
					return
				}
				for _, q := range []string{
					"DROP TEXT SEARCH DICTIONARY IF EXISTS pg_catalog.english_stem",
					"ALTER TEXT SEARCH DICTIONARY dictionary_right.russian_english_saved SET SCHEMA pg_catalog",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.russian_english_saved RENAME TO english_stem",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.english_stem OWNER TO \"" + strings.ReplaceAll(originalOwner, "\"", "\"\"") + "\"",
				} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("repair real English seed", err)
					}
				}
			}()
		}
		for _, q := range []string{
			"CREATE ROLE dictionary_creator", "CREATE SCHEMA dictionary_right",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.english_stem RENAME TO russian_english_saved",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.russian_english_saved SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH DICTIONARY dictionary_right.russian_english_saved OWNER TO dictionary_creator",
			"CREATE TEXT SEARCH DICTIONARY pg_catalog.english_stem (TEMPLATE=simple,accept=false)",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('russian','THE машины и RUNNING') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'run':4 'машин':2"}}))
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='russian'::regconfig AND maptokentype IN (1,11,16) AND mapseqno=1 AND mapdict='dictionary_right.russian_english_saved'::regdictionary", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}})
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('pg_catalog.english_stem','RUNNING') AS lexemes", nil)
			dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_right.russian_english_saved','RUNNING') AS lexemes", "{run}")
			regtsSelect(t, p, b, c, "SELECT dictowner='dictionary_creator'::regrole::oid AS owned,dictnamespace='dictionary_right'::regnamespace AS moved FROM pg_ts_dict WHERE oid='dictionary_right.russian_english_saved'::regdictionary", []string{"owned", "moved"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		}
		check(p, b, c)
		regtsSelect(t, p, b, c, "SELECT oid::bigint FROM pg_ts_dict WHERE oid='dictionary_right.russian_english_saved'::regdictionary", []string{"oid"}, []string{"INT8"}, [][]any{{original}})
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native English-backup stream required")
			}
			for _, q := range []string{
				"DROP TEXT SEARCH DICTIONARY pg_catalog.english_stem",
				"ALTER TEXT SEARCH DICTIONARY dictionary_right.russian_english_saved SET SCHEMA pg_catalog",
				"ALTER TEXT SEARCH DICTIONARY pg_catalog.russian_english_saved RENAME TO english_stem",
				"ALTER TEXT SEARCH DICTIONARY pg_catalog.english_stem OWNER TO \"" + strings.ReplaceAll(originalOwner, "\"", "\"\"") + "\"",
				"DROP SCHEMA dictionary_right",
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted English-backup replay", err)
			}
			check(p, b, c)
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
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('english_stem','RUNNING') AS lexemes", "{run}")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("English-backup redump", err)
			}
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		regtsExec(t, fresh, fb, fc, "CREATE ROLE dictionary_creator", 0)
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(fresh, fb, fc)
	})
}
