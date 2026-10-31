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

func romanianStockMappingControls(t *testing.T) {
	var emitted []byte
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cleanupPair := *p
		restored := func() {}
		var configOwner string
		if b.PG {
			if err := c.QueryRowContext(p.Ctx, "SELECT cfgowner::regrole::text FROM pg_ts_config WHERE oid='romanian'::regconfig").Scan(&configOwner); err != nil {
				t.Fatal("original stock configuration owner", err)
			}
			restored = romanianDefinitionSnapshot(t, &cleanupPair, b, c, "romanian_stem", "romanian")
		}
		canonical := func(ctx context.Context) {
			for _, q := range []string{
				"ALTER TEXT SEARCH CONFIGURATION pg_catalog.romanian ALTER MAPPING FOR asciiword WITH romanian_stem",
				"ALTER TEXT SEARCH CONFIGURATION pg_catalog.romanian DROP MAPPING IF EXISTS FOR uint",
				"ALTER TEXT SEARCH CONFIGURATION pg_catalog.romanian ADD MAPPING FOR uint WITH simple",
			} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("repair stock mapping", err)
				}
			}
			if b.PG {
				if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH CONFIGURATION pg_catalog.romanian OWNER TO \""+strings.ReplaceAll(configOwner, "\"", "\"\"")+"\""); err != nil {
					t.Error("restore stock configuration owner", err)
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
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION pg_catalog.romanian ALTER MAPPING FOR asciiword WITH english_stem", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION pg_catalog.romanian DROP MAPPING FOR uint", 0)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('romanian','RUNNING abruptă 123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'abrupt':2 'run':1"}}))
			regtsSelect(t, p, b, c, "SELECT maptokentype,mapseqno,mapdict='english_stem'::regdictionary::oid AS same FROM pg_ts_config_map WHERE mapcfg='romanian'::regconfig AND maptokentype=1", []string{"maptokentype", "mapseqno", "same"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(1), true}})
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='romanian'::regconfig", []string{"n"}, []string{"INT8"}, [][]any{{int64(18)}})
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='romanian'::regconfig AND maptokentype=22", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual stock mapping stream required")
			}
			canonical(p.Ctx)
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual stock mapping replay", err)
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
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='romanian'::regconfig", []string{"n"}, []string{"INT8"}, [][]any{{int64(19)}})
				romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('romanian','RUNNING abruptă 123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':3 'abrupt':2 'running':1"}}))
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("stock mapping redump", err)
			}
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-fresh")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		defer recorded.Close()
		fresh := &fixture.Pair{Ctx: p.Ctx, Native: raw, Memory: recorded, MemoryRecord: recorder}
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc)
	})
}
