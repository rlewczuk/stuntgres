package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func nepaliStockOptionControls(t *testing.T) {
	for _, tc := range []struct{ name, alter, options, stopword, the, house, rats string }{
		{"explicit-empty", "stopwords=''", "language = 'nepali', stopwords = ''", "{सबै}", "{the}", "{घर}", "{rats}"},
		{"reordered-mixed-language", "language='NePaLi'", "stopwords = 'nepali', language = 'NePaLi'", "{}", "{the}", "{घर}", "{rats}"},
		{"substituted-resource", "stopwords=english", "language = 'nepali', stopwords = 'english'", "{सबै}", "{}", "{घर}", "{rats}"},
		{"changed-language-retains-resource", "language=english", "stopwords = 'nepali', language = 'english'", "{}", "{the}", "{घरहरू}", "{rat}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var emitted []byte
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer nepaliStockCleanup(t, p, b, c)()
				regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_stem ("+tc.alter+")", 0)
				check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='nepali_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{tc.options}})
					for _, pair := range [][2]string{{"सबै", tc.stopword}, {"THE", tc.the}, {"घरहरू", tc.house}, {"RATS", tc.rats}} {
						nepaliSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('nepali_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{pair[1]}}, pair[0]))
					}
				}
				check(p, b, c)
				if b.PG {
					if len(emitted) == 0 {
						t.Fatal("actual native bytes required")
					}
					regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_stem (language=nepali,stopwords=nepali)", 0)
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
func TestReferenceNepaliSnowballOptionControls(t *testing.T) {
	t.Run("stock-options", nepaliStockOptionControls)
	t.Run("removed-stock", nepaliRemovedStockControls)
}
