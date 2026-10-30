package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 NIImportAffixes passes the entire compoundwords operand to
// addCompoundAffixFlagValue. Its full key ZZ does not match dictionary flag Z;
// the ordinary Z control must still permit compounding. These expectations are
// independently specified, not inferred from native output.
func TestReferenceIspellLegacyCompoundOperand(t *testing.T) {
	for _, initial := range []string{"single", "full"} {
		t.Run(initial, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct_ddl"
				if prepared {
					name = "prepared_ddl"
				}
				t.Run(name, func(t *testing.T) {
					ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := func(step fixture.Step) {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						}
						utility := func(query string) {
							step := fixture.Exec(query, 0)
							step.Prepared = prepared
							run(step)
						}
						affix := func(form string) string { return "tr_ispell_legacy_compound_" + form }
						utility("CREATE TEXT SEARCH DICTIONARY ispell_legacy_operand (TEMPLATE=pg_catalog.ispell,DictFile=tr_ispell_legacy_compound,AffFile=" + affix(initial) + ")")
						run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION ispell_legacy_operand_config (COPY=pg_catalog.simple)", 0))
						run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION ispell_legacy_operand_config ALTER MAPPING FOR asciiword,word WITH ispell_legacy_operand", 0))
						var oid int64
						if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_dict WHERE dictname='ispell_legacy_operand' AND dictnamespace='public'::regnamespace").Scan(&oid); err != nil || oid <= 0 {
							t.Fatal("created dictionary identity", oid, err)
						}
						checkCatalog := func(form string) {
							t.Helper()
							run(fixture.Select("SELECT oid::bigint AS id,dictinitoption FROM pg_ts_dict WHERE dictname='ispell_legacy_operand' AND dictnamespace='public'::regnamespace", []string{"id", "dictinitoption"}, []string{"INT8", "TEXT"}, [][]any{{oid, "dictfile = 'tr_ispell_legacy_compound', afffile = '" + affix(form) + "'"}}))
						}
						const lexicalSQL = "SELECT ts_lexize('ispell_legacy_operand'::regdictionary,$1::text) AS lexemes"
						const vectorSQL = "SELECT to_tsvector('ispell_legacy_operand_config'::regconfig,$1::text)::text AS normalized_vector"
						const constantSQL = "SELECT ts_lexize('ispell_legacy_operand'::regdictionary,'rootroot'::text) AS lexemes"
						statements := make(map[string]*sql.Stmt)
						for _, query := range []string{lexicalSQL, vectorSQL, constantSQL} {
							statement, err := c.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer statement.Close()
							statements[query] = statement
						}
						held := func(query string) fixture.HeldStatement {
							return fixture.HeldStatement{Executor: c, Statement: statements[query], SQL: query}
						}
						compound := func(form string) any {
							if form == "single" {
								return "{root,root}"
							}
							return nil
						}
						checkCurrent := func(form string) {
							t.Helper()
							checkCatalog(form)
							for _, executor := range []fixture.Executor{c, held(lexicalSQL)} {
								dictionaryResult(t, p, b, executor, lexicalSQL, "{root}", "root")
								dictionaryResult(t, p, b, executor, lexicalSQL, nil, "unknown")
								dictionaryResult(t, p, b, executor, lexicalSQL, compound(form), "rootroot")
							}
							for _, token := range []string{"root", "rootroot", "unknown"} {
								vector := ""
								if token == "root" || token == "rootroot" && form == "single" {
									vector = "'root':1"
								}
								step := fixture.Select(vectorSQL, []string{"normalized_vector"}, []string{"TEXT"}, [][]any{{vector}}, token)
								run(step)
								fixture.RunStep(t, p.Ctx, held(vectorSQL), b.Recorder, step)
							}
						}
						checkCurrent(initial)
						dictionaryResult(t, p, b, held(constantSQL), constantSQL, compound(initial))

						// Retain unread rows on a separate borrower: lib/pq cannot
						// execute ALTER on the connection with an open result stream.
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
								t.Fatal("retained column names", columns, err)
							}
							types, err := old.ColumnTypes()
							if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "_TEXT" {
								t.Fatal("retained TEXT[] metadata", types, err)
							}
						}
						checkRetainedMetadata()
						replacement := "full"
						if initial == "full" {
							replacement = "single"
						}
						utility("ALTER TEXT SEARCH DICTIONARY ispell_legacy_operand (AffFile=" + affix(replacement) + ")")
						checkCurrent(replacement)
						// Warmed immutable values retain their original result, while
						// direct and fresh prepared constants see the accepted ALTER.
						dictionaryResult(t, p, b, held(constantSQL), constantSQL, compound(initial))
						dictionaryResult(t, p, b, c, constantSQL, compound(replacement))
						run(fixture.Prepared(fixture.Select(constantSQL, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{compound(replacement)}})))
						checkRetainedMetadata()
						if !old.Next() {
							t.Fatal("retained result missing", old.Err())
						}
						var value any
						if err := old.Scan(&value); err != nil {
							t.Fatal(err)
						}
						if got := fixture.Normalize(value); got != compound(initial) {
							t.Fatalf("retained result changed: got=%v want=%v", got, compound(initial))
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
