package reference_test

import (
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These finite LP64 expectations follow REL_18_6 spell.c's strtol ->
// int32/int -> flag-bound check and are independently confirmed by the pinned
// PostgreSQL 18.6 oracle. AF counts and alias references keep distinct checks.
type ispellNumericCase struct {
	name, dictionary, affix string
	message, readerText string
	line int
	deferred, compound bool
	configuration bool
	roots, unroot, rootsroot any
}

func ispellNumericCases() []ispellNumericCase {
	const suffix = "FLAG num\nSFX 1 N 1\nSFX 1 0 s .\nPFX 2 N 1\nPFX 2 0 un .\n"
	cases := []ispellNumericCase{}
	for _, boundary := range []struct{ name, raw, message string; compound bool }{
		{"signed32_one_past", "2147483648", `affix flag "2147483648" is out of range`, false},
		{"wrapped_one", "4294967297", "", true},
		{"negative_wrapped_one", "-4294967295", "", true},
		{"long_max", "9223372036854775807", `affix flag "9223372036854775807" is out of range`, false},
		{"long_min", "-9223372036854775808", "", false},
		{"long_positive_overflow", "9223372036854775808", `invalid affix flag "9223372036854775808"`, false},
		{"long_negative_overflow", "-9223372036854775809", `invalid affix flag "-9223372036854775809"`, false},
		{"negative_one", "-1", `affix flag "-1" is out of range`, false},
		{"flag_max", "65536", "", false},
		{"flag_one_past", "65537", `affix flag "65537" is out of range`, false},
		{"zero", "0", "", false},
		{"prefix_only_directive", "+4294967297tail", "", true},
	} {
		line := "COMPOUNDFLAG " + boundary.raw + "\n"
		cases = append(cases, ispellNumericCase{name: "directive_" + boundary.name, dictionary: "root/1\n", affix: "FLAG num\n" + line + "SFX 1 N 1\nSFX 1 0 s .\n", message: boundary.message, readerText: line, line: 2, compound: boundary.compound, roots: "{root}"})
	}
	for _, boundary := range []struct{ name, raw, message string; roots any }{
		{"signed32_one_past", "2147483648", `affix flag "2147483648" is out of range`, nil},
		{"wrapped_one", "4294967297", "", "{root}"},
		{"negative_wrapped_one", "-4294967295", "", "{root}"},
		{"long_positive_overflow", "9223372036854775808", `invalid affix flag "9223372036854775808"`, nil},
		{"long_negative_overflow", "-9223372036854775809", `invalid affix flag "-9223372036854775809"`, nil},
		{"negative_one", "-1", `affix flag "-1" is out of range`, nil},
		{"flag_max", "65536", "", nil},
		{"flag_one_past", "65537", `affix flag "65537" is out of range`, nil},
		{"zero", "0", "", nil},
		{"original_tail", "+4294967297tail", `invalid character in affix flag "tail"`, nil},
	} {
		cases = append(cases, ispellNumericCase{name: "dictionary_" + boundary.name, dictionary: "root/" + boundary.raw + "\n", affix: suffix, message: boundary.message, deferred: boundary.message != "", roots: boundary.roots})
	}
	cases = append(cases,
		ispellNumericCase{name: "valid_prefix_dormant_tail", dictionary: "root/4294967297,2147483648\n", affix: suffix, message: `affix flag "2147483648" is out of range`, deferred: true, roots: "{root}"},
		ispellNumericCase{name: "wrapped_alias_count", dictionary: "root/1\n", affix: "FLAG num\nAF 4294967297\nAF 1\nSFX 1 N 1\nSFX 1 0 s .\n", roots: "{root}"},
		ispellNumericCase{name: "negative_alias_count", dictionary: "root/1\n", affix: "FLAG num\nAF 2147483648\n", message: "invalid number of flag vector aliases", line: 2, readerText: "AF 2147483648\n"},
		ispellNumericCase{name: "wrapped_dictionary_alias", dictionary: "root/4294967297\n", affix: "FLAG num\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s .\n", roots: "{root}"},
		ispellNumericCase{name: "dictionary_alias_one_past", dictionary: "root/4294967298\n", affix: "FLAG num\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s .\n", message: `invalid affix alias "4294967298"`},
		ispellNumericCase{name: "wrapped_alias_vector", dictionary: "root/1\n", affix: "FLAG num\nAF 1\nAF 4294967297\nSFX 1 N 1\nSFX 1 0 s .\n", roots: "{root}"},
		ispellNumericCase{name: "raw_suffix_identifier", dictionary: "root/1\n", affix: "FLAG num\nSFX 4294967297 N 1\nSFX 4294967297 0 s .\n"},
		ispellNumericCase{name: "raw_prefix_identifier", dictionary: "root/1\n", affix: "FLAG num\nPFX 4294967297 N 1\nPFX 4294967297 0 un .\n"},
		ispellNumericCase{name: "negative_wrapped_dictionary_alias", dictionary: "root/-4294967295\n", affix: "FLAG num\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s .\n", roots: "{root}"},
		ispellNumericCase{name: "dictionary_alias_trailing", dictionary: "root/1tail\n", affix: "FLAG num\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s .\n", message: `invalid affix alias "1tail"`},
		ispellNumericCase{name: "continuation_wrapped", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nSFX 1 N 1\nSFX 1 0 s/4294967297 .\n", roots: "{root}", compound: true, rootsroot: "{root,root}"},
		ispellNumericCase{name: "continuation_range", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nSFX 1 N 1\nSFX 1 0 s/2147483648 .\n", message: `affix flag "2147483648" is out of range`, line: 4, readerText: "SFX 1 0 s/2147483648 .\n"},
		ispellNumericCase{name: "continuation_overflow", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nSFX 1 N 1\nSFX 1 0 s/9223372036854775808 .\n", message: `invalid affix flag "9223372036854775808"`, line: 4, readerText: "SFX 1 0 s/9223372036854775808 .\n"},
		ispellNumericCase{name: "continuation_alias_one_past_empty", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s/4294967298 .\n", roots: "{root}", compound: true},
		ispellNumericCase{name: "continuation_alias_negative_empty", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s/-1 .\n", roots: "{root}", compound: true},
		ispellNumericCase{name: "continuation_alias_trailing", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s/4294967297tail .\n", roots: "{root}", compound: true, rootsroot: "{root,root}"},
		ispellNumericCase{name: "inclusive_flag_bound_morphology", dictionary: "root/65536\n", affix: "FLAG num\nSFX 65536 N 1\nSFX 65536 0 s .\n", roots: "{root}"},
		ispellNumericCase{name: "wrapped_prefix_membership", dictionary: "root/4294967298\n", affix: suffix, unroot: "{root}"},
		ispellNumericCase{name: "continuation_without_compound_dormant", dictionary: "root/1\n", affix: "FLAG num\nSFX 1 N 1\nSFX 1 0 s/9223372036854775808 .\n", roots: "{root}"},
		ispellNumericCase{name: "continuation_alias_outside", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s/4294967299 .\n", message: `invalid affix alias "4294967299"`, line: 6, readerText: "SFX 1 0 s/4294967299 .\n"},
	)
	return cases
}

// Install the same real bytes through the existing package manifest in both
// backends. Each case has one dictionary row and at most one alias entry.
func ispellNumericFiles() []stuntgres.TextSearchFile {
	var files []stuntgres.TextSearchFile
	for _, tc := range ispellNumericCases() {
		name := "tr_ispell_numeric_" + tc.name
		files = append(files, stuntgres.TextSearchFile{Name: name + ".dict", Data: []byte(tc.dictionary)}, stuntgres.TextSearchFile{Name: name + ".affix", Data: []byte(tc.affix)})
	}
	return files
}

func TestReferenceIspellNumericConversion(t *testing.T) {
	for _, tc := range ispellNumericCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
					p := fixture.NewScenarioWithDriverConfig(t, ispellConfigure)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							ispellRunNumericCase(t, p, b, tc, prepared, "tr_ispell_numeric_"+tc.name)
						})
					}
				})
			}
		})
	}
}

// The same publication and lifetime assertions serve completed numeric tables
// and interleaved aliases. Backend discovery belongs to each top-level test.
func ispellRunNumericCase(t *testing.T, p *fixture.Pair, b fixture.Backend, tc ispellNumericCase, prepared bool, resource string) {
							c, err := b.DB.Conn(p.Ctx)
							if err != nil { t.Fatal(err) }
							defer c.Close()
							run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
							utility := func(query string) {
								step := fixture.Exec(query, 0)
								step.Prepared = prepared
								run(step)
							}
							type dictionaryState struct {
								oid, namespace, owner, template, dependencies int64
								options string
							}
							state := func(name string) dictionaryState {
								t.Helper()
								var s dictionaryState
								if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dictnamespace::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE dictname=$1 AND dictnamespace='public'::regnamespace", name).Scan(&s.oid, &s.namespace, &s.owner, &s.template, &s.options); err != nil { t.Fatal(err) }
								if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=3600 AND objid=$1::oid", s.oid).Scan(&s.dependencies); err != nil { t.Fatal(err) }
								if s.oid <= 0 || s.namespace <= 0 || s.owner <= 0 || s.template != 3733 || s.dependencies != 1 { t.Fatalf("dictionary state: %+v", s) }
								regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid=$1::oid AND objsubid=0 AND refclassid=2615 AND refobjid=$2::oid AND refobjsubid=0 AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, s.oid, s.namespace)
								return s
							}
							utility("CREATE TEXT SEARCH DICTIONARY ispell_numeric_kept (TEMPLATE=pg_catalog.ispell,DictFile=tr_ispell_af_alias,AffFile=tr_ispell_numeric_dictionary_wrapped_one)")
							before := state("ispell_numeric_kept")
							if before.options != "dictfile = 'tr_ispell_af_alias', afffile = 'tr_ispell_numeric_dictionary_wrapped_one'" { t.Fatal("incumbent options", before.options) }
							const runtimeSQL = "SELECT ts_lexize('ispell_numeric_kept'::regdictionary,$1::text) AS lexemes"
							const constantSQL = "SELECT ts_lexize('ispell_numeric_kept'::regdictionary,'roots'::text) AS lexemes"
							prepare := func(query string) fixture.HeldStatement {
								t.Helper()
								stmt, err := c.PrepareContext(p.Ctx, query)
								if err != nil { t.Fatal(err) }
								t.Cleanup(func() { stmt.Close() })
								return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
							}
							held, constant := prepare(runtimeSQL), prepare(constantSQL)
							dictionaryResult(t, p, b, held, runtimeSQL, "{root}", "roots")
							dictionaryResult(t, p, b, constant, constantSQL, "{root}")
							reader, err := b.DB.Conn(p.Ctx)
							if err != nil { t.Fatal(err) }
							defer reader.Close()
							old, err := reader.QueryContext(p.Ctx, constantSQL)
							if err != nil { t.Fatal(err) }
							defer old.Close()
							columns, err := old.Columns()
							if err != nil || len(columns) != 1 || columns[0] != "lexemes" { t.Fatal(columns, err) }
							types, err := old.ColumnTypes()
							if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" { t.Fatal(types, err) }
							options := "DictFile=" + resource + ",AffFile=" + resource
							wantOptions := "dictfile = '" + resource + "', afffile = '" + resource + "'"
							diagnostic := dictionaryDiagnostic("F0000", tc.message)
							if tc.line != 0 { diagnostic.Context = ispellReadContext(b, resource + ".affix", tc.line, tc.readerText) }
							fail := func(query string, rows bool, executor fixture.Executor, args ...any) {
								t.Helper()
								step := fixture.ErrorDiagnostic(query, diagnostic, args...)
								step.SelectRows, step.ErrorStage = rows, "execute"
								if !rows { step.Prepared = prepared }
								fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
							}
							checkConfiguration := func(name string, roots, rootsroot any) {
								t.Helper()
								if !tc.configuration { return }
								query := "SELECT to_tsvector('" + name + "_cfg'::regconfig,$1::text)::text AS lexemes"
								for _, lookup := range []struct{ token string; recognized bool; value string }{
									{"roots", roots != nil, "'root':1"},
									{"rootsroot", rootsroot != nil, "'root':1"},
								} {
									want := ""
									if lookup.recognized { want = lookup.value }
									step := fixture.Select(query, []string{"lexemes"}, []string{"TEXT"}, [][]any{{want}}, lookup.token)
									run(step)
									run(fixture.Prepared(step))
								}
							}
							configure := func(name string) {
								if !tc.configuration { return }
								utility("CREATE TEXT SEARCH CONFIGURATION " + name + "_cfg (PARSER=pg_catalog.default)")
								utility("ALTER TEXT SEARCH CONFIGURATION " + name + "_cfg ADD MAPPING FOR asciiword WITH " + name)
							}
							configure("ispell_numeric_kept")
							checkConfiguration("ispell_numeric_kept", "{root}", nil)
							checkLookup := func(name string) {
								t.Helper()
								query := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
								stmt := prepare(query) // malformed vectors must not fail runtime Prepare
								for _, executor := range []fixture.Executor{c, stmt} {
									for range 2 {
										dictionaryResult(t, p, b, executor, query, "{root}", "root")
										dictionaryResult(t, p, b, executor, query, nil, "unknown")
										if tc.deferred && tc.roots == nil { fail(query, true, executor, "roots") } else { dictionaryResult(t, p, b, executor, query, tc.roots, "roots") }
										if tc.deferred { fail(query, true, executor, "unroot") } else { dictionaryResult(t, p, b, executor, query, tc.unroot, "unroot") }
										var compound any
										if tc.compound { compound = "{root,root}" }
										dictionaryResult(t, p, b, executor, query, compound, "rootroot")
										// An affixed MIDDLE component needs the actual continuation
										// compound bit; base rootroot alone cannot prove transport.
										if !tc.deferred { dictionaryResult(t, p, b, executor, query, tc.rootsroot, "rootsroot") }
										dictionaryResult(t, p, b, executor, query, "{root}", "root")
									}
								}
							}
							create := "CREATE TEXT SEARCH DICTIONARY ispell_numeric_new (TEMPLATE=pg_catalog.ispell," + options + ")"
							alter := "ALTER TEXT SEARCH DICTIONARY ispell_numeric_kept (" + options + ")"
							if tc.message != "" && !tc.deferred {
								var dependencies int64
								if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=3600").Scan(&dependencies); err != nil { t.Fatal(err) }
								for _, query := range []string{create, "ALTER TEXT SEARCH DICTIONARY ispell_numeric_kept (StopWords=tr_ispell_book," + options + ")"} {
									fail(query, false, c)
									regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='ispell_numeric_new' AND dictnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
									regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600", []string{"n"}, []string{"INT8"}, [][]any{{dependencies}})
									if after := state("ispell_numeric_kept"); after != before { t.Fatalf("failed publication: %+v != %+v", after, before) }
									for _, executor := range []fixture.Executor{c, held} { dictionaryResult(t, p, b, executor, runtimeSQL, "{root}", "roots") }
									dictionaryResult(t, p, b, constant, constantSQL, "{root}")
									dictionaryResult(t, p, b, c, constantSQL, "{root}")
									run(fixture.Prepared(fixture.Select(constantSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{root}"}})))
									checkConfiguration("ispell_numeric_kept", "{root}", nil)
								}
							} else {
								utility(create)
								created := state("ispell_numeric_new")
								if created.options != wantOptions || created.oid == before.oid || created.owner != before.owner || created.namespace != before.namespace { t.Fatalf("accepted CREATE: %+v", created) }
								configure("ispell_numeric_new")
								checkConfiguration("ispell_numeric_new", tc.roots, tc.rootsroot)
								checkLookup("ispell_numeric_new")
								if after := state("ispell_numeric_new"); after != created { t.Fatal("lookup changed CREATE state", after, created) }
								utility(alter)
								current := before
								current.options = wantOptions
								if after := state("ispell_numeric_kept"); after != current { t.Fatalf("accepted ALTER: %+v != %+v", after, current) }
								checkLookup("ispell_numeric_kept")
								for _, executor := range []fixture.Executor{c, held} {
									if tc.deferred && tc.roots == nil { fail(runtimeSQL, true, executor, "roots") } else { dictionaryResult(t, p, b, executor, runtimeSQL, tc.roots, "roots") }
									dictionaryResult(t, p, b, executor, runtimeSQL, "{root}", "root")
								}
								if after := state("ispell_numeric_kept"); after != current { t.Fatal("demand changed ALTER state", after, current) }
								checkConfiguration("ispell_numeric_kept", tc.roots, tc.rootsroot)
								// Warmed immutable constants keep their accepted incumbent value.
								dictionaryResult(t, p, b, constant, constantSQL, "{root}")
							}
							if !old.Next() { t.Fatal("retained row missing", old.Err()) }
							var value any
							if err := old.Scan(&value); err != nil || fixture.Normalize(value) != "{root}" { t.Fatal("retained value", value, err) }
							if old.Next() || old.Err() != nil { t.Fatal("retained rows changed", old.Err()) }
}
