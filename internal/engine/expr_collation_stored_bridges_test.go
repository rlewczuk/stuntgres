package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Use the ordinary admitted statement/binding path, including its occurrence
// manifest and current attribute capabilities, rather than synthetic view roots.
func exprCollationStoredBinding(t *testing.T, e *Engine, owner *Session, q string) (binding, *StatementScope) {
	t.Helper()
	op := transactionOperation(t, owner)
	t.Cleanup(op.Done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	ast := statement(t, q)
	if _, err := scope.Describe(ast); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var bound binding
	if err == nil {
		bound, err = e.describe(op.ctx, ast, auth, scope.lockedRef, scope.input, op, nil, &scope.queryLockAdmission)
		scope.routineAuth, scope.routineDependencies = auth, bound.dependencies
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return bound, scope
}

func TestExprCollationStoredPreflightDemand(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	for _, q := range []string{
		`CREATE TABLE expr_preflight_t(id INT)`,
		`CREATE DOMAIN expr_preflight_c AS TEXT COLLATE "C"`,
		`CREATE DOMAIN expr_preflight_posix AS TEXT COLLATE "POSIX"`,
		`CREATE VIEW expr_preflight_v AS SELECT id,CAST(CASE WHEN ('A'::expr_preflight_c)=('A'::expr_preflight_posix) THEN TRUE ELSE FALSE END AS TEXT) AS unused,TRUE AS selected FROM expr_preflight_t ORDER BY id`,
	} {
		sessionSQL(t, owner, q)
	}
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "expr_preflight_v")
	definition := viewOf(view.MustRelation(ref.Address))
	frozen := definition.query.Root
	for _, tc := range []struct {
		name, q string
		uses    []int
		fail    bool
	}{
		{"unused", `SELECT id,selected FROM expr_preflight_v WHERE id=1`, []int{0, 2}, false},
		{"selected", `SELECT unused FROM expr_preflight_v WHERE id=1`, []int{1, 0}, true},
		{"full", `SELECT * FROM expr_preflight_v WHERE id=1`, []int{0, 1, 2}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bound, scope := exprCollationStoredBinding(t, e, owner, tc.q)
			if len(bound.querySources) != 1 || !reflect.DeepEqual(bound.querySources[0].uses, tc.uses) {
				t.Fatal("ordinary source occurrence uses", bound.querySources, tc.uses)
			}
			evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background(), statementScope: scope, Input: scope.input, Output: scope.output}, evalFrame: evalFrame{Scope: 1, Frame: bound.query.Root.(*projectNode).Frame}}
			err := validateQueryJoinExecution(&evaluation, bound.query.Root, e.CatalogRead(), 0)
			if tc.fail {
				var diagnostic *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatal("demanded legality preflight lost provider error", err)
				}
			} else if err != nil {
				t.Fatal("legality preflight demanded unused stored payload", err)
			}
			if definition.query.Root != frozen {
				t.Fatal("legality mutated frozen definition")
			}
		})
	}
}

func TestExprCollationNestedPayloadDemand(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	for _, q := range []string{
		`CREATE TABLE expr_nested_t(id INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`,
		`CREATE VIEW expr_nested_inner AS SELECT id,a=b AS unused,a COLLATE "C"=b AS selected FROM expr_nested_t ORDER BY id`,
		`CREATE VIEW expr_nested_outer AS SELECT id,unused,selected FROM expr_nested_inner`,
		`CREATE VIEW expr_nested_repeat AS SELECT l.id,l.unused,r.selected FROM expr_nested_inner l JOIN expr_nested_inner r ON l.id=r.id`,
		`CREATE VIEW expr_nested_frames AS SELECT l.id,r.selected FROM (SELECT id FROM expr_nested_inner) l JOIN (SELECT id,selected FROM expr_nested_inner) r ON l.id=r.id`,
	} {
		sessionSQL(t, owner, q)
	}
	view := e.CatalogRead()
	innerRef := catalogRef(t, e, view, "expr_nested_inner")
	inner := view.MustRelation(innerRef.Address)
	innerRoot := viewOf(inner).query.Root
	for _, tc := range []struct {
		name, q string
		outer   string
		want    [][]bool
	}{
		{"selected", `SELECT id,selected FROM expr_nested_outer WHERE id=1`, "expr_nested_outer", [][]bool{{true, false, true}}},
		{"full", `SELECT * FROM expr_nested_outer WHERE id=1`, "expr_nested_outer", [][]bool{{true, true, true}}},
		{"repeated", `SELECT id,selected FROM expr_nested_repeat WHERE id=1`, "expr_nested_repeat", [][]bool{{true, false, false}, {true, false, true}}},
		{"frames", `SELECT id,selected FROM expr_nested_frames WHERE id=1`, "expr_nested_frames", [][]bool{{true, false, false}, {true, false, true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outerRef := catalogRef(t, e, view, tc.outer)
			outer := view.MustRelation(outerRef.Address)
			frozenRoot := viewOf(outer).query.Root
			reads := make([][]int32, len(viewOf(outer).sources))
			for i, leaf := range viewOf(outer).sources {
				reads[i] = append([]int32(nil), leaf.reads...)
			}
			if (tc.name == "selected" || tc.name == "full") && !reflect.DeepEqual(reads[0], inner.Attributes) {
				t.Fatal("fixture lacks full frozen nested-read manifest", reads, inner.Attributes)
			}
			bound, scope := exprCollationStoredBinding(t, e, owner, tc.q)
			e.mu.Lock()
			source, err := e.captureQuerySourcesLocked(scope, bound.querySources, false)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer source.close()
			bundle := source.nestedInputs
			if bundle == nil {
				t.Fatal("ordinary outer view did not open nested source")
			}
			occurrences := bundle.sources
			if occurrences == nil {
				occurrences = []*querySource{bundle}
			}
			if len(occurrences) != len(tc.want) {
				t.Fatal("nested occurrence count", len(occurrences), len(tc.want))
			}
			for i, child := range occurrences {
				project, ok := child.nestedRoot.(*projectNode)
				if !ok {
					t.Fatal("missing demanded nested projection", i, child.nestedRoot)
				}
				if len(project.Expressions) != 3 {
					t.Fatal("nested output layout changed", project.Schema())
				}
				for slot, live := range tc.want[i] {
					_, dead := project.Expressions[slot].(literalExpression)
					if dead == live {
						t.Fatal("nested executable occurrence demand", tc.name, i, slot, "dead", dead, "want live", live)
					}
				}
				if child.authority != source.authority {
					t.Fatal("nested authorization capture changed")
				}
			}
			if viewOf(outer).query.Root != frozenRoot || viewOf(inner).query.Root != innerRoot {
				t.Fatal("nested demand mutated frozen source roots")
			}
			for i, leaf := range viewOf(outer).sources {
				if !reflect.DeepEqual(leaf.reads, reads[i]) {
					t.Fatal("executable pruning weakened frozen ACL/dependency reads", i, leaf.reads, reads[i])
				}
			}
		})
	}
}

func TestExprCollationDomainDerivation(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	for _, q := range []string{
		`CREATE TABLE expr_domain_t(a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`,
		`CREATE DOMAIN expr_domain_c AS TEXT COLLATE "C"`,
		`CREATE DOMAIN expr_domain_default AS TEXT`,
	} {
		sessionSQL(t, owner, q)
	}
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	for _, tc := range []struct {
		name, q                            string
		ref                                uint32
		conflict, top, childConflict, want bool
	}{
		{"implicit-conflict-reset", `SELECT (a||b)::expr_domain_c=a AS v FROM expr_domain_t`, 950, false, false, true, false},
		{"top-explicit", `SELECT ('A' COLLATE "POSIX")::expr_domain_c=b AS v FROM expr_domain_t`, 951, false, true, false, true},
		{"nested-explicit-reset", `SELECT (('A' COLLATE "POSIX")||'')::expr_domain_c=b AS v FROM expr_domain_t`, 100, true, false, false, false},
		{"default-explicit-bubble", `SELECT (('A' COLLATE "POSIX")||'')::expr_domain_default=b AS v FROM expr_domain_t`, 951, false, false, false, true},
		{"default-conflict-bubble", `SELECT (a||b)::expr_domain_default=a AS v FROM expr_domain_t`, 100, true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bound, _ := exprCollationStoredBinding(t, e, owner, tc.q)
			comparison, ok := bound.query.Root.(*projectNode).Expressions[0].(comparisonExpression)
			if !ok {
				t.Fatal("missing real comparison")
			}
			ref, conflict := comparison.Predicate.InputCollation()
			if (!tc.conflict && ref.ID != tc.ref) || !ref.Valid() || conflict != tc.conflict || comparison.ResultUsage().Collation.ID != 0 {
				t.Fatal("consumer authority", ref, conflict, comparison.ResultUsage())
			}
			left := comparison.Left
			if tc.top && left.ExplicitCollation().Character != strings.Index(tc.q, "COLLATE")+1 {
				t.Fatal("top explicit diagnostic location lost", left.ExplicitCollation())
			}
			if tc.name == "nested-explicit-reset" || tc.name == "implicit-conflict-reset" {
				if left.ResultUsage().Collation.ID != 950 || left.CollationIndeterminate() || left.ExplicitCollation().Character != 0 {
					t.Fatal("nondefault domain did not supply implicit declaration", left.ResultUsage(), left.ExplicitCollation())
				}
			}
			if tc.childConflict {
				if !left.Properties().RowDependent || len(bound.querySources) != 1 || !reflect.DeepEqual(bound.querySources[0].uses, []int{0, 1}) {
					t.Fatal("domain reset discarded child properties/dependencies", left.Properties(), bound.querySources)
				}
			}
			value, err := comparison.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: bound.query.Root.(*projectNode).Frame, Row: []sqltype.Value{sqltype.String(text, "A"), sqltype.String(text, "A")}}})
			if tc.conflict {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}) {
					t.Fatal("consumer did not demand domain-derived conflict", err)
				}
			} else {
				truth, failure := value.Boolean()
				if err != nil || failure != nil || truth != tc.want {
					t.Fatal("domain-derived comparison", value, err, failure)
				}
			}
		})
	}
	t.Run("stored-destination", func(t *testing.T) {
		sessionSQL(t, owner, `CREATE TABLE expr_domain_storage(a TEXT COLLATE "C",b TEXT COLLATE "POSIX",v TEXT COLLATE "POSIX" DEFAULT (('A' COLLATE "POSIX")||'')::expr_domain_c,g TEXT COLLATE "POSIX" GENERATED ALWAYS AS ((a||b)::expr_domain_c) STORED)`)
		view := e.CatalogRead()
		ref := catalogRef(t, e, view, "expr_domain_storage")
		table := view.MustRelation(ref.Address)
		definition := table.ColumnMeta.Defaults[table.Attributes[2]].Expression
		generation := table.ColumnMeta.Generations[table.Attributes[3]]
		if definition.ResultUsage() != table.Columns[2].Usage || generation.Expression.ResultUsage() != table.Columns[3].Usage || generation.Planned.ResultUsage() != table.Columns[3].Usage {
			t.Fatal("storage lost exact immutable destination usage")
		}
		if !generation.Expression.Properties().RowDependent || !generation.Planned.Properties().RowDependent {
			t.Fatal("storage erased domain child row demand")
		}
		rendered, err := defaultSQL(context.Background(), view, definition)
		if err != nil || !strings.Contains(rendered, "expr_domain_c") || !strings.Contains(rendered, "POSIX") {
			t.Fatal("storage erased semantic domain/annotation definition", rendered, err)
		}
		sessionSQL(t, owner, `INSERT INTO expr_domain_storage(a,b) VALUES('A','A')`)
	})
}
