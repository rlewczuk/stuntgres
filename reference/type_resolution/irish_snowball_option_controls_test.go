package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func irishStockOptionControls(t *testing.T) {
	for _, tc := range []struct{ name, alter, options, rats, the string }{
		{"canonical", "stopwords", "language = 'irish'", "{rats}", "{te}"},
		{"removed-list", "stopwords", "language = 'irish'", "{rats}", "{te}"},
		{"reordered", "stopwords=english,language='IrIsH'", "stopwords = 'english', language = 'IrIsH'", "{rats}", "{}"},
		{"explicit-empty", "stopwords=''", "language = 'irish', stopwords = ''", "{rats}", "{te}"},
		{"mixed-language", "language='IrIsH'", "language = 'IrIsH'", "{rats}", "{te}"},
		{"explicit-english-resource", "stopwords=english", "language = 'irish', stopwords = 'english'", "{rats}", "{}"},
		{"provider-change", "language=english", "language = 'english'", "{rat}", "{the}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var emitted []byte
			nativeDumped := false
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer irishStockCleanup(t, p, b, c)()
				if tc.name == "removed-list" {
					regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_stem (stopwords=english)", 0)
				}
				regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_stem ("+tc.alter+")", 0)
				check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='irish_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.options}})
					for _, pair := range [][2]string{{"RATS", tc.rats}, {"THE", tc.the}} {
						irishSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('irish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{pair[1]}}, pair[0]))
					}
				}
				check(p, b, c)
				if b.PG {
					if !nativeDumped {
						t.Fatal("successful native dump observation required")
					}
					regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY irish_stem (language=irish,stopwords)", 0)
					if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
						t.Fatal("actual stock option replay", err)
					}
					check(p, b, c)
					return
				}
				var out bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
					t.Fatal(err)
				}
				emitted = bytes.Clone(out.Bytes())
				nativeDumped = true
				canonical := tc.name == "canonical" || tc.name == "removed-list"
				if (len(emitted) == 0) != canonical {
					t.Fatalf("stock option delta: canonical=%v emitted %q", canonical, emitted)
				}
				for _, reset := range []bool{false, true} {
					if reset {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
						t.Fatal(err)
					}
					check(p, b, c)
					var again bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
						t.Fatal("ordered options redump", err)
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
				fc, err := recorded.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer fc.Close()
				fb := fixture.Backend{DB: recorded, Recorder: recorder}
				if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				check(fresh, fb, fc)
			})
		})
	}
}
