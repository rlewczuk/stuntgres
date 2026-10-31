package binder_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
)

func TestQueryOuterJoinsFullLegality(t *testing.T) {
	for _, tc := range []struct {
		condition string
		legal     bool
	}{
		{"b.k=a.k", true}, {"a.k+1=b.k+1", true}, {"CAST(a.k AS bigint)=b.k", true},
		{"a.k=a.k", false}, {"a.k=1", false}, {"a.k+b.k=b.k", false}, {"a.k<b.k", false},
		{"NOT NOT (a.k=b.k)", true}, {"NOT (NOT (a.k=b.k) OR FALSE)", true},
		{"(a.k=b.k AND a.q=9) OR (a.k=b.k AND b.q=8)", true},
		{"a.k=b.k OR a.q=9", false}, {"(a.k<b.k) AND FALSE", true},
		{"(a.k<b.k) OR (1+1=2)", true}, {"NULL::boolean AND a.k<b.k", false},
		{"CASE WHEN 1+1=2 THEN a.k=b.k ELSE a.k<b.k END", true},
		{"(a.k<b.k) AND (2=3)", true}, {"NOT (TRUE OR a.k<b.k)", true},
		{"CASE WHEN 2=3 THEN a.k<b.k ELSE FALSE END", true},
		{"COALESCE(NULL::boolean,TRUE)", true}, {"COALESCE(a.k=b.k,FALSE)", false},
		{"COALESCE(NULL::boolean,a.k=b.k)", false},
		{"COALESCE(NULL::boolean,CASE WHEN TRUE THEN a.k=b.k ELSE FALSE END)", false},
	} {
		t.Run(tc.condition, func(t *testing.T) {
			e, query, _ := queryJoinsFixture(t, "SELECT a.k FROM a FULL JOIN b ON "+tc.condition)
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := view.PreflightQuery(&evaluation, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			err = view.ValidateQueryJoinExecution(&evaluation, root, e.CatalogRead(), 0)
			if tc.legal && err != nil || !tc.legal && err == nil {
				t.Fatal(tc, err)
			}
			if !tc.legal {
				enginetest.State(t, err, "0A000")
			}
		})
	}
}

func TestQueryOuterJoinsNestedMergedKeyDependencies(t *testing.T) {
	for _, queryText := range []string{
		"SELECT j.k FROM (a FULL JOIN wide USING(k)) j FULL JOIN b ON j.k=b.k",
		"SELECT j.k FROM b FULL JOIN (a FULL JOIN wide USING(k)) j ON b.k=j.k",
		"SELECT a.k FROM a FULL JOIN b ON a.l||current_user=b.r",
		"SELECT a.k FROM a FULL JOIN b ON CASE WHEN a.q=9 THEN a.k ELSE a.k+1 END=b.k",
	} {
		e, query, _ := queryJoinsFixture(t, queryText)
		c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		root, err := view.PreflightQuery(&c, query.Root)
		if err != nil {
			t.Fatal(queryText, err)
		}
		if err := view.ValidateQueryJoinExecution(&c, root, e.CatalogRead(), 0); err != nil {
			t.Fatal(queryText, err)
		}
	}
}

func TestQueryOuterJoinsExecutionRowmarkModes(t *testing.T) {
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
			e, query, _ := queryJoinsFixture(t, "SELECT a.k FROM a "+kind+" JOIN b ON a.k=b.k LIMIT 0 "+clause)
			c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			root, err := view.PreflightQuery(&c, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			err = view.ValidateQueryJoinExecution(&c, root, e.CatalogRead(), 0)
			enginetest.State(t, err, "0A000")
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: clause + " cannot be applied to the nullable side of an outer join"}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(err)
			}
		}
	}
}

func queryJoinsFixture(t *testing.T, q string) (*core.Engine, *plan.Query, *exec.Source) {
	t.Helper()
	e, query, source, _ := queryJoinsAdmittedFixture(t, q, false)
	return e, query, source
}

// Explicit admission allows immutable captured rows to outlive catalog replacement
// without bypassing the replacement reservation held by implicit statements.
func queryJoinsAdmittedFixture(t *testing.T, q string, explicit bool) (*core.Engine, *plan.Query, *exec.Source, func()) {
	t.Helper()
	ast := enginetest.Statement(t, q)
	e := engine.New()
	for _, sql := range []string{"CREATE TABLE a(l TEXT,k INT,q INT)", "CREATE TABLE b(r TEXT,q INT,k INT)", "CREATE TABLE c(k INT,c TEXT)", "CREATE TABLE wide(k BIGINT)", "CREATE TABLE chars(k VARCHAR(3))", "CREATE TABLE longer(k VARCHAR(5))", "CREATE TABLE same(k VARCHAR(3))", "CREATE TABLE unused(v INT)", "INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", "INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", "INSERT INTO c VALUES(1,'c'),(1,'c')", "INSERT INTO wide VALUES(1)", "INSERT INTO chars VALUES('x')", "INSERT INTO longer VALUES('x')", "INSERT INTO same VALUES('x')"} {
		enginetest.Run(t, e, sql)
	}
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var scope *core.StatementScope
	var source *exec.Source
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if source != nil {
			source.Close()
		}
		if scope != nil && !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
		done()
	}
	t.Cleanup(release)
	if explicit {
		if _, err = e.Begin(op, session.TxOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var b analyze.Binding
	if err == nil {
		b, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	if err == nil {
		// Standalone stages supply contexts, but still need live admission authority.
		source, err = e.CaptureQuerySourcesForInvokerLocked(scope, b.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{}, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if err == nil {
		err = exec.SelectQuerySources(op.Ctx, source)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	return e, b.Query, source, release
}
