package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func mergeOnTruth(value sqltype.Value) (plan.Truth, error) {
	if value.Type().IsDomain() {
		var err error
		value, err = value.BaseValue()
		if err != nil {
			return plan.False, err
		}
	}
	if value.IsNull() {
		return plan.Unknown, nil
	}
	switch value.Type().ID() {
	case sqltype.Bool:
		return plan.TruthOf(value)
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		i, err := value.Integer()
		if err != nil {
			return plan.False, err
		}
		if i != 0 {
			return plan.True, nil
		}
		return plan.False, nil
	case sqltype.Text:
		return plan.True, nil
	}
	return plan.False, fmt.Errorf("stuntgres: invalid MERGE ON carrier")
}

func mergeHasVirtualReads(merge *analyze.MergeBinding) bool {
	if exec.QueryHasVirtualReads(merge.Source) || exec.BoundHasVirtualReads(merge.On) {
		return true
	}
	for _, arm := range merge.Arms {
		if exec.BoundHasVirtualReads(arm.Condition) {
			return true
		}
		if arm.Input.Update != nil {
			for _, expr := range arm.Input.Update.Assignments {
				if exec.BoundHasVirtualReads(expr) {
					return true
				}
			}
			for _, target := range arm.Input.Update.ArrayTargets {
				if exec.AssignmentTargetHasVirtualReads(target) {
					return true
				}
			}
		}
		for _, row := range arm.Input.Expressions {
			for _, expr := range row {
				if exec.BoundHasVirtualReads(expr) {
					return true
				}
			}
		}
	}
	return false
}

// DELETE carries captured identity only; payload indexes name INSERT/UPDATE
// rows. All records remain private until the sole statement Finish.
type mergeActionRow struct {
	action     parser.MergeAction
	payload    int
	deletion   visibility.DeleteCandidate
	updateMode lock.MultiMode
}

type mergeRows struct {
	index.WriteBatch
	predecessors []storage.RowVersion
	actions      []mergeActionRow
	returning    *exec.FinalRows
}

type mergeCandidate struct {
	updateCandidate
	class    parser.MergeMatch
	deletion visibility.DeleteCandidate
}

func mergeCardinalityError() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "MERGE command cannot affect row a second time", Hint: "Ensure that not more than one source row matches any one target row."}
}

func mergeMatches(evaluation *exec.Context, on expr.Expr) (bool, error) {
	value, err := on.Eval(evaluation)
	if err != nil {
		return false, err
	}
	truth, err := mergeOnTruth(value)
	return truth == plan.True, err
}

func selectMergeArm(merge *analyze.MergeBinding, evaluation *exec.Context, class parser.MergeMatch) (*analyze.MergeArm, error) {
	for i := range merge.Arms {
		arm := &merge.Arms[i]
		if arm.Match != class {
			continue
		}
		selected, err := exec.UpdateMatches(evaluation, arm.Condition)
		if err != nil {
			return nil, err
		}
		if selected {
			return arm, nil
		}
	}
	return nil, nil
}
