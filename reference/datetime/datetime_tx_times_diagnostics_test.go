package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const txTimesHint = "No function matches the given name and argument types. You might need to add explicit type casts."

type txTimesFailure struct {
	query, code, message, hint string
	position                   int
}

func txTimesFailures(t *testing.T, cases []txTimesFailure) {
	t.Helper()
	txTimesScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, r *fixture.Recorder) {
		for _, item := range cases {
			for _, route := range []string{"exec", "query", "prepare"} {
				t.Run(route+"/"+item.query, func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic(item.code, item.message)
					want.Position = fixture.Position(item.position)
					want.Hint = item.hint
					step := fixture.ErrorDiagnostic(item.query, want)
					if route == "query" {
						step.SelectRows = true
					}
					if route == "prepare" {
						step = fixture.Prepared(step)
						step.ErrorStage = "prepare"
					} else {
						step.ErrorStage = "execute"
					}
					fixture.RunStep(t, p.Ctx, c, r, step)
					fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			}
		}
	})
}

func TestReferenceDatetimeTxTimesSignatures(t *testing.T) {
	var cases []txTimesFailure
	for _, name := range txTimesNames {
		for _, arg := range []struct{ sql, signature string }{{"1", "integer"}, {"'x'::TEXT", "text"}, {"NULL", "unknown"}, {"1,2", "integer, integer"}, {"x=>1", "x => integer"}, {"x:=1", "x => integer"}, {"VARIADIC 1", "integer"}} {
			cases = append(cases, txTimesFailure{"SELECT " + name + "(" + arg.sql + ")", "42883", "function " + name + "(" + arg.signature + ") does not exist", txTimesHint, 8})
		}
		cases = append(cases, txTimesFailure{"SELECT " + name + "(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", "", 8})
		cases = append(cases, txTimesFailure{"SELECT " + name + "(missing_tx_time_column," + strings.Repeat("1,", 99) + "1)", "42703", "column \"missing_tx_time_column\" does not exist", "", 8 + len(name) + 1})
		cases = append(cases, txTimesFailure{"SELECT pg_catalog." + name + "(1)", "42883", "function pg_catalog." + name + "(integer) does not exist", txTimesHint, 8})
		cases = append(cases, txTimesFailure{"SELECT public." + name + "()", "42883", "function public." + name + "() does not exist", txTimesHint, 8})
		cases = append(cases, txTimesFailure{"SELECT \"" + strings.ToUpper(name) + "\"()", "42883", "function " + strings.ToUpper(name) + "() does not exist", txTimesHint, 8})
	}
	txTimesFailures(t, cases)
	txTimesScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, r *fixture.Recorder) {
		for _, name := range txTimesNames {
			want := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"(unknown) does not exist")
			want.Position = "8"
			want.Hint = txTimesHint
			step := fixture.Prepared(fixture.ErrorDiagnostic("SELECT "+name+"($1)", want))
			step.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, r, step)
		}
	})
}

func TestReferenceDatetimeTxTimesNamedStructure(t *testing.T) {
	var cases []txTimesFailure
	for _, name := range txTimesNames {
		for _, notation := range []string{"=>", ":="} {
			prefix := "SELECT " + name + "(x" + notation + "1,"
			cases = append(cases, txTimesFailure{prefix + "2)", "42601", "positional argument cannot follow named argument", "", len(prefix) + 1})
			cases = append(cases, txTimesFailure{prefix + "x" + notation + "2)", "42601", "argument name \"x\" used more than once", "", len(prefix) + 1})
			childPrefix := "SELECT " + name + "(x" + notation
			cases = append(cases, txTimesFailure{childPrefix + "missing_tx_time_column,2)", "42703", "column \"missing_tx_time_column\" does not exist", "", len(childPrefix) + 1})
			childPrefix = prefix + "x" + notation
			cases = append(cases, txTimesFailure{childPrefix + "missing_tx_time_column)", "42703", "column \"missing_tx_time_column\" does not exist", "", len(childPrefix) + 1})
		}
		cases = append(cases, txTimesFailure{"SELECT /*é*/ " + name + "(x=>1,2)", "42601", "positional argument cannot follow named argument", "", 14 + len(name) + len("(x=>1,")})
		cases = append(cases, txTimesFailure{"SELECT /*é*/ " + name + "(x=>1,x=>2)", "42601", "argument name \"x\" used more than once", "", 14 + len(name) + len("(x=>1,")})
		filterPrefix := "SELECT " + name + "(x=>1,2) FILTER (WHERE "
		cases = append(cases, txTimesFailure{filterPrefix + "missing_tx_time_filter)", "42703", "column \"missing_tx_time_filter\" does not exist", "", len(filterPrefix) + 1})
		cases = append(cases, txTimesFailure{"SELECT " + name + "(x=>1," + strings.Repeat("1,", 99) + "1)", "54023", "cannot pass more than 100 arguments to a function", "", 8})
	}
	txTimesFailures(t, cases)
}

func TestReferenceDatetimeTxTimesDecorationStructure(t *testing.T) {
	var cases []txTimesFailure
	for _, name := range txTimesNames {
		base := "SELECT " + name + "()"
		for _, over := range []string{"OVER ()", "OVER tx_time_window"} {
			cases = append(cases, txTimesFailure{base + " FILTER (WHERE true) " + over, "42809", "FILTER specified, but " + name + " is not an aggregate function", "", 8})
			filterPrefix := base + " FILTER (WHERE "
			cases = append(cases, txTimesFailure{filterPrefix + "missing_tx_time_filter) " + over, "42703", "column \"missing_tx_time_filter\" does not exist", "", len(filterPrefix) + 1})
			prefix := base + " " + over + " "
			cases = append(cases, txTimesFailure{prefix + "FILTER (WHERE true)", "42601", "syntax error at or near \"FILTER\"", "", len(prefix) + 1})
			cases = append(cases, txTimesFailure{prefix + "OVER ()", "42601", "syntax error at or near \"OVER\"", "", len(prefix) + 1})
		}
		prefix := base + " FILTER (WHERE true) "
		cases = append(cases, txTimesFailure{prefix + "FILTER (WHERE true)", "42601", "syntax error at or near \"FILTER\"", "", len(prefix) + 1})
		prefix = base + " FILTER (WHERE missing_tx_time_filter) "
		cases = append(cases, txTimesFailure{prefix + "FILTER (WHERE true)", "42601", "syntax error at or near \"FILTER\"", "", len(prefix) + 1})
		prefix = base + " FILTER (WHERE true) OVER () "
		cases = append(cases, txTimesFailure{prefix + "FILTER (WHERE true)", "42601", "syntax error at or near \"FILTER\"", "", len(prefix) + 1})
		cases = append(cases, txTimesFailure{prefix + "OVER ()", "42601", "syntax error at or near \"OVER\"", "", len(prefix) + 1})
	}
	txTimesFailures(t, cases)
}

func TestReferenceDatetimeTxTimesDecorations(t *testing.T) {
	var cases []txTimesFailure
	for _, name := range txTimesNames {
		cases = append(cases, txTimesFailure{"SELECT " + name + "(*)", "42809", name + "(*) specified, but " + name + " is not an aggregate function", "", 8})
		for _, modifier := range []string{"DISTINCT", "ALL"} {
			cases = append(cases, txTimesFailure{"SELECT " + name + "(" + modifier + ")", "42601", "syntax error at or near \"" + ")" + "\"", "", len("SELECT "+name+"("+modifier) + 1})
			cases = append(cases, txTimesFailure{"SELECT " + name + "(" + modifier + " 1)", "42883", "function " + name + "(integer) does not exist", txTimesHint, 8})
		}
		cases = append(cases, txTimesFailure{"SELECT " + name + "(ORDER BY 1)", "42601", "syntax error at or near \"ORDER\"", "", 8 + len(name) + 1})
		cases = append(cases, txTimesFailure{"SELECT " + name + "(1 ORDER BY 1)", "42883", "function " + name + "(integer) does not exist", txTimesHint, 8})
		cases = append(cases, txTimesFailure{"SELECT " + name + "() FILTER (WHERE true)", "42809", "FILTER specified, but " + name + " is not an aggregate function", "", 8})
		cases = append(cases, txTimesFailure{"SELECT " + name + "() OVER ()", "42809", "OVER specified, but " + name + " is not a window function nor an aggregate function", "", 8})
		cases = append(cases, txTimesFailure{"SELECT " + name + "() OVER tx_time_window", "42809", "OVER specified, but " + name + " is not a window function nor an aggregate function", "", 8})
		cases = append(cases, txTimesFailure{"SELECT " + name + "(1) FILTER (WHERE true)", "42883", "function " + name + "(integer) does not exist", txTimesHint, 8})
		filterPosition := len("SELECT "+name+"() FILTER (WHERE ") + 1
		cases = append(cases, txTimesFailure{"SELECT " + name + "() FILTER (WHERE missing_tx_time_filter)", "42703", "column \"missing_tx_time_filter\" does not exist", "", filterPosition})
		cases = append(cases, txTimesFailure{"SELECT " + name + "() FILTER (WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "", filterPosition})
	}
	txTimesFailures(t, cases)
}

func TestReferenceDatetimeTxTimesDiagnostics(t *testing.T) {
	var cases []txTimesFailure
	for _, name := range txTimesNames {
		cases = append(cases, txTimesFailure{"SELECT /*é*/ " + name + "(1)", "42883", "function " + name + "(integer) does not exist", txTimesHint, 14})
		cases = append(cases, txTimesFailure{"SELECT " + name, "42703", "column \"" + name + "\" does not exist", "", 8})
		cases = append(cases, txTimesFailure{"SELECT " + name + "(", "42601", "syntax error at end of input", "", len("SELECT "+name+"(") + 1})
		cases = append(cases, txTimesFailure{"SELECT " + name + "(1,)", "42601", "syntax error at or near \")\"", "", len("SELECT "+name+"(1,") + 1})
		cases = append(cases, txTimesFailure{"SELECT " + name + "(missing_tx_time_column)", "42703", "column \"missing_tx_time_column\" does not exist", "", len("SELECT "+name+"(") + 1})
	}
	txTimesFailures(t, cases)
	txTimesScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, r *fixture.Recorder) {
		datetimeCurrentExec(t, p.Ctx, c, "BEGIN", 0)
		old := txTimesRead(t, p.Ctx, c, pg, "SELECT "+txTimesProjection, 1, 0, false)
		datetimeCurrentExec(t, p.Ctx, c, "SAVEPOINT tx_time_recovery", 0)
		want := fixture.ControlsReferenceDiagnostic("42883", "function now(integer) does not exist")
		want.Position = "8"
		want.Hint = txTimesHint
		fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("SELECT now(1)", want))
		fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("SELECT now()", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		datetimeCurrentExec(t, p.Ctx, c, "ROLLBACK TO tx_time_recovery", 0)
		same := txTimesRead(t, p.Ctx, c, pg, "SELECT "+txTimesProjection, 1, 0, false)
		if !same.transaction.Equal(old.transaction) {
			t.Error("recovery rebirthed transaction")
		}
	})
}

// FILTER is analyzed in its own clause before the decoration or signature is
// rejected, so nested aggregates, set-returning and window calls fail there.
func TestReferenceDatetimeTxTimesFilterContext(t *testing.T) {
	var cases []txTimesFailure
	for _, name := range txTimesNames {
		for _, args := range []string{"", "1"} {
			prefix := "SELECT " + name + "(" + args + ") FILTER (WHERE "
			at := len(prefix) + 1
			cases = append(cases,
				txTimesFailure{prefix + "count(*) > 0)", "42803", "aggregate functions are not allowed in FILTER", "", at},
				txTimesFailure{prefix + "generate_series(1,2) > 0)", "0A000", "set-returning functions are not allowed in FILTER", "", at},
				txTimesFailure{prefix + "row_number() OVER () > 0)", "42P20", "window functions are not allowed in FILTER", "", at},
			)
		}
	}
	txTimesFailures(t, cases)
}
