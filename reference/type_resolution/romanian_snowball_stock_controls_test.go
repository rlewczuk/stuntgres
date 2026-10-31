package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func romanianDroppedStockControls(t *testing.T) {
	for _, name := range []string{"both", "dictionary", "configuration", "ordinary-replacement", "seed-name-reuse"} {
		t.Run(name, func(t *testing.T) {
			var emitted []byte
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				check := func(p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
					var dictionaries, configurations int64
					if name == "configuration" || name == "ordinary-replacement" || name == "seed-name-reuse" {
						dictionaries = 1
					}
					if name == "dictionary" || name == "ordinary-replacement" || name == "seed-name-reuse" {
						configurations = 1
					}
					regtsSelect(t, p, b, e, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='romanian_stem'", []string{"n"}, []string{"INT8"}, [][]any{{dictionaries}})
					regtsSelect(t, p, b, e, "SELECT count(*) AS n FROM pg_ts_config WHERE cfgname='romanian'", []string{"n"}, []string{"INT8"}, [][]any{{configurations}})
					switch name {
					case "configuration":
						dictionaryResult(t, p, b, e, "SELECT ts_lexize('romanian_stem','OCOLUL') AS lexemes", "{ocol}")
						regtsSelect(t, p, b, e, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'romanian'"}})
					case "dictionary", "ordinary-replacement":
						regtsSelect(t, p, b, e, "SELECT to_tsvector('romanian','OCOLUL și') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'ocolul':1 'și':2"}})
						regtsSelect(t, p, b, e, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='romanian'::regconfig", []string{"n"}, []string{"INT8"}, [][]any{{int64(19)}})
						if name == "ordinary-replacement" {
							dictionaryResult(t, p, b, e, "SELECT ts_lexize('romanian_stem','OCOLUL') AS lexemes", "{ocolul}")
						}
					case "seed-name-reuse":
						dictionaryResult(t, p, b, e, "SELECT ts_lexize('romanian_stem','CASAS') AS lexemes", "{cas}")
						regtsSelect(t, p, b, e, "SELECT to_tsvector('romanian','CASAS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cas':1"}})
						regtsSelect(t, p, b, e, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'portuguese', stopwords = 'portuguese'"}})
					}
					if configurations == 1 {
						wordDictionary := "romanian_stem"
						if name == "dictionary" {
							wordDictionary = "simple"
						}
						var maps [][]any
						for _, token := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
							maps = append(maps, []any{token, int64(1), true})
						}
						q := "SELECT maptokentype::bigint,mapseqno::bigint,mapdict=CASE WHEN maptokentype IN (1,2,10,11,16,17) THEN '" + wordDictionary + "'::regdictionary::oid ELSE 'simple'::regdictionary::oid END AS same FROM pg_ts_config_map WHERE mapcfg='romanian'::regconfig ORDER BY maptokentype,mapseqno"
						regtsSelect(t, p, b, e, q, []string{"maptokentype", "mapseqno", "same"}, []string{"INT8", "INT8", "BOOL"}, maps)
						regtsSelect(t, p, b, e, "SELECT cfgparser::bigint FROM pg_ts_config WHERE oid='romanian'::regconfig", []string{"cfgparser"}, []string{"INT8"}, [][]any{{int64(3722)}})
					}
					if name == "ordinary-replacement" {
						regtsSelect(t, p, b, e, "SELECT dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid='romanian_stem'::regdictionary", []string{"dicttemplate", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{int64(3727), nil}})
					}
					if name == "seed-name-reuse" {
						regtsSelect(t, p, b, e, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='portuguese_stem'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
						regtsSelect(t, p, b, e, "SELECT count(*) AS n FROM pg_ts_config WHERE cfgname='portuguese'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
					}

				}
				romanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('romanian_stem','OCOLUL') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{ocol}"}}))
				if b.PG {
					romanianReplayDroppedStock(t, p, b, c, name, emitted, check)
					return
				}
				old, err := c.QueryContext(p.Ctx, "SELECT ts_lexize('romanian_stem','OCOLUL') AS lexemes")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				oldTypes, err := old.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if name == "both" || name == "configuration" || name == "seed-name-reuse" {
					regtsExec(t, p, b, c, "DROP TEXT SEARCH CONFIGURATION romanian", 0)
				} else {
					// Detach actual dependencies before ordinary restricted DROP.
					regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION romanian ALTER MAPPING FOR asciiword,word,hword_part,hword_asciipart,asciihword,hword WITH simple", 0)
				}
				if name != "configuration" {
					regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY romanian_stem", 0)
				}
				if name == "ordinary-replacement" {
					regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_catalog.romanian_stem (TEMPLATE=simple)", 0)
					regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION romanian ALTER MAPPING FOR asciiword,word,hword_part,hword_asciipart,asciihword,hword WITH romanian_stem", 0)
				}
				if name == "seed-name-reuse" {
					regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY portuguese_stem RENAME TO romanian_stem", 0)
					regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION portuguese RENAME TO romanian", 0)
				}

				check(p, b, c)
				var removed bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &removed); err != nil {
					t.Fatal(err)
				}
				emitted = bytes.Clone(removed.Bytes())
				for _, reset := range []bool{false, true} {
					if reset {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						dictionaryResult(t, p, b, c, "SELECT ts_lexize('romanian_stem','OCOLUL') AS lexemes", "{ocol}")
						regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='romanian_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'romanian'"}})
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(removed.Bytes())); err != nil {
						t.Fatal(err)
					}
					check(p, b, c)
					var again bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(removed.Bytes(), again.Bytes()) {
						t.Fatal("removed seed redump", err)
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
				if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(removed.Bytes())); err != nil {
					t.Fatal(err)
				}
				check(fresh, fixture.Backend{DB: recorded, Recorder: recorder}, fc)
				if !old.Next() {
					t.Fatal(old.Err())
				}
				var value string
				if err := old.Scan(&value); err != nil || value != "{ocol}" {
					t.Fatal("retained dropped-provider rows", value, err)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal(old.Err())
				}
				if len(oldTypes) != 1 || oldTypes[0].Name() != "lexemes" || oldTypes[0].DatabaseTypeName() != "_TEXT" {
					t.Fatal("retained descriptors", oldTypes)
				}
			})
		})
	}
}

// PostgreSQL executes the actual native retirement stream from a pristine stock
// catalog. Rollback restores original stock OIDs as well as names/options/maps.
func romanianReplayDroppedStock(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, emitted []byte, check func(*fixture.Pair, fixture.Backend, fixture.Executor)) {
	t.Helper()
	if len(emitted) == 0 {
		t.Fatal("missing actual native dropped-seed stream")
	}
	var romanianDictionary, romanianConfiguration, portugueseDictionary, portugueseConfiguration int64
	const identities = "SELECT 'romanian_stem'::regdictionary::oid::bigint AS romanian_dictionary,'romanian'::regconfig::oid::bigint AS romanian_configuration,'portuguese_stem'::regdictionary::oid::bigint AS portuguese_dictionary,'portuguese'::regconfig::oid::bigint AS portuguese_configuration"
	if err := c.QueryRowContext(p.Ctx, identities).Scan(&romanianDictionary, &romanianConfiguration, &portugueseDictionary, &portugueseConfiguration); err != nil {
		t.Fatal(err)
	}
	restoredRomanian := romanianDefinitionSnapshot(t, p, b, c, "romanian_stem", "romanian")
	restoredPortuguese := romanianDefinitionSnapshot(t, p, b, c, "portuguese_stem", "portuguese")
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			t.Error("restore original PostgreSQL seeds", err)
		}
	}()
	if _, err := tx.ExecContext(p.Ctx, string(emitted)); err != nil {
		t.Fatal("actual native dropped-seed replay", name, err)
	}
	check(p, b, tx)
	if name != "configuration" {
		regtsSelect(t, p, b, tx, "SELECT count(*) AS n FROM pg_ts_dict WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, romanianDictionary)
	}
	if name == "both" || name == "configuration" || name == "seed-name-reuse" {
		regtsSelect(t, p, b, tx, "SELECT count(*) AS n FROM pg_ts_config WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, romanianConfiguration)
	}
	switch name {
	case "configuration":
		regtsSelect(t, p, b, tx, "SELECT oid::bigint FROM pg_ts_dict WHERE dictname='romanian_stem'", []string{"oid"}, []string{"INT8"}, [][]any{{romanianDictionary}})
	case "dictionary", "ordinary-replacement":
		regtsSelect(t, p, b, tx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='romanian'", []string{"oid"}, []string{"INT8"}, [][]any{{romanianConfiguration}})
		if name == "ordinary-replacement" {
			regtsSelect(t, p, b, tx, "SELECT oid>0 AND oid<>$1::oid AND oid<>$2::oid AS fresh FROM pg_ts_dict WHERE dictname='romanian_stem'", []string{"fresh"}, []string{"BOOL"}, [][]any{{true}}, romanianDictionary, portugueseDictionary)
		}
	case "seed-name-reuse":
		regtsSelect(t, p, b, tx, "SELECT oid::bigint,dicttemplate=(SELECT dicttemplate FROM pg_ts_dict WHERE oid='english_stem'::regdictionary) AS shared FROM pg_ts_dict WHERE dictname='romanian_stem'", []string{"oid", "shared"}, []string{"INT8", "BOOL"}, [][]any{{portugueseDictionary, true}})
		regtsSelect(t, p, b, tx, "SELECT oid::bigint FROM pg_ts_config WHERE cfgname='romanian'", []string{"oid"}, []string{"INT8"}, [][]any{{portugueseConfiguration}})
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal("rollback dropped-seed replay", err)
	}
	restoredRomanian()
	restoredPortuguese()
	regtsSelect(t, p, b, c, identities, []string{"romanian_dictionary", "romanian_configuration", "portuguese_dictionary", "portuguese_configuration"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{romanianDictionary, romanianConfiguration, portugueseDictionary, portugueseConfiguration}})
}
