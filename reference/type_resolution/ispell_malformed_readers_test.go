package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from REL_18_6 getNextFlagFromString,
// getCompoundAffixFlagValue, IsAffixFlagInUse, NIImportAffixes and
// NIImportOOAffixes, not native results. Root must resolve the initially
// source-grounded complete diagnostic fields against the pinned oracle.
func TestReferenceIspellMalformedReaders(t *testing.T) {
	type readerCase struct {
		name, dictionary, affix string
		message, text           string
		line                    int
		accepted, lookupError   bool
		prefixRecognized        bool
		suffixRecognized        bool
	}
	const longError = `invalid affix flag "A" with "long" flag value`
	const modeError = `Ispell dictionary supports only "default", "long", and "num" flag values`
	const aliasCountError = "invalid number of flag vector aliases"
	cases := []readerCase{
		{name: "long_init", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_long_demand", message: longError},
		{name: "long_deferred/odd", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_long_deferred", message: longError, accepted: true, lookupError: true},
		{name: "long_deferred/even_control", dictionary: "tr_ispell_long_even", affix: "tr_ispell_long_deferred", accepted: true, prefixRecognized: true},
		{name: "compound_operand/valid_control", dictionary: "tr_ispell_plain", affix: "tr_ispell_long_demand", accepted: true},
		{name: "compound_operand/ignored_control", dictionary: "tr_ispell_plain", affix: "tr_ispell_reader_ignored", accepted: true},
		{name: "compound_operand/first_error", dictionary: "tr_ispell_plain", affix: "tr_ispell_compound_first_error", message: "syntax error", line: 1, text: "COMPOUNDFLAG \t\n"},
		// NIImportOOAffixes reads FLAG as a raw, case-sensitive prefix,
		// not a generic field/comment. The original line is reader Context.
		{name: "flag/hash", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_hash", message: modeError, line: 3, text: "FLAG #\n"},
		{name: "flag/hash_prefix", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_hash_prefix", message: modeError, line: 3, text: "FLAG#\n"},
		{name: "flag/unknown", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_unknown", message: modeError, line: 3, text: "FLAG \tUTF-8\n"},
		{name: "flag/uppercase_operand", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_upper", message: modeError, line: 3, text: "FLAG LONG\n"},
		{name: "flag/empty", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_empty", accepted: true, suffixRecognized: true},
		{name: "flag/whitespace", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_space", accepted: true, suffixRecognized: true},
		{name: "flag/indented_ignored", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_indented", accepted: true, suffixRecognized: true},
		{name: "flag/lowercase_ignored", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_lowercase", accepted: true, suffixRecognized: true},
		{name: "flag/default_prefix", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_default", accepted: true, suffixRecognized: true},
		{name: "flag/long_prefix", dictionary: "tr_ispell_long_even", affix: "tr_ispell_flag_long_prefix", accepted: true, suffixRecognized: true},
		{name: "flag/num_prefix", dictionary: "tr_ispell_flag_num", affix: "tr_ispell_flag_num_prefix", accepted: true, suffixRecognized: true},
		// "default" and empty operands do not reset a preceding mode.
		// All affix rules use the final first-pass mode, even earlier rules.
		{name: "flag/long_default_no_reset", dictionary: "tr_ispell_long_even", affix: "tr_ispell_flag_long_default", accepted: true, suffixRecognized: true},
		{name: "flag/num_default_no_reset", dictionary: "tr_ispell_flag_num", affix: "tr_ispell_flag_num_default", accepted: true, suffixRecognized: true},
		{name: "flag/long_empty_no_reset", dictionary: "tr_ispell_long_even", affix: "tr_ispell_flag_long_empty", accepted: true, suffixRecognized: true},
		{name: "flag/last_num", dictionary: "tr_ispell_flag_num", affix: "tr_ispell_flag_last_num", accepted: true, suffixRecognized: true},
		{name: "flag/last_long", dictionary: "tr_ispell_long_even", affix: "tr_ispell_flag_last_long", accepted: true, suffixRecognized: true},
		// Without a new-format marker, # is a valid single legacy flag.
		{name: "flag/legacy_hash", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_legacy_hash", accepted: true},
		{name: "flag/legacy_hash_prefix", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_flag_legacy_prefix", accepted: true},
		// Alias dispatch precedes the short-line check; its omitted fields
		// are empty strings. AF/PFX/SFX use lowercased command prefixes.
		{name: "second_pass/af_bare", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_af_bare", message: aliasCountError, line: 3, text: "AF\n"},
		{name: "second_pass/af_space", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_af_space", message: aliasCountError, line: 3, text: "AF \t\n"},
		{name: "second_pass/af_comment", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_af_comment", message: aliasCountError, line: 3, text: "AF # comment\n"},
		{name: "second_pass/af_attached_comment", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_af_attached_comment", message: aliasCountError, line: 3, text: "AF#\n"},
		{name: "second_pass/af_prefix_zero", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_af_prefix_zero", message: aliasCountError, line: 3, text: "aFextra 0\n"},
		{name: "second_pass/af_prefix_bare", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_af_prefix_bare", message: aliasCountError, line: 3, text: "AFextra\n"},
		{name: "second_pass/af_prefix_valid", dictionary: "tr_ispell_af_alias", affix: "tr_ispell_af_prefix_valid", accepted: true, suffixRecognized: true},
		{name: "second_pass/af_empty_entry", dictionary: "tr_ispell_af_alias", affix: "tr_ispell_af_empty_entry", accepted: true},
		{name: "second_pass/af_empty_comment", dictionary: "tr_ispell_af_alias", affix: "tr_ispell_af_empty_comment", accepted: true},
		{name: "second_pass/af_empty_excess", dictionary: "tr_ispell_af_alias", affix: "tr_ispell_af_empty_excess", message: "number of aliases exceeds specified number 1", line: 5, text: "AF\n"},
		{name: "second_pass/sfx_prefix", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_sfx_prefix", accepted: true, suffixRecognized: true},
		{name: "second_pass/pfx_prefix", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_pfx_prefix", accepted: true, prefixRecognized: true},
		// The last recognized header, not the rule spelling, owns direction.
		{name: "second_pass/sfx_header", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_sfx_header", accepted: true, suffixRecognized: true},
		{name: "second_pass/pfx_header", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_pfx_header", accepted: true, prefixRecognized: true},
		{name: "second_pass/ignored_control", dictionary: "tr_ispell_long_odd", affix: "tr_ispell_second_ignored", accepted: true, suffixRecognized: true},
	}
	for _, command := range []string{
		"COMPOUNDFLAG", "COMPOUNDBEGIN", "COMPOUNDMIDDLE", "COMPOUNDLAST",
		"COMPOUNDEND", "ONLYINCOMPOUND", "COMPOUNDPERMITFLAG", "COMPOUNDFORBIDFLAG",
	} {
		for _, form := range []struct{ name, ending string }{
			{"bare", "\n"}, {"space", " \t\n"},
		} {
			name := strings.ToLower(command) + "_" + form.name
			text := command + form.ending
			cases = append(cases, readerCase{
				name:       "compound_operand/myspell/" + name,
				dictionary: "tr_ispell_plain", affix: "tr_ispell_missing_" + name,
				message: "syntax error", line: 2, text: text,
			})
			unmarked := readerCase{
				name:       "compound_operand/unmarked/" + name,
				dictionary: "tr_ispell_plain", affix: "tr_ispell_unmarked_" + name,
				accepted: true,
			}
			// Only COMPOUNDFLAG is itself a new-format detection marker.
			// The other seven unmarked lines remain ignorable legacy input.
			if command == "COMPOUNDFLAG" {
				unmarked.accepted = false
				unmarked.message, unmarked.line, unmarked.text = "syntax error", 1, text
			}
			cases = append(cases, unmarked)
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
					ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						type dictionaryState struct {
							oid, namespace, owner, template, dependencies int64
							options                                       string
						}
						readState := func(name string) dictionaryState {
							t.Helper()
							var state dictionaryState
							q := "SELECT oid::bigint,dictnamespace::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE dictname=$1 AND dictnamespace='public'::regnamespace"
							if err := c.QueryRowContext(p.Ctx, q, name).Scan(&state.oid, &state.namespace, &state.owner, &state.template, &state.options); err != nil {
								t.Fatal(err)
							}
							if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=3600 AND objid=$1::oid", state.oid).Scan(&state.dependencies); err != nil {
								t.Fatal(err)
							}
							if state.oid <= 0 || state.namespace <= 0 || state.owner <= 0 || state.template != 3733 || state.dependencies != 1 {
								t.Fatalf("dictionary identity/dependency contract: %+v", state)
							}
							// The sole normal edge is the actual namespace; pinned
							// builtin-template edges must not be manufactured.
							regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid=$1::oid AND objsubid=0 AND refclassid=2615 AND refobjid=$2::oid AND refobjsubid=0 AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, state.oid, state.namespace)
							return state
						}
						dependencyCount := func() int64 {
							t.Helper()
							var count int64
							if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=3600").Scan(&count); err != nil {
								t.Fatal(err)
							}
							return count
						}
						ispellCreate(t, p, b, c, "ispell_readers_kept", "legacy", "")
						before := readState("ispell_readers_kept")
						if before.options != "dictfile = 'tr_ispell_legacy', afffile = 'tr_ispell_legacy'" {
							t.Fatal("incumbent ordered options", before.options)
						}
						constantSQL := "SELECT ts_lexize('ispell_readers_kept'::regdictionary,'unbook'::text) AS lexemes"
						constantStmt, err := c.PrepareContext(p.Ctx, constantSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer constantStmt.Close()
						constant := fixture.HeldStatement{Executor: c, Statement: constantStmt, SQL: constantSQL}
						dictionaryResult(t, p, b, constant, constantSQL, "{book}")
						runtimeSQL := "SELECT ts_lexize('ispell_readers_kept'::regdictionary,$1::text) AS lexemes"
						runtimeStmt, err := c.PrepareContext(p.Ctx, runtimeSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer runtimeStmt.Close()
						held := fixture.HeldStatement{Executor: c, Statement: runtimeStmt, SQL: runtimeSQL}
						dictionaryResult(t, p, b, held, runtimeSQL, "{book}", "unbook")
						// lib/pq cannot execute a second query on a connection with
						// open Rows. Retain the old result on its own connection.
						reader, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer reader.Close()
						old, err := reader.QueryContext(p.Ctx, constantSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer old.Close()
						types, err := old.ColumnTypes()
						if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" {
							t.Fatal("retained TEXT[] metadata", types, err)
						}
						options := "DictFile=" + tc.dictionary + ",AffFile=" + tc.affix
						createSQL := "CREATE TEXT SEARCH DICTIONARY ispell_readers_new (TEMPLATE=pg_catalog.ispell," + options + ")"
						alterSQL := "ALTER TEXT SEARCH DICTIONARY ispell_readers_kept (" + options + ")"
						diagnostic := dictionaryDiagnostic("F0000", tc.message)
						if tc.line != 0 {
							diagnostic.Context = ispellReadContext(b, tc.affix+".affix", tc.line, tc.text)
						}
						// Long dictionary flags are demanded after the file-reader
						// callback has ended. No invented resource-line Context.
						runError := func(q string, e fixture.Executor, rows bool, args ...any) {
							t.Helper()
							s := fixture.ErrorDiagnostic(q, diagnostic, args...)
							s.SelectRows, s.ErrorStage = rows, "execute"
							fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
						}
						utilityError := func(q string) {
							t.Helper()
							s := fixture.ErrorDiagnostic(q, diagnostic)
							s.Prepared, s.ErrorStage = prepared, "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
						unchanged := func(want dictionaryState) {
							t.Helper()
							if after := readState("ispell_readers_kept"); after != want {
								t.Fatalf("dictionary state changed: before=%+v after=%+v", want, after)
							}
							dictionaryResult(t, p, b, constant, constantSQL, "{book}")
						}
						if !tc.accepted {
							dependencies := dependencyCount()
							absent := func() {
								t.Helper()
								regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='ispell_readers_new'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
								if after := dependencyCount(); after != dependencies {
									t.Fatalf("rejected CREATE published dependencies: %d -> %d", dependencies, after)
								}
							}
							utilityError(createSQL)
							absent()
							// A rejected replacement must not publish even the valid
							// earlier/later option changes from the same declaration.
							alterSQL = "ALTER TEXT SEARCH DICTIONARY ispell_readers_kept (StopWords=tr_ispell_book," + options + ")"
							utilityError(alterSQL)
							unchanged(before)
							dictionaryResult(t, p, b, held, runtimeSQL, "{book}", "unbook")
							ispellToken(t, p, b, c, "ispell_readers_kept", "unbook", "{book}")
							regtsExec(t, p, b, c, "BEGIN", 0)
							regtsExec(t, p, b, c, "SAVEPOINT ispell_reader_create", 0)
							utilityError(createSQL)
							regtsExec(t, p, b, c, "ROLLBACK TO ispell_reader_create", 0)
							absent()
							regtsExec(t, p, b, c, "SAVEPOINT ispell_reader_alter", 0)
							utilityError(alterSQL)
							regtsExec(t, p, b, c, "ROLLBACK TO ispell_reader_alter", 0)
							unchanged(before)
							dictionaryResult(t, p, b, held, runtimeSQL, "{book}", "unbook")
							regtsExec(t, p, b, c, "ROLLBACK", 0)
							unchanged(before)
						} else {
							create := fixture.Exec(createSQL, 0)
							create.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, b.Recorder, create)
							created := readState("ispell_readers_new")
							wantOptions := "dictfile = '" + tc.dictionary + "', afffile = '" + tc.affix + "'"
							if created.options != wantOptions || created.oid == before.oid || created.owner != before.owner || created.namespace != before.namespace {
								t.Fatalf("accepted CREATE state: %+v, want options %q", created, wantOptions)
							}
							checkLookup := func(name string, want dictionaryState) {
								t.Helper()
								q := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
								for _, prepareLookup := range []bool{false, true} {
									var executor fixture.Executor = c
									if prepareLookup {
										stmt, err := c.PrepareContext(p.Ctx, q)
										if err != nil {
											t.Fatal(err)
										}
										defer stmt.Close()
										executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
									}
									dictionaryResult(t, p, b, executor, q, "{root}", "root")
									dictionaryResult(t, p, b, executor, q, nil, "unknown")
									var suffix any
									if tc.suffixRecognized {
										suffix = "{root}"
									}
									dictionaryResult(t, p, b, executor, q, suffix, "roots")
									for range 2 {
										if tc.lookupError {
											runError(q, executor, true, "preroot")
										} else {
											var prefix any
											if tc.prefixRecognized {
												prefix = "{root}"
											}
											dictionaryResult(t, p, b, executor, q, prefix, "preroot")
										}
										if after := readState(name); after != want {
											t.Fatalf("lookup changed accepted dictionary: before=%+v after=%+v", want, after)
										}
										dictionaryResult(t, p, b, executor, q, "{root}", "root")
									}
								}
							}
							checkLookup("ispell_readers_new", created)
							alter := fixture.Exec(alterSQL, 0)
							alter.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, b.Recorder, alter)
							current := readState("ispell_readers_kept")
							expected := before
							expected.options = wantOptions
							if current != expected {
								t.Fatalf("accepted ALTER state: got=%+v want=%+v", current, expected)
							}
							checkLookup("ispell_readers_kept", current)
							dictionaryResult(t, p, b, held, runtimeSQL, "{root}", "root")
							dictionaryResult(t, p, b, held, runtimeSQL, nil, "unbook")
							if tc.lookupError {
								runError(runtimeSQL, held, true, "preroot")
								regtsExec(t, p, b, c, "BEGIN", 0)
								regtsExec(t, p, b, c, "SAVEPOINT ispell_reader_lookup", 0)
								runError(runtimeSQL, held, true, "preroot")
								regtsExec(t, p, b, c, "ROLLBACK TO ispell_reader_lookup", 0)
								unchanged(current)
								dictionaryResult(t, p, b, held, runtimeSQL, "{root}", "root")
								regtsExec(t, p, b, c, "ROLLBACK", 0)
							}
							unchanged(current)
						}
						if !old.Next() {
							t.Fatal("retained row missing", old.Err())
						}
						var oldValue string
						if err := old.Scan(&oldValue); err != nil || oldValue != "{book}" {
							t.Fatal("retained accepted value changed", oldValue, err)
						}
						types, err = old.ColumnTypes()
						if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" {
							t.Fatal("retained metadata changed", types, err)
						}
						if old.Next() || old.Err() != nil {
							t.Fatal("retained result changed", old.Err())
						}
					})
				})
			}
		})
	}
}
