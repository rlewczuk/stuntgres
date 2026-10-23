package plan

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// RoutineInput maps one declared input slot to a child or a contiguous expanded
// variadic tail. Children remain in written order, followed by hidden defaults.
type RoutineInput struct {
	Start, Count int
	Expanded     bool
}

// AssembleArguments closes a selected call shape once, before publication.
// Execution and stored refresh validate this projection rather than rebuilding
// it or falling back to declaration order.
func (n SQLRoutineExpr) AssembleArguments() (SQLRoutineExpr, error) {
	n.ChildSlots = make([]int, len(n.Args))
	for i := range n.ChildSlots {
		n.ChildSlots[i] = -1
	}
	for slot, source := range n.InputMap {
		if source.Start < 0 || source.Count < 1 || source.Start > len(n.Args)-source.Count {
			return SQLRoutineExpr{}, fmt.Errorf("invalid SQL routine input source")
		}
		for i := source.Start; i < source.Start+source.Count; i++ {
			if n.ChildSlots[i] != -1 {
				return SQLRoutineExpr{}, fmt.Errorf("duplicate SQL routine child consumption")
			}
			n.ChildSlots[i] = slot
		}
	}
	if err := n.ValidateArguments(n.Args); err != nil {
		return SQLRoutineExpr{}, err
	}
	return n, nil
}

func (n SQLRoutineExpr) ValidateArguments(children []expr.Expr) error {
	if n.Procedure == nil || n.Procedure.SQL == nil || n.WrittenCount < 0 || n.WrittenCount > len(children) || len(n.InputMap) != len(n.Procedure.SQL.Inputs) || len(n.ArgNames) != 0 && len(n.ArgNames) != n.WrittenCount {
		return fmt.Errorf("invalid SQL routine input assembly")
	}
	if len(n.ChildSlots) != len(children) {
		return fmt.Errorf("invalid SQL routine child projection")
	}
	for slot, source := range n.InputMap {
		if source.Start < 0 || source.Count < 1 || source.Start > len(children)-source.Count || !source.Expanded && source.Count != 1 {
			return fmt.Errorf("invalid SQL routine input source")
		}
		usage := n.Procedure.SQL.Inputs[slot]
		if source.Expanded {
			if slot != len(n.InputMap)-1 || !usage.Type.IsArray() || source.Start+source.Count > n.WrittenCount || n.Variadic {
				return fmt.Errorf("invalid SQL routine variadic source")
			}
			if len(n.Procedure.SQL.InputFormals) != len(n.InputMap) || n.Procedure.SQL.Formals[n.Procedure.SQL.InputFormals[slot]].Mode != "v" {
				return fmt.Errorf("invalid SQL routine variadic formal")
			}
			usage = usage.Type.Element().Usage()
		} else if source.Start >= n.WrittenCount {
			if n.Procedure.SQLDefaults == nil {
				return fmt.Errorf("missing SQL routine default payload")
			}
			if _, ok := n.Procedure.SQLDefaults.Default(slot); !ok {
				return fmt.Errorf("missing SQL routine default source")
			}
		}
		for index := source.Start; index < source.Start+source.Count; index++ {
			if n.ChildSlots[index] != slot {
				return fmt.Errorf("duplicate SQL routine child consumption")
			}
			child := children[index]
			if child == nil {
				return fmt.Errorf("invalid SQL routine argument usage")
			}
			actual := child.ResultUsage()
			expected := usage
			if usage.Collation.ID != 0 && actual.Collation.ID != 0 && actual.Collation.Valid() {
				expected.Collation = actual.Collation
			}
			if actual != expected {
				return fmt.Errorf("invalid SQL routine argument usage")
			}
		}
	}
	for child := range children {
		if n.ChildSlots[child] < 0 || n.ChildSlots[child] >= len(n.InputMap) {
			return fmt.Errorf("invalid SQL routine child slot")
		}
		source := n.InputMap[n.ChildSlots[child]]
		if child < source.Start || child >= source.Start+source.Count {
			return fmt.Errorf("inconsistent SQL routine child slot")
		}
	}
	return nil
}

func (SQLRoutineExpr) SourceAdmission() (bool, bool) { return true, false }

func (n SQLRoutineExpr) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}

func (n SQLRoutineExpr) ScalarSourceOutName() string { return n.Procedure.SQL.OutputName() }

func (n SQLRoutineExpr) SameArguments(o SQLRoutineExpr) bool {
	return n.WrittenCount == o.WrittenCount && n.Variadic == o.Variadic && slices.Equal(n.InputMap, o.InputMap) && slices.Equal(n.ChildSlots, o.ChildSlots) && slices.EqualFunc(n.ArgNames, o.ArgNames, func(a, b parser.Identifier) bool { return a.Text == b.Text })
}
