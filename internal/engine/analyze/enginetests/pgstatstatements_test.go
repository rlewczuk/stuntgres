package enginetests

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func pgssAnalyzedBinding(t *testing.T, e *core.Engine, source string) (parser.Statement, *analyze.Binding) {
	t.Helper()
	return pgssBindStatement(t, e, source, enginetest.Statement(t, source))
}

func pgssBindStatement(t *testing.T, e *core.Engine, source string, ast parser.Statement) (parser.Statement, *analyze.Binding) {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	op.OriginalSQL, op.SQLAvailable = source, true
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	// Admit the real description-only snapshot/lifetime before observing the
	// typed binding; Gate alone does not initialize DML analysis ownership.
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var binding analyze.Binding
	if err == nil {
		binding, err = core.Describe(op.Context(), e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	e.Mu.Unlock()
	if err = scope.Finish(err); err != nil {
		t.Fatal(source, err)
	}
	return ast, &binding
}

func pgssAnalyzedJumble(t *testing.T, e *core.Engine, source string) analyze.StatementJumble {
	t.Helper()
	ast, binding := pgssAnalyzedBinding(t, e, source)
	return analyze.JumbleStatement(ast, binding)
}

func TestPgStatStatementsFingerprint(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE jumble_a(k integer,v text)", "CREATE TABLE jumble_b(k bigint,v text)", "CREATE TABLE jumble_dest(k integer,v text)"} {
		enginetest.Run(t, e, q)
	}
	for _, pair := range []struct {
		left, right string
		same        bool
	}{
		{"SELECT 1 AS first_name", "SELECT 2 AS other_name", true},
		{"SELECT NULL::text", "SELECT 'value'::text", true},
		{"SELECT 1::integer", "SELECT 1::bigint", false},
		{"SELECT $1::integer,$2::integer", "SELECT $2::integer,$1::integer", false},
		{"SELECT $1::integer", "SELECT $1::bigint", false},
		{"SELECT a.k FROM jumble_a a", "SELECT a.k FROM jumble_b a", true},
		{"SELECT a.k FROM jumble_a a", "SELECT b.k FROM jumble_a b", false},
		{"SELECT k FROM jumble_a", "SELECT k FROM jumble_b", false},
		{"SELECT a.k FROM jumble_a a", "SELECT a.k::bigint FROM jumble_a a", false},
		{"SELECT k+1 FROM jumble_a", "SELECT k+2 FROM jumble_a", true},
		{"SELECT k+1 FROM jumble_a", "SELECT k-1 FROM jumble_a", false},
		{"SELECT abs(k) FROM jumble_a", "SELECT sign(k) FROM jumble_a", false},
		{"SELECT k FROM jumble_a WHERE k IN (1,2)", "SELECT k FROM jumble_a WHERE k IN (3,4,5)", true},
		{"SELECT ARRAY[1,2]", "SELECT ARRAY[3,4,5]", true},
		{"SELECT k FROM jumble_a ORDER BY k ASC", "SELECT k FROM jumble_a ORDER BY k DESC", false},
		{"SELECT count(k) FROM jumble_a", "SELECT sum(k) FROM jumble_a", false},
		{"SELECT row_number() OVER(ORDER BY k) FROM jumble_a", "SELECT rank() OVER(ORDER BY k) FROM jumble_a", false},
		{"SELECT sum(k) OVER(ORDER BY k ROWS UNBOUNDED PRECEDING) FROM jumble_a", "SELECT sum(k) OVER(ORDER BY k RANGE UNBOUNDED PRECEDING) FROM jumble_a", false},
		{"SELECT k FROM jumble_a LIMIT 1", "SELECT k FROM jumble_a LIMIT 7", true},
		{"VALUES(1),(2)", "VALUES(3),(4)", true},
		{"VALUES(1),(2)", "VALUES(3,4)", false},
		{"WITH x AS(SELECT 1 AS k) SELECT k FROM x", "WITH x AS(SELECT 2 AS k) SELECT k FROM x", true},
		{"SELECT a.k FROM jumble_a a JOIN jumble_dest b ON a.k=b.k", "SELECT a.k FROM jumble_a a LEFT JOIN jumble_dest b ON a.k=b.k", false},
		{"INSERT INTO jumble_a(k) SELECT 1", "INSERT INTO jumble_dest(k) SELECT 1", false},
		{"INSERT INTO jumble_a(k) VALUES(1)", "INSERT INTO jumble_a(k) VALUES(2)", true},
		{"UPDATE jumble_a SET k=1 WHERE k=2", "UPDATE jumble_a SET k=3 WHERE k=4", true},
		{"DELETE FROM jumble_a WHERE k=1", "DELETE FROM jumble_a WHERE k=2 RETURNING k", false},
	} {
		t.Run(pair.left+" / "+pair.right, func(t *testing.T) {
			left, right := pgssAnalyzedJumble(t, e, pair.left), pgssAnalyzedJumble(t, e, pair.right)
			if (left.QueryID() == right.QueryID()) != pair.same {
				t.Fatal("released selected-field relationship", left.QueryID(), right.QueryID(), pair.same)
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtility(t *testing.T) {
	identity := func(source string) int64 {
		statement := enginetest.Statement(t, source)
		jumble, _ := analyze.JumbleUtility(statement)
		return jumble.QueryID()
	}
	for _, pair := range []struct {
		left, right string
		same        bool
	}{
		{"SET application_name='one'", "SET application_name='two'", true},
		{"SET application_name='one'", "SET LOCAL application_name='one'", false},
		{"SET application_name='one'", "SET application_name=DEFAULT", false},
		{"SET timezone='UTC'", "SET timezone='Europe/Paris'", true},
		{"SET TIME ZONE 'UTC'", "SET TIME ZONE 'Europe/Paris'", false},
		{"SET TIME ZONE DEFAULT", "SET timezone=DEFAULT", false},
		{"SET XML OPTION DOCUMENT", "SET XML OPTION CONTENT", false},
		{"DEALLOCATE first_name", "DEALLOCATE other_name", true},
		{"DEALLOCATE first_name", "DEALLOCATE ALL", false},
		{"CREATE TABLE one_name(k integer)", "CREATE TABLE other_name(k integer)", false},
		{"FETCH 1 FROM one_cursor", "FETCH 2 FROM one_cursor", false},
	} {
		if (identity(pair.left) == identity(pair.right)) != pair.same {
			t.Fatal("released utility selection", pair)
		}
	}
	// The parser, not a GUC-name heuristic, distinguishes keyword-derived SET.
	generic := enginetest.Statement(t, "SET xmloption='document'").(*parser.Set)
	keyword := enginetest.Statement(t, "SET XML OPTION DOCUMENT").(*parser.Set)
	if generic.JumbleArgs || !keyword.JumbleArgs {
		t.Fatal("lost grammar-selected argument policy")
	}
}
