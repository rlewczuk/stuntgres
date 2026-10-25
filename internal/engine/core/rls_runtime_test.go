package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type rlsCountedExpression struct {
	expr.Expr
	calls   *int
	failure error
}

func (n rlsCountedExpression) Eval(c expr.Context) (sqltype.Value, error) {
	*n.calls++
	if n.failure != nil {
		return sqltype.Value{}, n.failure
	}
	return n.Expr.Eval(c)
}

func TestRLSComposition(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	bound := func(sql string) expr.Expr {
		p, err := policyUnitBind(t, e, op, d, sql)
		if err != nil {
			t.Fatal(err)
		}
		return p.Expression()
	}
	yes, no, unknown := bound("TRUE"), bound("FALSE"), bound("NULL")
	clause := func(n expr.Expr) exec.RLSRuntimeClause { return exec.RLSRuntimeClause{Expression: n} }
	parent := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for _, tc := range []struct {
		name  string
		p     []expr.Expr
		r     expr.Expr
		pass  bool
		named string
	}{
		{"true or null", []expr.Expr{yes, unknown}, yes, true, ""},
		{"false or null", []expr.Expr{no, unknown}, yes, false, ""},
		{"restrictive null", []expr.Expr{yes}, unknown, false, "restriction"},
		{"restrictive false", []expr.Expr{yes}, no, false, "restriction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := exec.RLSRuntimeGroup{Restrictive: []exec.RLSRuntimeClause{{Name: "restriction", Expression: tc.r}}}
			for _, n := range tc.p {
				group.Permissive = append(group.Permissive, clause(n))
			}
			p := &exec.RLSProgram{Table: "t", Groups: []exec.RLSRuntimeGroup{group}}
			pass, name, err := exec.EvaluateRowSecurity(p, &parent, nil)
			if err != nil || pass != tc.pass || name != tc.named {
				t.Fatal(pass, name, err)
			}
		})
	}
	calls := 0
	failure := errors.New("policy evaluation witness")
	dangerous := rlsCountedExpression{Expr: yes, calls: &calls, failure: failure}
	p := &exec.RLSProgram{Table: "t", Groups: []exec.RLSRuntimeGroup{{Restrictive: []exec.RLSRuntimeClause{clause(dangerous)}}}}
	if pass, _, err := exec.EvaluateRowSecurity(p, &parent, nil); err != nil || pass || calls != 0 {
		t.Fatal("restrictive-only demanded", pass, calls, err)
	}
	p.Groups[0].Permissive = []exec.RLSRuntimeClause{clause(dangerous)}
	if _, _, err := exec.EvaluateRowSecurity(p, &parent, nil); err != failure || calls != 1 {
		t.Fatal("evaluation error replaced", calls, err)
	}
	p.Groups = []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{clause(yes)}}, {Permissive: []exec.RLSRuntimeClause{clause(no)}}}
	if pass, _, err := exec.EvaluateRowSecurity(p, &parent, nil); err != nil || pass {
		t.Fatal("command groups flattened", pass, err)
	}
	p.Groups = []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{clause(yes)}, Restrictive: []exec.RLSRuntimeClause{{Name: "named", Expression: no}}}}
	err := exec.CheckRowSecurity(p, &parent, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != `new row violates row-level security policy "named" for table "t"` {
		t.Fatal(err)
	}
}

func TestRLSPolicyFrame(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	allowed, err := policyUnitBind(t, e, op, d, "allowed")
	if err != nil {
		t.Fatal(err)
	}
	yes, err := policyUnitBind(t, e, op, d, "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 99, Frame: 99}}
	value, err := yes.Expression().Eval(&parent)
	if err != nil {
		t.Fatal(err)
	}
	row := []sqltype.Value{sqltype.Null(d.Columns[0].Usage.Type), value}
	old := []sqltype.Value{sqltype.Null(d.Columns[0].Usage.Type), sqltype.Null(d.Columns[1].Usage.Type)}
	parent.Row, parent.OldRow = old, old
	p := &exec.RLSProgram{Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{Expression: allowed.Expression()}}}}}
	if pass, _, err := exec.EvaluateRowSecurity(p, &parent, row); err != nil || !pass {
		t.Fatal("NEW target frame", pass, err)
	}
	if parent.Scope != 99 || parent.Frame != 99 || !parent.Row[1].IsNull() || !parent.OldRow[1].IsNull() {
		t.Fatal("policy contaminated caller OLD/FROM frame")
	}
	scratch := &p.Groups[0].Permissive[0].Evaluation
	if scratch.Row != nil || scratch.Outer != nil || scratch.Query != nil || scratch.Context != nil {
		t.Fatal("clause retained borrowed row frame")
	}
	failure := errors.New("frame error witness")
	calls := 0
	p.Groups[0].Permissive[0].Expression = rlsCountedExpression{Expr: allowed.Expression(), calls: &calls, failure: failure}
	if _, _, err := exec.EvaluateRowSecurity(p, &parent, row); err != failure {
		t.Fatal("frame error identity", err)
	}
	if scratch.Row != nil || scratch.Outer != nil || scratch.Context != nil {
		t.Fatal("error retained borrowed frame")
	}
	cancelled, cancel := context.WithCancel(parent.Context)
	cancel()
	parent.Context = cancelled
	if _, _, err := exec.EvaluateRowSecurity(p, &parent, row); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled frame", err)
	}
	if calls != 1 || scratch.Row != nil || scratch.Context != nil {
		t.Fatal("cancellation evaluated/retained row", calls)
	}
}

func TestRLSSourceGuard(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	allowed, err := policyUnitBind(t, e, op, d, "allowed")
	if err != nil {
		t.Fatal(err)
	}
	yes, err := policyUnitBind(t, e, op, d, "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	value, err := yes.Expression().Eval(&c)
	if err != nil {
		t.Fatal(err)
	}
	visible := []sqltype.Value{sqltype.Null(d.Columns[0].Usage.Type), value}
	hidden := []sqltype.Value{sqltype.Null(d.Columns[0].Usage.Type), sqltype.Null(d.Columns[1].Usage.Type)}
	p := &exec.RLSProgram{Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{Expression: allowed.Expression()}}}}}
	source := &exec.Source{Anonymous: [][]sqltype.Value{visible, hidden}, RLS: p}
	defer source.Close()
	stage, err := exec.OpenNode(context.Background(), &exec.State{Source: source}, &plan.SourceNode{QuerySchema: d.Columns})
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if stage.Produce == nil || stage.Count() != 0 {
		t.Fatal("guard is not lazy")
	}
	if err := stage.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stage.Count() != 1 || stage.Index(0) != 0 || source.Count() != 2 {
		t.Fatal("guard lost physical occurrence identity", stage.Count(), source.Count())
	}
}

func TestRLSRuntimeImport(t *testing.T) {
	e, op, done, d := policyUnitFixture(t)
	defer done()
	policy, err := policyUnitBind(t, e, op, d, "EXISTS(SELECT 1 FROM helper h WHERE h.id=t.id)")
	if err != nil {
		t.Fatal(err)
	}
	read := e.CatalogRead()
	definition := policy.Presentation()
	if len(definition.Sources()) != 2 {
		t.Fatal("fixture source manifest", len(definition.Sources()))
	}
	var importer view.StoredQueryImporter
	binding := analyze.Binding{}
	imported, _, err := stored.RewriteViewExpression(read, policy.Expression(), nil, func(n expr.Expr) (expr.Expr, bool, error) { return n, false, nil }, func(root plan.Node, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
		return importer.GraftQuery(&binding, read, root, definition, d, rewrite)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.QuerySources) != 1 || binding.QuerySources[0].Ref != definition.Sources()[1].Ref || len(definition.Sources()) != 2 {
		t.Fatal("target scanned or frozen identity replaced", binding.QuerySources)
	}
	if imported == nil || !binding.SubqueryDemand {
		t.Fatal("nested graph not imported")
	}
	// The selection retains the original payload instead of storing a rewritten
	// runtime graph back into the immutable relation root.
	next := *d
	next.Security.RLSEnabled, next.Security.RLSForced = true, true
	next.Security.Policies = []catalog.PolicyDefinition{{Name: "all", Permissive: true, Command: catalog.PolicyAll, Roles: []catalog.PolicyRole{{Public: true}}, Using: view.PolicyPayloadOf(policy)}}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Bootstrap bypass remains metadata-only; actual imported-source assertions
	// above exercise the common graft independently of row-security eligibility.
	selection, err := access.SelectRowSecurity(auth.Roles, &next, auth.State.CurrentUser, catalog.PolicySelect, false, false)
	if err != nil || selection.Count != 0 || next.Security.Policies[0].Using != view.PolicyPayloadOf(policy) {
		t.Fatal("payload/fresh bypass", selection, err)
	}
}

// Runtime call lifetimes include pinned oprcode/function providers even though
// PostgreSQL-style catalog dependency recording deliberately omits those edges.
func TestRLSPolicyLifetimeManifest(t *testing.T) {
	e, op, done, owner := policyUnitFixture(t)
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE DOMAIN manifest_type AS INT CHECK(VALUE>0)")
	for _, condition := range []string{"id=1", "random()>=0", "EXISTS(SELECT 1 FROM helper h WHERE h.id=t.id)", "id::manifest_type>0 AND 'manifest_type'::REGTYPE IS NOT NULL"} {
		t.Run(condition, func(t *testing.T) {
			policy, err := policyUnitBind(t, e, op, owner, condition)
			if err != nil {
				t.Fatal(err)
			}
			read := e.CatalogRead()
			if err := policy.Validate(read, owner); err != nil {
				t.Fatal("full manifest rejected", err)
			}
			semantic, err := stored.ExpressionDependRefs(context.Background(), read, policy.Expression())
			if err != nil {
				t.Fatal(err)
			}
			catalogEdges := make(map[ids.ObjectAddress]bool, len(semantic))
			for _, address := range semantic {
				catalogEdges[address] = true
				found := false
				for _, dependency := range policy.Presentation().Dependencies() {
					found = found || dependency.Ref.Address == address
				}
				if !found {
					t.Fatal("lost semantic dependency", address)
				}
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			evaluation := exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: read}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineConsumer: owner, RoutineChecks: policy.Presentation().Dependencies()}}
			if err := exec.AdmitExpression(&evaluation, policy.Expression(), read); err != nil {
				t.Fatal("original retained manifest did not admit real expression", err)
			}
			pinned := 0
			analyze.WalkQueryArgumentExpression(policy.Expression(), func(node expr.Expr) {
				procedure, err := exec.ExpressionRoutineDescriptor(node)
				if err != nil {
					t.Fatal(err)
				}
				if procedure == nil {
					return
				}
				if err := exec.AdmitRoutineDescriptor(auth.AdmissionAccess(), policy.Presentation().Dependencies(), procedure, owner); err != nil {
					t.Fatal("lost captured callable lifetime", err)
				}
				if err := exec.AdmitRoutineDescriptor(auth.AdmissionAccess(), nil, procedure, owner); err == nil {
					t.Fatal("missing manifest was exempted")
				}
				if stored.DependPinned(read, nil, procedure.Address()) {
					pinned++
					if catalogEdges[procedure.Address()] {
						t.Fatal("pinned runtime provider leaked into pg_depend recording")
					}
				}
			})
			if pinned == 0 {
				t.Fatal("fixture did not cover a pinned runtime provider")
			}
			invalid := append([]catalog.ExpressionDependency(nil), policy.Presentation().Dependencies()...)
			invalid = append(invalid, catalog.ExpressionDependency{})
			corrupt := stored.NewPolicy(policy.Syntax(), policy.Expression(), policy.Presentation().With(func(parts *stored.ViewParts) { parts.Dependencies = invalid }))
			if err := corrupt.Validate(read, owner); err == nil {
				t.Fatal("invalid extra provider identity accepted")
			}
		})
	}
}

func TestRLSPolicyImportedViewDemand(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		uses            []int
	}{
		{"key", "EXISTS(SELECT 1 FROM helper_view h WHERE h.lookup=t.id)", []int{1}},
		{"presence", "EXISTS(SELECT 1 FROM helper_view)", []int{}},
		{"all", "EXISTS(SELECT 1 FROM helper_view h WHERE h.lookup=t.id AND h.unused=t.id)", []int{0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, op, done, owner := policyUnitFixture(t)
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE VIEW helper_view AS SELECT id+10 AS unused,id AS lookup FROM helper")
			policy, err := policyUnitBind(t, e, op, owner, tc.condition)
			if err != nil {
				t.Fatal(err)
			}
			definition := policy.Presentation()
			if len(definition.Sources()) != 2 {
				t.Fatal("unexpected source occurrences")
			}
			frozen := definition.Sources()[1]
			reads := slices.Clone(frozen.Reads)
			enginetest.RoleExecute(t, e, op, "ALTER VIEW helper_view RENAME COLUMN lookup TO stable_key")
			read := e.CatalogRead()
			var importer view.StoredQueryImporter
			binding := analyze.Binding{}
			_, _, err = stored.RewriteViewExpression(read, policy.Expression(), nil, func(n expr.Expr) (expr.Expr, bool, error) { return n, false, nil }, func(root plan.Node, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
				return importer.GraftQuery(&binding, read, root, definition, owner, rewrite)
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(binding.QuerySources) != 1 {
				t.Fatal("target imported or occurrence duplicated")
			}
			leaf := binding.QuerySources[0]
			if leaf.Uses == nil || !slices.Equal(leaf.Uses, tc.uses) {
				t.Fatal("lost exact output demand", leaf.Uses, tc.uses)
			}
			if leaf.Ref != frozen.Ref || !slices.Equal(leaf.FrozenAttributes, frozen.Attributes) || leaf.Relation.Columns[1].Name != "stable_key" {
				t.Fatal("lost frozen/live occurrence identity")
			}
			if !slices.Equal(definition.Sources()[1].Reads, reads) {
				t.Fatal("mutated frozen read manifest")
			}
		})
	}
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
