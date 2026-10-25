package core_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This observer delegates to the real captured column evaluator. It records
// actual residual demand; it does not supply rows, perform pruning or echo an
// expected selection. It is deliberately an unsupported proof conjunct.
// Its placement is a native executor unit contract, not a PG evaluation-order
// claim. The original analyzed key predicate remains independently visible.
type pruningDemandWitness struct {
	expr.Info
	key       expr.Expr
	demand    *map[int64]int
	rowOwners map[*sqltype.Value]uint32
	physical  *map[uint32]int
}

func (w pruningDemandWitness) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := w.key.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := ctx.(*exec.Context)
	if !ok || len(runtime.Row) != 1 {
		return sqltype.Value{}, fmt.Errorf("pruning witness requires the captured one-column row frame")
	}
	oid, ok := w.rowOwners[&runtime.Row[0]]
	if !ok {
		return sqltype.Value{}, fmt.Errorf("pruning witness received an uncaptured row buffer")
	}
	(*w.physical)[oid]++
	if !v.IsNull() {
		k, err := v.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		(*w.demand)[k]++
	}
	return sqltype.Boolean(w.Usage.Type, true), nil
}

func TestPartitionPruningExecution(t *testing.T) {
	for _, c := range []struct {
		name, strategy, first, second, predicate string
		wanted                                   map[int64]int
		count                                    int
	}{
		{"range", "RANGE(k)", "FROM(1) TO(3)", "FROM(3) TO(5)", "k=1", map[int64]int{1: 2, 2: 1}, 2},
		{"list", "LIST(k)", "IN(1,2)", "IN(3,4)", "k=1", map[int64]int{1: 2, 2: 1}, 2},
		{"hash", "HASH(k)", "WITH(MODULUS 4,REMAINDER 0)", "WITH(MODULUS 4,REMAINDER 2)", "k=1", map[int64]int{1: 2}, 2},
		{"empty", "RANGE(k)", "FROM(1) TO(3)", "FROM(3) TO(5)", "k=99", map[int64]int{}, 0},
		{"inheritance", "", "", "", "k=1", map[int64]int{1: 2, 2: 1}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := engine.New()
			if c.name == "inheritance" {
				for _, q := range []string{
					"CREATE TABLE pp(k INT)",
					"CREATE TABLE pp_a(CHECK(k>=1 AND k<3)) INHERITS(pp)",
					"CREATE TABLE pp_b(CHECK(k>=3 AND k<5)) INHERITS(pp)",
					"INSERT INTO pp_a VALUES(1),(1),(2)",
					"INSERT INTO pp_b VALUES(3),(4)",
				} {
					enginetest.Run(t, e, q)
				}
			} else {
				for _, q := range []string{
					"CREATE TABLE pp(k INT) PARTITION BY " + c.strategy,
					"CREATE TABLE pp_a PARTITION OF pp FOR VALUES " + c.first,
					"CREATE TABLE pp_b PARTITION OF pp FOR VALUES " + c.second,
				} {
					enginetest.Run(t, e, q)
				}
			}
			input := "INSERT INTO pp VALUES(1),(1),(2),(3),(4)"
			if c.name == "hash" {
				input = "INSERT INTO pp VALUES(1),(1),(2)"
			}
			if c.name != "inheritance" {
				enginetest.Run(t, e, input)
			}
			query, source, release := hashImplicitQuery(t, e, "SELECT k FROM pp WHERE "+c.predicate)
			defer release()
			project := query.Root.(*plan.ProjectNode)
			filter := project.Child.(*plan.FilterNode)
			comparison := filter.Predicate.(plan.ComparisonExpr)
			demand := map[int64]int{}
			physical, expectedPhysical := map[uint32]int{}, map[uint32]int{}
			for _, segment := range source.Inheritance {
				if segment.Leaf.Relation.Name() == "pp_a" && c.name != "empty" {
					count := 0
					for _, n := range c.wanted {
						count += n
					}
					expectedPhysical[uint32(segment.Leaf.Ref.Address.ObjectID)] = count
				}
			}
			if c.name != "empty" && len(expectedPhysical) != 1 {
				t.Fatal("missing captured physical pp_a")
			}
			// These fixtures have identity one-column layouts. Associate each
			// actual borrowed captured row buffer with its physical descriptor;
			// duplicate values retain distinct captured row-version buffers.
			// The runtime observer identifies the demanded buffer, not its key.
			rowOwners := make(map[*sqltype.Value]uint32)
			for i := range source.Count() {
				row := source.Row(i)
				if len(row) != 1 {
					t.Fatal("unexpected captured layout", len(row))
				}
				cell := &row[0]
				if _, exists := rowOwners[cell]; exists {
					t.Fatal("fixture does not have distinct captured row buffers")
				}
				rowOwners[cell] = uint32(source.PhysicalRef(i).Address.ObjectID)
			}
			witness := pruningDemandWitness{Info: expr.InfoOf(filter.Predicate), key: comparison.Left, demand: &demand, rowOwners: rowOwners, physical: &physical}
			filter.Predicate = plan.LogicalExpr{Info: expr.InfoOf(filter.Predicate), Op: parser.LogicalAnd, Left: witness, Right: filter.Predicate}
			state := &exec.State{Source: source, Frame: project.Frame}
			if scope := core.ScopeOf(source.LiveHost()); scope != nil {
				state.Input, state.Output = scope.Input, scope.Output
			}
			stage, err := exec.OpenNode(context.Background(), state, filter)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if err := stage.Drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			if stage.Count() != c.count {
				t.Fatalf("residual bag count=%d want %d", stage.Count(), c.count)
			}
			if !reflect.DeepEqual(physical, expectedPhysical) {
				t.Fatalf("irrelevant physical branch was demanded: physical demand=%v want %v; key demand=%v", physical, expectedPhysical, demand)
			}
			if !reflect.DeepEqual(demand, c.wanted) {
				t.Fatalf("irrelevant branch was demanded: actual residual demand=%v want %v", demand, c.wanted)
			}
		})
	}
}
