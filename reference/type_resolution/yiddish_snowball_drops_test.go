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

func yiddishStockDrops(t *testing.T) {
	for _, cascade := range []bool{false, true} {
		name := "restrict-after-configuration"
		if cascade { name = "cascade-dictionary" }
		t.Run(name, func(t *testing.T) {
			var emitted []byte
			nativeDumped := false
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cleanupPair := *p
				original := lithuanianDefinitionSnapshot(t, &cleanupPair, b, c, "yiddish_stem", "yiddish")
				if b.PG {
					// Successful stock drops and actual emitted replay run inside
					// ordinary SQL transactions. Rollback preserves the original
					// PostgreSQL OIDs, owner/options/parser and all nineteen maps.
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel()
						for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE"} {
							if _, err := c.ExecContext(ctx, q); err != nil { t.Error("restore stock drops/session authority before fixture teardown", err); return }
						}
						cleanupPair.Ctx = ctx; original()
					}()
				}
				var dictionary, configuration int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog.yiddish_stem'::regdictionary::oid::bigint,'pg_catalog.yiddish'::regconfig::oid::bigint").Scan(&dictionary, &configuration); err != nil || dictionary <= 0 || configuration <= 0 { t.Fatal("actual drop targets", dictionary, configuration, err) }
				d := dictionaryDiagnostic("2BP01", "cannot drop text search dictionary yiddish_stem because other objects depend on it")
				d.Detail = "text search configuration yiddish depends on text search dictionary yiddish_stem"
				d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("DROP TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem RESTRICT", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					original()
					dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','קינדער') AS lexemes", "{קינד}")
					regtsSelect(t, p, b, c, "SELECT to_tsvector('yiddish','ONLY קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'only':1 'קינד':2"}})
				}
				if b.PG { regtsExec(t, p, b, c, "BEGIN", 0) }
				if cascade {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("DROP TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem CASCADE", 0)))
				} else {
					regtsExec(t, p, b, c, "DROP TEXT SEARCH CONFIGURATION pg_catalog.yiddish RESTRICT", 0)
					lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_ts_config WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, configuration))
					lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, configuration))
					regtsSelect(t, p, b, c, "SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid=$1::oid", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{dictionary, "language = 'yiddish'"}}, dictionary)
					dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','קינדער') AS lexemes", "{קינד}")
					regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem RESTRICT", 0)
				}
				yiddishDroppedState(t, p, b, c, dictionary, configuration)
				if b.PG {
					if !nativeDumped { t.Fatal("missing actual native dropped-seed stream") }
					regtsExec(t, p, b, c, "ROLLBACK", 0)
					original()
					regtsExec(t, p, b, c, "BEGIN", 0)
					if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil { t.Fatal("actual dropped-seed replay", err) }
					yiddishDroppedState(t, p, b, c, dictionary, configuration)
					return
				}
				var out bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil { t.Fatal(err) }
				emitted, nativeDumped = bytes.Clone(out.Bytes()), true
				for _, reset := range []bool{false, true} {
					if reset {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
						dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','קינדער') AS lexemes", "{קינד}")
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
					yiddishDroppedState(t, p, b, c, dictionary, configuration)
					var again bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) { t.Fatal("dropped-seed deterministic redump", err) }
				}
				fresh := fixture.NewFixture(t, false, true)
				fc, err := fresh.Memory.Conn(fresh.Ctx)
				if err != nil { t.Fatal(err) }
				defer fc.Close()
				var freshDictionary, freshConfiguration int64
				if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'pg_catalog.yiddish_stem'::regdictionary::oid::bigint,'pg_catalog.yiddish'::regconfig::oid::bigint").Scan(&freshDictionary, &freshConfiguration); err != nil { t.Fatal(err) }
				if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
				yiddishDroppedState(t, fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc, freshDictionary, freshConfiguration)
			})
		})
	}
}

func yiddishDroppedState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dictionary, configuration int64) {
	t.Helper()
	for _, step := range []fixture.Step{
		fixture.Select("SELECT count(*) AS n FROM pg_ts_dict WHERE dictnamespace='pg_catalog'::regnamespace AND dictname='yiddish_stem'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(*) AS n FROM pg_ts_config WHERE cfgnamespace='pg_catalog'::regnamespace AND cfgname='yiddish'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(*) AS n FROM pg_ts_dict WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, dictionary),
		fixture.Select("SELECT count(*) AS n FROM pg_ts_config WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, configuration),
		fixture.Select("SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg=$1::oid OR mapdict=$2::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, configuration, dictionary),
		fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE (classid='pg_ts_dict'::regclass AND objid=$1::oid) OR (classid='pg_ts_config'::regclass AND objid=$2::oid) OR (refclassid='pg_ts_dict'::regclass AND refobjid=$1::oid) OR (refclassid='pg_ts_config'::regclass AND refobjid=$2::oid)", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, dictionary, configuration),
		fixture.Select("SELECT count(*) AS n FROM pg_ts_template WHERE tmplnamespace='pg_catalog'::regnamespace AND tmplname='snowball'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='pg_catalog'::regnamespace AND proname IN ('dsnowball_init','dsnowball_lexize')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
		fixture.Select("SELECT ts_lexize('english_stem','RATS') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{rat}"}}),
	} { lithuanianSmokeBoth(t, p, b, c, step) }
}
