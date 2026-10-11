package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSimilarDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE t(v TEXT,p TEXT,e TEXT)`, 0))
		for _, populated := range []bool{false, true} {
			if populated {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO t VALUES('a','[','ab'),(NULL,'[','ab'),('','[','ab')`, 3))
			}
			t.Run(fmt.Sprintf("%s/populated=%t", b.Name, populated), func(t *testing.T) {
				for _, query := range []string{
					`SELECT v SIMILAR TO '[' FROM t`, `SELECT v SIMILAR TO p ESCAPE '#' FROM t`,
					`SELECT v SIMILAR TO p FROM t`, `SELECT v SIMILAR TO '[' ESCAPE '#' FROM t`,
					`SELECT COALESCE(NULL::boolean,v SIMILAR TO '[') FROM t`,
				} {
					label := "?column?"
					if query == `SELECT COALESCE(NULL::boolean,v SIMILAR TO '[') FROM t` {
						label = "coalesce"
					}
					step := fixture.Select(query, []string{label}, []string{"BOOL"}, nil)
					if populated {
						step = similarReferenceRegexError(query, "brackets [] not balanced", "")
					}
					similarReferenceRoutes(t, p, b, step)
				}
				similarReferenceRoutes(t, p, b, similarReferenceError(`SELECT NULL SIMILAR TO 'x' ESCAPE 'ab' FROM t`, fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}))
				similarReferenceRoutes(t, p, b, similarReferenceError(`SELECT v SIMILAR TO $1 ESCAPE $2 FROM t`, fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}, "[", "ab"))
				for _, c := range []struct {
					query, label, typ string
					value             any
					filtered          bool
				}{
					{`SELECT NULL SIMILAR TO '[' FROM t`, "?column?", "BOOL", nil, false},
					{`SELECT NULL::text SIMILAR TO p ESCAPE e FROM t`, "?column?", "BOOL", nil, false},
					{`SELECT substring(NULL::text SIMILAR '[' ESCAPE '#') FROM t`, "substring", "TEXT", nil, false},
					{`SELECT v SIMILAR TO NULL ESCAPE 'ab' FROM t`, "?column?", "BOOL", nil, false},
					{`SELECT v SIMILAR TO p ESCAPE e FROM t WHERE FALSE`, "?column?", "BOOL", nil, true},
					{`SELECT CASE WHEN FALSE THEN v SIMILAR TO '[' ELSE TRUE END FROM t`, "case", "BOOL", true, false},
					{`SELECT COALESCE(TRUE,v SIMILAR TO '[') FROM t`, "coalesce", "BOOL", true, false},
					{`SELECT CASE WHEN FALSE THEN substring(v SIMILAR '[' ESCAPE '#') ELSE 'safe' END FROM t`, "case", "TEXT", "safe", false},
				} {
					var rows [][]any
					if populated && !c.filtered {
						rows = [][]any{{c.value}, {c.value}, {c.value}}
					}
					similarReferenceRoutes(t, p, b, fixture.Select(c.query, []string{c.label}, []string{c.typ}, rows))
				}
				var typedNil *string
				const query = `SELECT $1::text SIMILAR TO $2::text ESCAPE $3::text AS matched, substring($1::text SIMILAR $2::text ESCAPE $3::text) AS piece FROM t`
				for _, c := range []struct {
					args           []any
					matched, piece any
					reason         string
					escapeError    bool
				}{
					{[]any{"a", "a", "#"}, true, "a", "", false},
					{[]any{"a", "[", "#"}, nil, nil, "brackets [] not balanced", false},
					{[]any{nil, "[", "#"}, nil, nil, "", false},
					{[]any{typedNil, "x", "ab"}, nil, nil, "", true},
					{[]any{"a", nil, "ab"}, nil, nil, "", false},
					{[]any{"", "[", "#"}, nil, nil, "brackets [] not balanced", false},
					{[]any{"nomatch", "[", "#"}, nil, nil, "brackets [] not balanced", false},
				} {
					var rows [][]any
					if populated {
						rows = [][]any{{c.matched, c.piece}, {c.matched, c.piece}, {c.matched, c.piece}}
					}
					step := fixture.Select(query, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, rows, c.args...)
					if c.reason != "" {
						step = similarReferenceRegexError(query, c.reason, "", c.args...)
					}
					if c.escapeError {
						step = similarReferenceError(query, fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}, c.args...)
					}
					similarReferenceRoutes(t, p, b, step)
				}
			})
		}
	}
}

func TestReferenceSimilarHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	const query = `SELECT $1::text SIMILAR TO $2::text ESCAPE $3::text AS matched, substring($1::text SIMILAR $2::text ESCAPE $3::text) AS piece`
	for _, b := range p.Backends() {
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			var typedNil *string
			for i, s := range []fixture.Step{
				fixture.Select(query, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, "ab"}}, "abab", `#"ab#"#1`, "#"),
				similarReferenceError(query, fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}, "a", "a", "ab"),
				similarReferenceRegexError(query, "brackets [] not balanced", "", "a", "[", "#"),
				fixture.Select(query, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{nil, nil}}, typedNil, "%", "#"),
				fixture.Select(query, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, ""}}, "ab", `a#"#"b`, "#"),
			} {
				t.Run(fmt.Sprintf("%s/frame=%d", b.Name, i), func(t *testing.T) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) })
			}
		}()
	}
}
