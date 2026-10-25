package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var analysisOnlyCases = []struct {
	name, sql string
	demand bool
}{
	{"unused", "WITH unused AS (SELECT pg_catalog.abs(-7::integer)) SELECT 1 AS v", false},
	{"nested", "WITH c AS (WITH unused AS (SELECT pg_catalog.abs(v) FROM t) SELECT id,v FROM t) SELECT id,v FROM c ORDER BY id", false},
	{"dead", "WITH c AS (SELECT id,CASE WHEN FALSE THEN pg_catalog.abs(v) ELSE v END AS v FROM t) SELECT id,v FROM c ORDER BY id", false},
	{"materialized-dead", "WITH c AS MATERIALIZED (SELECT id,CASE WHEN FALSE THEN pg_catalog.abs(v) ELSE v END AS v FROM t) SELECT id,v FROM c ORDER BY id", false},
	{"not-materialized-dead", "WITH c AS NOT MATERIALIZED (SELECT id,CASE WHEN FALSE THEN pg_catalog.abs(v) ELSE v END AS v FROM t) SELECT id,v FROM c ORDER BY id", false},
	{"unused-window", "SELECT id FROM t WINDOW unused AS (ORDER BY pg_catalog.abs(v)) ORDER BY id", true},
	{"scalar", "SELECT pg_catalog.abs(v) FROM t", true},
	{"scalar-subquery", "SELECT (SELECT pg_catalog.abs(v)) FROM t", true},
	{"referenced", "WITH c AS (SELECT pg_catalog.abs(v) AS v FROM t) SELECT v FROM c", true},
	{"active-window", "SELECT id,row_number() OVER used FROM t WINDOW used AS (ORDER BY pg_catalog.abs(v)) ORDER BY id", true},
}

type analysisOnlyGraph struct {
	projects map[*plan.ProjectNode]plan.ProjectNode
	vectors map[*plan.ProjectNode][2]*expr.Expr
	queries []*plan.Query
	calls []routineTestCapture
	declarations []catalog.Procedure
	dependencies []catalog.ExpressionDependency
}

// Snapshot the actual retained graph, not merely its executable occurrences.
func analysisOnlySnapshot(root plan.Node, dependencies []catalog.ExpressionDependency) analysisOnlyGraph {
	s := analysisOnlyGraph{
		projects: make(map[*plan.ProjectNode]plan.ProjectNode),
		vectors: make(map[*plan.ProjectNode][2]*expr.Expr),
		dependencies: append([]catalog.ExpressionDependency(nil), dependencies...),
	}
	plan.VisitCTEQuery(root, func(node plan.Node) {
		if p, ok := node.(*plan.ProjectNode); ok {
			copy := *p
			copy.Expressions = append([]expr.Expr(nil), p.Expressions...)
			copy.AnalysisExpressions = append([]expr.Expr(nil), p.AnalysisExpressions...)
			s.projects[p] = copy
			var vectors [2]*expr.Expr
			if len(p.Expressions) != 0 {
				vectors[0] = &p.Expressions[0]
			}
			if len(p.AnalysisExpressions) != 0 {
				vectors[1] = &p.AnalysisExpressions[0]
			}
			s.vectors[p] = vectors
		}
	}, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			if q, ok := node.(plan.SubqueryExpr); ok {
				s.queries = append(s.queries, q.QueryPlan)
			}
			p, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil {
				panic(err)
			}
			if p != nil {
				capture := routineTestCapture{Procedure: p}
				for _, d := range dependencies {
					if d.Ref.Address == p.Address() {
						capture.Ref = d.Ref
						break
					}
				}
				s.calls = append(s.calls, capture)
				s.declarations = append(s.declarations, *p)
			}
		})
	}, true)
	return s
}

func analysisOnlyUnchanged(t *testing.T, before analysisOnlyGraph, root plan.Node, dependencies []catalog.ExpressionDependency) {
	t.Helper()
	after := analysisOnlySnapshot(root, dependencies)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("execution changed retained graph, frames, declarations or manifest")
	}
	for i, query := range before.queries {
		if query != after.queries[i] {
			t.Fatal("execution replaced original CTE/subquery identity")
		}
	}
	for i, call := range before.calls {
		if call.Procedure != after.calls[i].Procedure {
			t.Fatal("execution substituted original declaration")
		}
	}
	for project, vectors := range before.vectors {
		if vectors != after.vectors[project] {
			t.Fatal("execution replaced original analyzed vectors")
		}
	}
}

func analysisOnlyLifetimeRejected(t *testing.T, kind string, err error) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if err == nil || errors.As(err, &diagnostic) && diagnostic.Code == "42501" {
		t.Fatalf("%s: expected lifetime rejection, not invocation authority: %v", kind, err)
	}
}

func analysisOnlyFixture(t *testing.T, sql string) (*Engine, *plan.Query, []catalog.ExpressionDependency) {
	t.Helper()
	// Reuse the core's real catalog/source fixture, then bind WITH through the
	// binder rather than its single-table FROM convenience wrapper.
	e, _, source := queryCoreFixture(t, "SELECT id FROM t")
	source.Close()
	run(t, e, "CREATE USER analysis_reader")
	raw := statement(t, sql).(*parser.Select)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	b := NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.Query.Auth = &auth
	var q *plan.Query
	if err == nil {
		q, err = b.BindSelect(raw, false)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return e, q, b.Dependencies
}

func analysisOnlyAuthority(t *testing.T, e *Engine, grant string) AuthorizationView {
	t.Helper()
	run(t, e, grant)
	op, done := roleOperation(t, e, "analysis_reader")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func analysisOnlyAdmit(ctx context.Context, q *plan.Query, dependencies []catalog.ExpressionDependency, auth AuthorizationView) error {
	// Each invocation owns all mutable execution/frame state. The query and
	// captured authority are immutable and may be shared by finite workers.
	c := exec.Context{
		EvalStatement: exec.EvalStatement{Context: ctx, Catalog: auth.Catalog},
		EvalFrame: exec.EvalFrame{Scope: 1, Frame: q.Root.(*plan.ProjectNode).Frame},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: dependencies},
	}
	root, err := view.PreflightQuery(&c, q.Root)
	if err != nil {
		return err
	}
	return view.ValidateQueryJoinExecution(&c, root, auth.Catalog, 0)
}

func analysisOnlyOutcome(err error, denied bool) error {
	if !denied {
		return err
	}
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function abs"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		return fmt.Errorf("denial=%#v (%v), want %#v", diagnostic, err, want)
	}
	return nil
}

func TestQueryAnalysisOnlyDemand(t *testing.T) {
	for _, tc := range analysisOnlyCases {
		t.Run(tc.name, func(t *testing.T) {
			e, q, dependencies := analysisOnlyFixture(t, tc.sql)
			before := analysisOnlySnapshot(q.Root, dependencies)
			if len(before.calls) == 0 {
				t.Fatal("retained original calls missing")
			}
			for _, phase := range []struct { sql string; denied bool }{
				{"GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC", false},
				{"REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", true},
				{"GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO analysis_reader", false},
				{"REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM analysis_reader", true},
			} {
				auth := analysisOnlyAuthority(t, e, phase.sql)
				for range 3 {
					if err := analysisOnlyOutcome(analysisOnlyAdmit(context.Background(), q, dependencies, auth), phase.denied && tc.demand); err != nil {
						t.Fatal(phase.sql, err)
					}
				}
				analysisOnlyUnchanged(t, before, q.Root, dependencies)
			}
			if tc.name == "dead" {
				// Inspect the same real referenced root that DerivedNode admission
				// preflights, not the retained definition's original expression.
				var derived *plan.DerivedNode
				plan.VisitExecutableQuery(q.Root, func(n plan.Node) {
					if d, ok := n.(*plan.DerivedNode); ok { derived = d }
				}, func(expr.Expr) {})
				if derived == nil { t.Fatal("missing actual referenced CTE") }
				c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: derived.Query.Root.(*plan.ProjectNode).Frame}}
				root, err := view.PreflightQuery(&c, derived.Query.Root)
				if err != nil { t.Fatal(err) }
				if len(analysisOnlySnapshot(root, dependencies).calls) != 0 { t.Fatal("actual root retained dead abs arm") }
			}
		})
	}
}

func TestQueryAnalysisOnlyOriginalLifetime(t *testing.T) {
	for _, sql := range []string{analysisOnlyCases[0].sql, analysisOnlyCases[1].sql, analysisOnlyCases[5].sql} {
		t.Run(sql, func(t *testing.T) {
			t.Run("frozen-owner", func(t *testing.T) {
				analysisOnlyFrozenLifetime(t, sql)
			})
			e, q, dependencies := analysisOnlyFixture(t, sql)
			original := analysisOnlySnapshot(q.Root, dependencies)
			if len(original.calls) == 0 { t.Fatal("no retained original manifest") }
			e.Mu.Lock()
			auth := catalogAuthorization(e, e.Catalog.Current())
			e.Mu.Unlock()
			foreign := New()
			foreign.Mu.Lock()
			other := catalogAuthorization(foreign, foreign.Catalog.Current())
			foreign.Mu.Unlock()
			for _, call := range original.calls {
				if err := exec.AdmitRoutineDescriptor(RoutineAccessOf(auth), dependencies, call.Procedure, nil); err != nil { t.Fatal("genuine original", err) }
				for _, kind := range []string{"missing", "revision", "foreign", "epoch", "substitution"} {
					probe := append([]catalog.ExpressionDependency(nil), dependencies...)
					selected, authority := call.Procedure, auth
					switch kind {
					case "missing":
						probe = nil
						for _, d := range dependencies { if d.Ref.Address != call.Ref.Address { probe = append(probe, d) } }
					case "revision":
						for i := range probe { if probe[i].Ref.Address == call.Ref.Address { probe[i].Revision++ } }
					case "foreign": authority = other
					case "epoch":
						for i := range probe { probe[i].Ref.Epoch++ }
					case "substitution": selected = other.Catalog.MustProcedure(call.Ref.Address)
					}
					analysisOnlyLifetimeRejected(t, kind, exec.AdmitRoutineDescriptor(RoutineAccessOf(authority), probe, selected, nil))
				}
			}
			if err := e.Reset(context.Background()); err != nil { t.Fatal(err) }
			e.Mu.Lock()
			after := catalogAuthorization(e, e.Catalog.Current())
			e.Mu.Unlock()
			for _, call := range original.calls {
				analysisOnlyLifetimeRejected(t, "replacement", exec.AdmitRoutineDescriptor(RoutineAccessOf(after), dependencies, call.Procedure, nil))
			}
			analysisOnlyUnchanged(t, original, q.Root, dependencies)
		})
	}
}

// Exercise the frozen-definition owner with whole real graphs. Its validation
// traverses every retained frame and routine occurrence independently of the
// invocation walker; no EXECUTE admission belongs to this boundary.
func analysisOnlyFrozenLifetime(t *testing.T, sql string) {
	t.Helper()
	e, _, source := queryCoreFixture(t, "SELECT id FROM t")
	source.Close()
	run(t, e, "CREATE VIEW analysis_lifetime_view AS "+sql)
	ref := roleTableRef(t, e, "analysis_lifetime_view")
	read := e.CatalogRead()
	owner := read.MustRelation(ref.Address)
	definition := stored.ViewOf(owner)
	if definition == nil {
		t.Fatal("missing actual frozen definition")
	}
	original := analysisOnlySnapshot(definition.Query().Root, definition.Dependencies())
	var selected routineTestCapture
	for _, call := range original.calls {
		if call.Procedure.Name() == "abs" {
			selected = call
			break
		}
	}
	if selected.Procedure == nil || len(definition.Parts().RoutineRefs) == 0 {
		t.Fatal("frozen graph lost retained abs occurrence or manifest")
	}
	if err := definition.Validate(read, owner); err != nil {
		t.Fatal("genuine frozen graph", err)
	}
	run(t, e, "REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC")
	read = e.CatalogRead()
	owner = read.MustRelation(ref.Address)
	if err := definition.Validate(read, owner); err != nil {
		t.Fatal("lifetime owner acquired invocation EXECUTE demand", err)
	}
	// The frozen owner preserves historical declarations across catalog COW;
	// its nonzero manifest revision is not a current-revision equality fence.
	cow := catalog.CloneState(read.State())
	current := *read.MustProcedure(selected.Ref.Address)
	current.ObjectRevision++
	cow.SetDescriptor(current.Address(), &current)
	if err := definition.Validate(read.WithState(cow), owner); err != nil {
		t.Fatal("historical nonzero manifest rejected after declaration COW", err)
	}
	foreign := New().CatalogRead()
	for _, kind := range []string{"missing", "revision", "foreign", "epoch", "substitution"} {
		t.Run(kind, func(t *testing.T) {
			parts := definition.Parts()
			parts.Dependencies = append([]catalog.ExpressionDependency(nil), parts.Dependencies...)
			parts.RoutineRefs = maps.Clone(parts.RoutineRefs)
			probeRead := read
			switch kind {
			case "missing":
				var retained []catalog.ExpressionDependency
				for _, dependency := range parts.Dependencies {
					if dependency.Ref.Address != selected.Ref.Address {
						retained = append(retained, dependency)
					}
				}
				parts.Dependencies = retained
			case "revision":
				// Frozen declarations may retain historical nonzero revisions
				// across COW. Zero is invalid proof, not an alternate history.
				for i := range parts.Dependencies {
					if parts.Dependencies[i].Ref.Address == selected.Ref.Address {
						parts.Dependencies[i].Revision = 0
					}
				}
			case "foreign", "epoch":
				for path, occurrence := range parts.RoutineRefs {
					if occurrence.Address != selected.Ref.Address {
						continue
					}
					if kind == "foreign" {
						occurrence = foreign.Owner().Domain.Ref(read.Epoch(), occurrence.Address)
					} else {
						occurrence.Epoch++
					}
					parts.RoutineRefs[path] = occurrence
				}
			case "substitution":
				state := catalog.CloneState(read.State())
				// Equal builtin OID is not declaration provenance. Submit the
				// substituted descriptor through the graph's actual owner.
				state.SetDescriptor(selected.Ref.Address, foreign.MustProcedure(selected.Ref.Address))
				probeRead = read.WithState(state)
			}
			probe := stored.NewView(parts)
			candidate := *owner
			candidate.View = stored.ViewPayload(probe)
			analysisOnlyLifetimeRejected(t, kind, probe.Validate(probeRead, &candidate))
		})
	}
	analysisOnlyUnchanged(t, original, definition.Query().Root, definition.Dependencies())
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	replaced := e.CatalogRead()
	// The fresh catalog still has the same builtin address; the retained graph
	// cannot acquire a new lifetime by resolving that address again.
	if replaced.MustProcedure(selected.Ref.Address).Address() != selected.Ref.Address {
		t.Fatal("replacement lost equal-OID control")
	}
	analysisOnlyLifetimeRejected(t, "replacement", definition.Validate(replaced, owner))
	analysisOnlyUnchanged(t, original, definition.Query().Root, definition.Dependencies())
}

func TestQueryAnalysisOnlyConcurrentPreflight(t *testing.T) {
	for _, index := range []int{0, 1, 2, 5, 6} {
		tc := analysisOnlyCases[index]
		t.Run(tc.name, func(t *testing.T) {
			e, q, dependencies := analysisOnlyFixture(t, tc.sql)
			before := analysisOnlySnapshot(q.Root, dependencies)
			for _, phase := range []struct { sql string; denied bool }{
				{"REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", true},
				{"GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO analysis_reader", false},
				{"REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM analysis_reader", true},
			} {
				// Publish a COW authority snapshot before releasing this phase's
				// barrier. No worker consults changing global session/catalog state.
				auth := analysisOnlyAuthority(t, e, phase.sql)
				ctx, cancel := context.WithCancel(context.Background())
				start := make(chan struct{})
				failures := make(chan error, 4)
				var workers sync.WaitGroup
				for range 4 {
					workers.Add(1)
					go func() {
						defer workers.Done()
						<-start
						for range 8 {
							if err := analysisOnlyOutcome(analysisOnlyAdmit(ctx, q, dependencies, auth), phase.denied && tc.demand); err != nil { failures <- err; return }
						}
					}()
				}
				close(start)
				workers.Wait()
				cancel()
				close(failures)
				for err := range failures { t.Error(err) }
				analysisOnlyUnchanged(t, before, q.Root, dependencies)
			}
		})
	}
}
