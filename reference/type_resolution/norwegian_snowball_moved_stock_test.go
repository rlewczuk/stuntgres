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

func norwegianMovedStockControls(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var dictionary, configuration int64
		var dictionaryOwner, configurationOwner string
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dictowner::regrole::text FROM pg_ts_dict WHERE oid='norwegian_stem'::regdictionary").Scan(&dictionary, &dictionaryOwner); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,cfgowner::regrole::text FROM pg_ts_config WHERE oid='norwegian'::regconfig").Scan(&configuration, &configurationOwner); err != nil {
			t.Fatal(err)
		}
		quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
		canonical := func(ctx context.Context) {
			for _, item := range []struct {
				kind, catalog, name, namespace, canonical, owner string
				id                                               int64
			}{
				{"DICTIONARY", "pg_ts_dict", "dictname", "dictnamespace", "norwegian_stem", dictionaryOwner, dictionary},
				{"CONFIGURATION", "pg_ts_config", "cfgname", "cfgnamespace", "norwegian", configurationOwner, configuration},
			} {
				var name, namespace string
				if err := c.QueryRowContext(ctx, "SELECT x."+item.name+"::text,n.nspname::text FROM "+item.catalog+" x JOIN pg_namespace n ON n.oid=x."+item.namespace+" WHERE x.oid=$1::oid", item.id).Scan(&name, &namespace); err != nil {
					t.Error("find moved seed", err)
					continue
				}
				object := quote(namespace) + "." + quote(name)
				if namespace != "pg_catalog" {
					if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+item.kind+" "+object+" SET SCHEMA pg_catalog"); err != nil {
						t.Error("restore stock namespace", err)
					}
					object = "pg_catalog." + quote(name)
				}
				if name != item.canonical {
					if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+item.kind+" "+object+" RENAME TO "+item.canonical); err != nil {
						t.Error("restore stock name", err)
					}
				}
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+item.kind+" pg_catalog."+item.canonical+" OWNER TO "+quote(item.owner)); err != nil {
					t.Error("restore actual original stock owner", err)
				}
			}
			if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.norwegian_stem (language=norwegian,stopwords=norwegian)"); err != nil {
				t.Error("restore canonical options", err)
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
			"CREATE ROLE dictionary_creator", "CREATE SCHEMA dictionary_right",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.norwegian_stem (stopwords='',language='NoRwEgIaN')",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.norwegian_stem RENAME TO norwegian_moved",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.norwegian_moved SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH DICTIONARY dictionary_right.norwegian_moved OWNER TO dictionary_creator",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.norwegian RENAME TO norwegian_moved_config",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.norwegian_moved_config SET SCHEMA dictionary_right",
			"ALTER TEXT SEARCH CONFIGURATION dictionary_right.norwegian_moved_config OWNER TO dictionary_creator",
			"CREATE TABLE norwegian_seed_carriers(d regdictionary DEFAULT 'dictionary_right.norwegian_moved'::regdictionary,c regconfig DEFAULT 'dictionary_right.norwegian_moved_config'::regconfig)",
			"CREATE VIEW norwegian_seed_view AS SELECT d,c FROM norwegian_seed_carriers",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO norwegian_seed_carriers DEFAULT VALUES", 1)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			regtsSelect(t, p, b, c, "SELECT dictinitoption,dictowner='dictionary_creator'::regrole::oid AS owned,dictnamespace='dictionary_right'::regnamespace AS moved FROM pg_ts_dict WHERE oid='dictionary_right.norwegian_moved'::regdictionary", []string{"dictinitoption", "owned", "moved"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"stopwords = '', language = 'NoRwEgIaN'", true, true}})
			regtsSelect(t, p, b, c, "SELECT cfgowner='dictionary_creator'::regrole::oid AS owned,cfgnamespace='dictionary_right'::regnamespace AS moved,cfgparser::bigint FROM pg_ts_config WHERE oid='dictionary_right.norwegian_moved_config'::regconfig", []string{"owned", "moved", "cfgparser"}, []string{"BOOL", "BOOL", "INT8"}, [][]any{{true, true, int64(3722)}})
			for _, relation := range []string{"norwegian_seed_carriers", "norwegian_seed_view"} {
				regtsSelect(t, p, b, c, "SELECT d='dictionary_right.norwegian_moved'::regdictionary AS dictionary_same,c='dictionary_right.norwegian_moved_config'::regconfig AS config_same FROM "+relation, []string{"dictionary_same", "config_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
			}
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('dictionary_right.norwegian_moved',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{ikk}"}}, "ikke"))
			norwegianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('dictionary_right.norwegian_moved_config','ikke biler') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'bil':2 'ikk':1"}}))
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='dictionary_right.norwegian_moved_config'::regconfig AND maptokentype IN (1,2,10,11,16,17) AND mapseqno=1 AND mapdict='dictionary_right.norwegian_moved'::regdictionary", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}})
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing actual native moved-seed stream")
			}
			regtsExec(t, p, b, c, "DROP VIEW norwegian_seed_view", 0)
			regtsExec(t, p, b, c, "DROP TABLE norwegian_seed_carriers", 0)
			canonical(p.Ctx)
			regtsExec(t, p, b, c, "DROP SCHEMA dictionary_right", 0)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual moved-seed replay", err)
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
				dictionaryResult(t, p, b, c, "SELECT ts_lexize('norwegian_stem','ikke') AS lexemes", "{}")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("moved stock deterministic redump", err)
			}
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		regtsExec(t, fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc, "CREATE ROLE dictionary_creator", 0)
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc)
	})
}
