package reference_test

import "testing"

// Context/recorded companions remain in the main value suites. These are the
// original database/sql consumers, using the same independently specified data.
func TestReferenceStringCoreValuesSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE string_core_seed(id INT)")
			stringCoreControlExec(t, db, "SET TimeZone='UTC'")
			stringCoreControlExec(t, db, "SET DateStyle='ISO, MDY'")
			for _, cases := range [][]stringCoreReferenceCase{stringCoreTrimCases(), stringCoreConcatCases(), stringCoreNormalizationCases(), stringCoreScalarNullCases()} {
				for _, tc := range cases {
					t.Run(tc.query, func(t *testing.T) {
						rows, err := db.Query(tc.query)
						stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, false)
						stmt, err := db.Prepare(tc.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						rows, err = stmt.Query()
						stmt.Close()
						stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, false)
					})
				}
			}
		})
	}
}

func TestReferenceStringCoreSyntaxAndLabelsSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			for _, tc := range []stringCoreReferenceCase{
				{"SELECT ltrim(NULL) AS v", nil, "TEXT"},
				{"SELECT rtrim('x',NULL) AS v", nil, "TEXT"},
				{"SELECT trim(leading from ' x ') AS v", "x ", "TEXT"},
				{"SELECT trim(trailing from ' x ') AS v", " x", "TEXT"},
				{"SELECT btrim('a\t  '::char(8),'a') AS v", "\t ", "TEXT"},
				{"SELECT \"normalize\"('x','NFC '::char(5)) AS v", "x", "TEXT"},
				{"SELECT normalize('é',nFc) AS v", "é", "TEXT"},
				{"SELECT 'ⓑ' IS NFKC NORMALIZED AS v", false, "BOOL"},
				{"SELECT 'b' IS NFKC NORMALIZED AS v", true, "BOOL"},
				{"SELECT 'é' IS NFKD NORMALIZED AS v", false, "BOOL"},
				{"SELECT 'é' IS NFKD NORMALIZED AS v", true, "BOOL"},
				{"SELECT is_normalized('é','nFd') AS v", true, "BOOL"},
				{"SELECT normalize('A̅́') AS v", "A̅́", "TEXT"},
				{"SELECT normalize('̀̕',NFD) AS v", "̀̕", "TEXT"},
				{"SELECT 'x'::text||setseed(NULL) AS v", nil, "TEXT"},
				{"SELECT setseed(NULL)||'x'::text AS v", nil, "TEXT"},
			} {
				rows, err := db.Query(tc.query)
				stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, false)
				stmt, err := db.Prepare(tc.query)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = stmt.Query()
				stmt.Close()
				stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, false)
			}
			for _, tc := range []struct {
				q            string
				names, types []string
				want         [][]any
			}{
				{"SELECT trim(' x '),trim(leading from ' x '),trim(trailing from ' x ')", []string{"btrim", "ltrim", "rtrim"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"x", "x ", " x"}}},
				{"SELECT normalize('é'),'é' IS NORMALIZED,'é' IS NOT NORMALIZED,'a'||'b'", []string{"normalize", "is_normalized", "?column?", "?column?"}, []string{"TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{"é", true, true, "ab"}}},
			} {
				rows, err := db.Query(tc.q)
				stringCoreRetainedRows(t, rows, err, tc.names, tc.types, tc.want, false)
				s, err := db.Prepare(tc.q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = s.Query()
				stringCoreRetainedRows(t, rows, err, tc.names, tc.types, tc.want, false)
				s.Close()
			}
		})
	}
}

func TestReferenceStringCoreInternalCharSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE string_core_seed(id INT)")
			for _, v := range []any{"A", `\377`, `\200`, `\000`, nil} {
				for _, tc := range []struct{ q, message, position string }{
					{"SELECT '['::text||CASE WHEN false THEN relkind ELSE $1 END AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", `operator is not unique: text || "char"`, "17"},
					{"SELECT CASE WHEN false THEN relkind ELSE $1 END||']'::text AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", `operator is not unique: "char" || text`, "48"},
				} {
					t.Run(tc.q, func(t *testing.T) {
						want := referenceDiagnostic{Code: "42725", Message: tc.message, Position: tc.position, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}
						_, err := db.Exec(tc.q, v)
						stringCoreControlDiagnostic(t, err, want)
						s, err := db.Prepare(tc.q)
						if s != nil {
							s.Close()
							t.Fatal("ambiguous operator exposed statement")
						}
						stringCoreControlDiagnostic(t, err, want)
					})
				}
				// CHAR/nonstring has a unique string overload, unlike CHAR/TEXT.
				var suffix, forward, reverse any
				if v != nil {
					suffix = v
					if v == `\000` {
						suffix = ""
					}
					forward = suffix.(string) + "42"
					reverse = "42" + suffix.(string)
				}
				for _, tc := range []stringCoreReferenceCase{
					{"SELECT CASE WHEN false THEN relkind ELSE $1 END||42 AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", forward, "TEXT"},
					{"SELECT 42||CASE WHEN false THEN relkind ELSE $1 END AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", reverse, "TEXT"},
				} {
					rows, err := db.Query(tc.query, v)
					stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, false)
					stmt, err := db.Prepare(tc.query)
					if err != nil {
						t.Fatal(err)
					}
					rows, err = stmt.Query(v)
					stmt.Close()
					stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}, false)
				}
			}
		})
	}
}
