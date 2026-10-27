package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeSyntaxOptions(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		run(fixture.Exec(`CREATE TABLE "Analyze Quoted"(a INT,b INT)`, 0))
		run(fixture.Exec(`INSERT INTO "Analyze Quoted" VALUES(1,2),(1,3)`, 2))
		for _, q := range []string{
			`ANALYZE "Analyze Quoted"`, `ANALYSE public."Analyze Quoted"(a,b)`,
			`ANALYZE ONLY "Analyze Quoted"(a)`, `ANALYZE "Analyze Quoted"*(b)`,
			`ANALYZE (VERBOSE false,SKIP_LOCKED true) "Analyze Quoted"`,
			`ANALYZE (VERBOSE OFF,SKIP_LOCKED 0) "Analyze Quoted"`,
			`ANALYZE (VERBOSE 0,SKIP_LOCKED ON) "Analyze Quoted"`,
			`ANALYZE (BUFFER_USAGE_LIMIT '0') "Analyze Quoted"`,
			`ANALYZE (BUFFER_USAGE_LIMIT '128 kB') "Analyze Quoted"`,
			`ANALYZE "Analyze Quoted"(a),public."Analyze Quoted"(b)`,
			`ANALYZE (SKIP_LOCKED)`,
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec(q, 0)
				s.Prepared = prepared
				run(s)
				if len(notices) != 0 {
					t.Fatalf("quiet %s emitted %#v", q, notices)
				}
			}
		}
		analyzeTuples(t, p, b, c, `public."Analyze Quoted"`, 2)
		// Preparing is silent and cannot refresh stale facts.
		run(fixture.Exec(`INSERT INTO "Analyze Quoted" VALUES(4,5)`, 1))
		h := analyzeHeld(t, p, c, `ANALYZE "Analyze Quoted"`)
		if len(notices) != 0 {
			t.Fatal(notices)
		}
		analyzeTuples(t, p, b, c, `public."Analyze Quoted"`, 2)
		if _, err := h.Statement.ExecContext(p.Ctx, int64(1)); err == nil || err.Error() != "sql: expected 0 arguments, got 1" {
			t.Fatalf("extra utility argument=%v", err)
		}
		analyzeTuples(t, p, b, c, `public."Analyze Quoted"`, 2)
		run(fixture.Exec(h.SQL, 0))
		analyzeTuples(t, p, b, c, `public."Analyze Quoted"`, 3)
		// Utility Query exposes no schema or rows, independently of Exec count.
		for _, executor := range []fixture.Executor{c, h} {
			rows, err := executor.QueryContext(p.Ctx, h.SQL)
			if err != nil {
				t.Fatal(err)
			}
			cols, err := rows.Columns()
			if err != nil || len(cols) != 0 || rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatalf("ANALYZE rows/schema: %v %v %v", cols, err, rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceAnalyzeVerbose(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("CREATE TABLE analyze_verbose(a INT)", 0))
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("INSERT INTO analyze_verbose VALUES(1)", 1))
		for _, q := range []string{"ANALYZE VERBOSE analyze_verbose", "ANALYZE (VERBOSE) analyze_verbose", "ANALYZE (VERBOSE false,VERBOSE true) analyze_verbose"} {
			h := analyzeHeld(t, p, c, q)
			if len(notices) != 0 {
				t.Fatalf("Prepare emitted %#v", notices)
			}
			fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
			want := fixture.Diagnostic{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: `analyzing "public.analyze_verbose"`}
			if len(notices) == 0 || !reflect.DeepEqual(notices[0], want) {
				t.Fatalf("notices=%#v, want first %#v", notices, want)
			}
			if !b.PG && len(notices) != 1 {
				t.Fatalf("native invented physical progress: %#v", notices)
			}
			notices = nil
		}
	})
}

func TestReferenceAnalyzeDiagnostics(t *testing.T) {
	cases := []struct{ q, code, message, position string }{
		{"ANALYZE analyze_absent", "42P01", `relation "analyze_absent" does not exist`, ""},
		{"ANALYZE analyze_diag(nope)", "42703", `column "nope" of relation "analyze_diag" does not exist`, ""},
		{"ANALYZE analyze_diag(ctid)", "42703", `column "ctid" of relation "analyze_diag" does not exist`, ""},
		{"ANALYZE analyze_diag(a,a)", "42701", `column "a" of relation "analyze_diag" appears more than once`, ""},
		{"ANALYZE (WHAT true) analyze_diag", "42601", `unrecognized ANALYZE option "what"`, "10"},
		{"ANALYZE (VERBOSE 'nonsense') analyze_diag", "42601", "verbose requires a Boolean value", ""},
		{"ANALYZE (SKIP_LOCKED 'bad') analyze_diag", "42601", "skip_locked requires a Boolean value", ""},
		{"ANALYZE (BUFFER_USAGE_LIMIT '-1') analyze_diag", "22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB", ""},
	}
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		analyzeRun(t, p, b, b.DB, fixture.Exec("CREATE TABLE analyze_diag(a INT)", 0))
		analyzeRun(t, p, b, b.DB, fixture.Exec("INSERT INTO analyze_diag VALUES(1),(1),(1),(1),(1),(1),(1),(1),(1),(1)", 10))
		analyzeRun(t, p, b, b.DB, fixture.Exec("ANALYZE analyze_diag", 0))
		analyzeRun(t, p, b, b.DB, fixture.Exec("UPDATE analyze_diag SET a=2", 10))
		for _, tc := range cases {
			t.Run(tc.q, func(t *testing.T) {
				want := analyzeError(tc.code, tc.message)
				want.Position = tc.position
				analyzeRun(t, p, b, b.DB, fixture.ErrorDiagnostic(tc.q, want))
				// Utility Prepare succeeds; semantic validation belongs to Exec.
				h := analyzeHeld(t, p, b.DB, tc.q)
				analyzeRun(t, p, b, h, fixture.ErrorDiagnostic(tc.q, want))
				analyzeTuples(t, p, b, b.DB, "analyze_diag", 10)
				analyzeWantEstimate(t, p, b.DB, "analyze_diag", "a=1", 10)
			})
		}
		analyzeRun(t, p, b, b.DB, fixture.Exec("CREATE VIEW analyze_view AS SELECT a FROM analyze_diag", 0))
		analyzeRun(t, p, b, b.DB, fixture.Exec("CREATE SEQUENCE analyze_sequence", 0))
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, name := range []string{"analyze_view", "analyze_sequence"} {
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("ANALYZE "+name, 0))
			want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `skipping "` + name + `" --- cannot analyze non-tables or special system tables`}
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
				t.Fatalf("%s notices=%#v", name, notices)
			}
			notices = nil
		}
	})
}
