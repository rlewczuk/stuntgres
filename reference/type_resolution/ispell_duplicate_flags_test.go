package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 NISortDictionary/mkSPNode/MergeAffix coalesce distinct raw flag
// identities before membership decoding. AB and BA both recognize roots, so
// neither equal-word sort order is an expectation. Identical A identities must
// not become AA: the malformed flag stays dormant until suffix demand.
func TestReferenceIspellDuplicateFlags(t *testing.T) {
	for _, tc := range []struct {
		name, dictionary, affix string
		lookupError             bool
	}{
		{"distinct", "tr_ispell_duplicate_long", "tr_ispell_duplicate_long", false},
		{"distinct_reverse", "tr_ispell_duplicate_long_reverse", "tr_ispell_duplicate_long", false},
		{"equal_identity", "tr_ispell_duplicate_long_equal", "tr_ispell_duplicate_long_equal", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
					p := fixture.NewScenarioWithDriverConfig(t, ispellConfigure)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							run := func(step fixture.Step) {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
							}
							utility := func(query string) {
								step := fixture.Exec(query, 0)
								step.Prepared = prepared
								run(step)
							}
							type dictionaryState struct {
								oid, namespace, owner, template, dependencies int64
								options                                       string
							}
							readState := func(name string) dictionaryState {
								t.Helper()
								var state dictionaryState
								query := "SELECT oid::bigint,dictnamespace::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE dictname=$1 AND dictnamespace='public'::regnamespace"
								if err := c.QueryRowContext(p.Ctx, query, name).Scan(&state.oid, &state.namespace, &state.owner, &state.template, &state.options); err != nil {
									t.Fatal(err)
								}
								if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=3600 AND objid=$1::oid", state.oid).Scan(&state.dependencies); err != nil {
									t.Fatal(err)
								}
								if state.oid <= 0 || state.namespace <= 0 || state.owner <= 0 || state.template != 3733 || state.dependencies != 1 {
									t.Fatalf("dictionary identity/dependency contract: %+v", state)
								}
								regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600 AND objid=$1::oid AND objsubid=0 AND refclassid=2615 AND refobjid=$2::oid AND refobjsubid=0 AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, state.oid, state.namespace)
								return state
							}
							utility("CREATE TEXT SEARCH DICTIONARY ispell_duplicate_kept (TEMPLATE=pg_catalog.ispell,DictFile=tr_ispell_duplicate_long_complete,AffFile=tr_ispell_duplicate_long)")
							before := readState("ispell_duplicate_kept")
							if before.options != "dictfile = 'tr_ispell_duplicate_long_complete', afffile = 'tr_ispell_duplicate_long'" {
								t.Fatal("incumbent ordered options", before.options)
							}
							run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION ispell_duplicate_config (COPY=pg_catalog.simple)", 0))
							run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION ispell_duplicate_config ALTER MAPPING FOR asciiword,word WITH ispell_duplicate_kept", 0))
							const runtimeSQL = "SELECT ts_lexize('ispell_duplicate_kept'::regdictionary,$1::text) AS lexemes"
							const constantSQL = "SELECT ts_lexize('ispell_duplicate_kept'::regdictionary,'roots'::text) AS lexemes"
							const vectorSQL = "SELECT to_tsvector('ispell_duplicate_config'::regconfig,$1::text)::text AS normalized_vector"
							statements := make(map[string]*sql.Stmt)
							for _, query := range []string{runtimeSQL, constantSQL, vectorSQL} {
								stmt, err := c.PrepareContext(p.Ctx, query)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								statements[query] = stmt
							}
							held := func(query string) fixture.HeldStatement {
								return fixture.HeldStatement{Executor: c, Statement: statements[query], SQL: query}
							}
							dictionaryResult(t, p, b, held(constantSQL), constantSQL, "{root}")
							dictionaryResult(t, p, b, held(runtimeSQL), runtimeSQL, "{root}", "roots")
							fixture.RunStep(t, p.Ctx, held(vectorSQL), b.Recorder, fixture.Select(vectorSQL, []string{"normalized_vector"}, []string{"TEXT"}, [][]any{{"'root':1"}}, "roots"))
							// Keep an unread typed result on another borrower so pq can
							// execute ALTER without an outstanding stream on c.
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
							checkRetainedMetadata := func() {
								t.Helper()
								columns, err := old.Columns()
								if err != nil || len(columns) != 1 || columns[0] != "lexemes" {
									t.Fatal("retained names", columns, err)
								}
								types, err := old.ColumnTypes()
								if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" {
									t.Fatal("retained TEXT[] metadata", types, err)
								}
							}
							checkRetainedMetadata()
							options := "DictFile=" + tc.dictionary + ",AffFile=" + tc.affix
							wantOptions := "dictfile = '" + tc.dictionary + "', afffile = '" + tc.affix + "'"
							utility("CREATE TEXT SEARCH DICTIONARY ispell_duplicate_new (TEMPLATE=pg_catalog.ispell," + options + ")")
							created := readState("ispell_duplicate_new")
							if created.options != wantOptions || created.oid == before.oid || created.owner != before.owner || created.namespace != before.namespace {
								t.Fatalf("accepted CREATE state: %+v", created)
							}
							// Runtime-deferred errors occur after reader callbacks have
							// ended: every optional diagnostic field, including Context,
							// is empty. Prepare succeeds for runtime TEXT arguments.
							diagnostic := dictionaryDiagnostic("F0000", `invalid affix flag "A" with "long" flag value`)
							runLookup := func(executor fixture.Executor, query, column, typ string, token string, want any) {
								t.Helper()
								step := fixture.Select(query, []string{column}, []string{typ}, [][]any{{want}}, token)
								if token == "roots" && tc.lookupError {
									step = fixture.ErrorDiagnostic(query, diagnostic, token)
									step.SelectRows, step.ErrorStage = true, "execute"
								}
								fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
							}
							checkDictionary := func(name string, want dictionaryState) {
								t.Helper()
								query := "SELECT ts_lexize('" + name + "'::regdictionary,$1::text) AS lexemes"
								stmt, err := c.PrepareContext(p.Ctx, query)
								if err != nil {
									t.Fatal("runtime prepare demanded malformed flag", err)
								}
								defer stmt.Close()
								for _, executor := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}} {
									for range 2 {
										runLookup(executor, query, "lexemes", "_TEXT", "root", "{root}")
										runLookup(executor, query, "lexemes", "_TEXT", "unknown", nil)
										runLookup(executor, query, "lexemes", "_TEXT", "roots", "{root}")
										if after := readState(name); after != want {
											t.Fatalf("lookup changed accepted state: got=%+v want=%+v", after, want)
										}
										runLookup(executor, query, "lexemes", "_TEXT", "root", "{root}")
									}
								}
							}
							checkDictionary("ispell_duplicate_new", created)
							utility("ALTER TEXT SEARCH DICTIONARY ispell_duplicate_kept (" + options + ")")
							current := before
							current.options = wantOptions
							if after := readState("ispell_duplicate_kept"); after != current {
								t.Fatalf("successful ALTER state: got=%+v want=%+v", after, current)
							}
							checkDictionary("ispell_duplicate_kept", current)
							for _, executor := range []fixture.Executor{c, held(runtimeSQL)} {
								runLookup(executor, runtimeSQL, "lexemes", "_TEXT", "roots", "{root}")
								runLookup(executor, runtimeSQL, "lexemes", "_TEXT", "root", "{root}")
							}
							for _, executor := range []fixture.Executor{c, held(vectorSQL)} {
								runLookup(executor, vectorSQL, "normalized_vector", "TEXT", "roots", "'root':1")
								runLookup(executor, vectorSQL, "normalized_vector", "TEXT", "root", "'root':1")
								runLookup(executor, vectorSQL, "normalized_vector", "TEXT", "unknown", "")
							}
							// Accepted constants and detached Rows retain the incumbent
							// typed result even when current suffix demand now errors.
							dictionaryResult(t, p, b, held(constantSQL), constantSQL, "{root}")
							if tc.lookupError {
								run(fixture.Exec("BEGIN", 0))
								run(fixture.Exec("SAVEPOINT ispell_duplicate_lookup", 0))
								runLookup(held(runtimeSQL), runtimeSQL, "lexemes", "_TEXT", "roots", nil)
								run(fixture.Exec("ROLLBACK TO ispell_duplicate_lookup", 0))
								runLookup(held(runtimeSQL), runtimeSQL, "lexemes", "_TEXT", "root", "{root}")
								run(fixture.Exec("ROLLBACK", 0))
							} else {
								dictionaryResult(t, p, b, c, constantSQL, "{root}")
								run(fixture.Prepared(fixture.Select(constantSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{root}"}})))
							}
							if after := readState("ispell_duplicate_kept"); after != current {
								t.Fatalf("runtime demand reverted accepted ALTER: got=%+v want=%+v", after, current)
							}
							dictionaryResult(t, p, b, held(constantSQL), constantSQL, "{root}")
							if tc.lookupError {
								// Compound setup demands the ORIGINAL odd vector,
								// before any duplicate merge can mask its error.
								// Equal identities make the exact A diagnostic
								// independent of equal-word sorting.
								var dependencies int64
								if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=3600").Scan(&dependencies); err != nil {
									t.Fatal(err)
								}
								for _, query := range []string{
									"CREATE TEXT SEARCH DICTIONARY ispell_duplicate_rejected (TEMPLATE=pg_catalog.ispell,DictFile=tr_ispell_duplicate_long_equal,AffFile=tr_ispell_long_demand)",
									"ALTER TEXT SEARCH DICTIONARY ispell_duplicate_kept (DictFile=tr_ispell_duplicate_long_equal,AffFile=tr_ispell_long_demand)",
								} {
									step := fixture.ErrorDiagnostic(query, diagnostic)
									step.Prepared, step.ErrorStage = prepared, "execute"
									run(step)
									regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='ispell_duplicate_rejected' AND dictnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
									regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600", []string{"n"}, []string{"INT8"}, [][]any{{dependencies}})
									if after := readState("ispell_duplicate_kept"); after != current {
										t.Fatalf("rejected compound init changed accepted state: got=%+v want=%+v", after, current)
									}
									runLookup(held(runtimeSQL), runtimeSQL, "lexemes", "_TEXT", "roots", nil)
									runLookup(held(runtimeSQL), runtimeSQL, "lexemes", "_TEXT", "root", "{root}")
									dictionaryResult(t, p, b, held(constantSQL), constantSQL, "{root}")
								}
							}
							checkRetainedMetadata()
							if !old.Next() {
								t.Fatal("retained row missing", old.Err())
							}
							var value any
							if err := old.Scan(&value); err != nil || fixture.Normalize(value) != "{root}" {
								t.Fatal("retained value changed", value, err)
							}
							if old.Next() || old.Err() != nil {
								t.Fatal("retained result changed", old.Err())
							}
							if tc.lookupError {
								// Successful recovery publishes distinct raw vectors
								// again, on the same dictionary and held consumers.
								utility("ALTER TEXT SEARCH DICTIONARY ispell_duplicate_kept (DictFile=tr_ispell_duplicate_long,AffFile=tr_ispell_duplicate_long)")
								current.options = "dictfile = 'tr_ispell_duplicate_long', afffile = 'tr_ispell_duplicate_long'"
								if after := readState("ispell_duplicate_kept"); after != current {
									t.Fatalf("recovery ALTER state: got=%+v want=%+v", after, current)
								}
								for _, executor := range []fixture.Executor{c, held(runtimeSQL)} {
									dictionaryResult(t, p, b, executor, runtimeSQL, "{root}", "roots")
								}
								for _, executor := range []fixture.Executor{c, held(vectorSQL)} {
									fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(vectorSQL, []string{"normalized_vector"}, []string{"TEXT"}, [][]any{{"'root':1"}}, "roots"))
								}
								dictionaryResult(t, p, b, held(constantSQL), constantSQL, "{root}")
								dictionaryResult(t, p, b, c, constantSQL, "{root}")
								run(fixture.Prepared(fixture.Select(constantSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{root}"}})))
							}
						})
					}
				})
			}
		})
	}
}
