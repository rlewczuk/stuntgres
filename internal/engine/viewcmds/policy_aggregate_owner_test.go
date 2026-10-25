package viewcmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRLSPolicyAggregateSemanticOwner(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	for _, tc := range []struct {
		condition string
		code      string
		message   string
		hint      string
	}{
		{"sum(t.id)>0", "42803", "aggregate functions are not allowed in policy expressions", ""},
		{"(SELECT sum(t.id))>0", "42803", "aggregate functions are not allowed in policy expressions", ""},
		{"(SELECT (SELECT sum(t.id)) FROM helper)>0", "42803", "aggregate functions are not allowed in policy expressions", ""},
		{"FALSE AND (SELECT sum(t.id))>0", "42803", "aggregate functions are not allowed in policy expressions", ""},
		{"CASE WHEN TRUE THEN TRUE ELSE (SELECT (SELECT sum(t.id)))>0 END", "42803", "aggregate functions are not allowed in policy expressions", ""},
		{"sum(t.allowed)>0", "42883", "function sum(boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"(SELECT sum(t.allowed))>0", "42883", "function sum(boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"(SELECT sum(sum(helper.id)) FROM helper)>0", "42803", "aggregate function calls cannot be nested", ""},
		{"(SELECT sum(sum(helper.id)+t.id) FROM helper)>0", "42803", "aggregate function calls cannot be nested", ""},
		{"(SELECT sum(sum(t.id)))>0", "42803", "aggregate functions are not allowed in policy expressions", ""},
		{"(SELECT count(*) FROM helper)>0", "", "", ""},
		{"(SELECT sum(1))>0", "", "", ""},
		{"(SELECT sum(helper.id+t.id) FROM helper)>0", "", "", ""},
		{"(SELECT (SELECT sum(helper.id)) FROM helper)>0", "", "", ""},
		// Each child exit must restore POLICY for its following sibling.
		{"(SELECT sum(helper.id) FROM helper)>0 AND sum(t.id)>0", "42803", "aggregate functions are not allowed in policy expressions", ""},
		{"(SELECT sum(helper.id) FROM helper)>0 AND (SELECT sum(t.id))>0", "42803", "aggregate functions are not allowed in policy expressions", ""},
		{"(SELECT sum(helper.id) FROM helper)>0 AND (SELECT sum(helper.id+t.id) FROM helper)>0", "", "", ""},
	} {
		t.Run(tc.condition, func(t *testing.T) {
			expression, err := policyUnitBind(t, e, op, d, tc.condition)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				if err := stored.ValidatePolicy(e.CatalogRead(), d, expression); err != nil {
					t.Fatal(err)
				}
				return
			}
			diagnostic, ok := err.(*sqlstate.Error)
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint}
			if !ok || *diagnostic != want {
				t.Fatalf("diagnostic got %#v, want %#v", err, want)
			}
		})
	}
}

func TestRLSPolicyAggregateClauseRestoration(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	b := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.Target.Relation = d
	b.Clause.AggregateClause = "policy expressions"
	for range 2 {
		leaveChild := b.EnterQuery(false)
		if b.Clause.AggregateClause != "" || len(b.Query.Scopes) != 1 || b.Query.Scopes[0].Clause != "policy expressions" {
			t.Fatal("child did not capture policy owner", b.Clause.AggregateClause, b.Query.Scopes)
		}
		b.Clause.AggregateClause = "SELECT"
		leaveGrandchild := b.EnterQuery(false)
		if len(b.Query.Scopes) != 2 || b.Query.Scopes[0].Clause != "policy expressions" || b.Query.Scopes[1].Clause != "SELECT" {
			t.Fatal("deep query lost lexical owner clauses", b.Query.Scopes)
		}
		b.Clause.AggregateClause = "WHERE"
		leaveGrandchild()
		if b.Clause.AggregateClause != "SELECT" || len(b.Query.Scopes) != 1 {
			t.Fatal("grandchild failed to restore child clause", b.Clause.AggregateClause, b.Query.Scopes)
		}
		leaveChild()
		if b.Clause.AggregateClause != "policy expressions" || len(b.Query.Scopes) != 0 || b.Query.Frame != 0 {
			t.Fatal("child failed to restore policy clause", b.Clause.AggregateClause, b.Query.Scopes, b.Query.Frame)
		}
	}
}

// SQL only constructs the ordinary internal catalog fixture. Assertions exercise
// real binders, frozen presentation, provider validation and publication directly.
func policyUnitFixture(t *testing.T) (*core.Engine, *session.Operation, func(), *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(id INTEGER, allowed BOOLEAN)")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE helper(id INTEGER)")
	ref := enginetest.RoleTableRef(t, e, "t")
	d, _ := e.CatalogRead().Descriptor(ref.Address)
	return e, op, done, d.(*catalog.Relation)
}

func policyUnitBind(t *testing.T, e *core.Engine, op *session.Operation, d *catalog.Relation, condition string) (*stored.Policy, error) {
	t.Helper()
	raw := enginetest.Statement(t, "CREATE POLICY p ON t USING("+condition+")").(*parser.CreatePolicy)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	return engine.ViewHost{TableHost: tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op}}}.BindPolicyExpression(auth, d, raw.Using, sqltype.InputEnvironment{})
}
