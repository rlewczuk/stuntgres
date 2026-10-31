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

func yiddishAbsentIdentityControls(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "configuration-only"
		if replacement { name = "remapped-survivor-and-canonical-replacements" }
		t.Run(name, func(t *testing.T) {
			var emitted []byte
			nativeDumped := false
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cleanupPair := *p
				original := lithuanianDefinitionSnapshot(t, &cleanupPair, b, c, "yiddish_stem", "yiddish")
				if b.PG {
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel()
						for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE"} { if _, err := c.ExecContext(ctx, q); err != nil { t.Error("restore original PG seed/session", err); return } }
						cleanupPair.Ctx = ctx; original()
					}()
				}
				baseline := func(c *sql.Conn, ctx context.Context) (int64, int64) {
					var dictionary, configuration int64
					if err := c.QueryRowContext(ctx, "SELECT 'yiddish_stem'::regdictionary::oid::bigint,'yiddish'::regconfig::oid::bigint").Scan(&dictionary, &configuration); err != nil || dictionary <= 0 || configuration <= 0 { t.Fatal("actual initialized targets", dictionary, configuration, err) }
					return dictionary, configuration
				}
				dictionary, configuration := baseline(c, p.Ctx)
				if b.PG { regtsExec(t, p, b, c, "BEGIN", 0) }
				if replacement {
					for _, q := range []string{
						"ALTER TEXT SEARCH CONFIGURATION pg_catalog.yiddish RENAME TO yiddish_survivor",
						"ALTER TEXT SEARCH CONFIGURATION pg_catalog.yiddish_survivor ALTER MAPPING FOR asciiword,word,hword_part,hword_asciipart,asciihword,hword WITH simple",
						"DROP TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem RESTRICT",
						"CREATE TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem (TEMPLATE=snowball,Language=yiddish,StopWords=english)",
						"CREATE TEXT SEARCH CONFIGURATION pg_catalog.yiddish (COPY=pg_catalog.yiddish_survivor)",
						"ALTER TEXT SEARCH CONFIGURATION pg_catalog.yiddish ALTER MAPPING FOR asciiword,word,hword_part,hword_asciipart,asciihword,hword WITH pg_catalog.yiddish_stem",
					} { regtsExec(t, p, b, c, q, 0) }
				} else { regtsExec(t, p, b, c, "DROP TEXT SEARCH CONFIGURATION pg_catalog.yiddish RESTRICT", 0) }
				check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, dictionary, configuration int64) {
					if replacement {
						for _, step := range []fixture.Step{
							fixture.Select("SELECT 'yiddish_stem'::regdictionary::oid<>$1::oid AS replacement", []string{"replacement"}, []string{"BOOL"}, [][]any{{true}}, dictionary),
							fixture.Select("SELECT 'yiddish_survivor'::regconfig::oid=$1::oid AS intrinsic,'yiddish'::regconfig::oid<>$1::oid AS replacement", []string{"intrinsic", "replacement"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, configuration),
							fixture.Select("SELECT dictinitoption FROM pg_ts_dict WHERE oid='yiddish_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'yiddish', stopwords = 'english'"}}),
							fixture.Select("SELECT to_tsvector('yiddish_survivor','ONLY קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'only':1 'קינדער':2"}}),
							fixture.Select("SELECT to_tsvector('yiddish','ONLY קינדער') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'קינד':2"}}),
							fixture.Select("SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='yiddish_survivor'::regconfig AND maptokentype IN (1,2,10,11,16,17) AND mapdict='simple'::regdictionary", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}),
							fixture.Select("SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='yiddish'::regconfig AND maptokentype IN (1,2,10,11,16,17) AND mapdict='yiddish_stem'::regdictionary", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}),
						} { lithuanianSmokeBoth(t, p, b, c, step) }
						dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','ONLY') AS lexemes", "{}")
					} else {
						for _, step := range []fixture.Step{
							fixture.Select("SELECT oid::bigint,dictinitoption FROM pg_ts_dict WHERE oid='yiddish_stem'::regdictionary", []string{"oid", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{dictionary, "language = 'yiddish'"}}),
							fixture.Select("SELECT count(*) AS n FROM pg_ts_config WHERE cfgnamespace=11 AND cfgname='yiddish'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
							fixture.Select("SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, configuration),
						} { lithuanianSmokeBoth(t, p, b, c, step) }
						dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','ONLY') AS lexemes", "{only}")
					}
					dictionaryResult(t, p, b, c, "SELECT ts_lexize('yiddish_stem','קינדער') AS lexemes", "{קינד}")
				}
				check(p, b, c, dictionary, configuration)
				if b.PG {
					if !nativeDumped { t.Fatal("missing actual absent-identity stream") }
					regtsExec(t, p, b, c, "ROLLBACK", 0)
					original()
					regtsExec(t, p, b, c, "BEGIN", 0)
					if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil { t.Fatal("actual absent/collision replay", err) }
					check(p, b, c, dictionary, configuration)
					return
				}
				var out bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil { t.Fatal(err) }
				emitted, nativeDumped = bytes.Clone(out.Bytes()), true
				for _, reset := range []bool{false, true} {
					if reset { if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }; dictionary, configuration = baseline(c, p.Ctx) }
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
					check(p, b, c, dictionary, configuration)
					var again bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) { t.Fatal("absent/collision deterministic redump", err) }
				}
				fresh := fixture.NewFixture(t, false, true)
				fc, err := fresh.Memory.Conn(fresh.Ctx)
				if err != nil { t.Fatal(err) }; defer fc.Close()
				fd, cfg := baseline(fc, fresh.Ctx)
				if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
				check(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc, fd, cfg)
			})
		})
	}
}
