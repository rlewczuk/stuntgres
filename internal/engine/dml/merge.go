package dml

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

func MergeOnTruth(value sqltype.Value) (plan.Truth, error) {
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

func MergeHasIdentityReads(merge *analyze.MergeBinding) bool {
	if exec.QueryHasIdentityReads(merge.Source) || exec.BoundHasIdentityReads(merge.On) {
		return true
	}
	for _, arm := range merge.Arms {
		if exec.BoundHasIdentityReads(arm.Condition) {
			return true
		}
		if arm.Input.Update != nil {
			for _, expr := range arm.Input.Update.Assignments {
				if exec.BoundHasIdentityReads(expr) {
					return true
				}
			}
			for _, target := range arm.Input.Update.ArrayTargets {
				if exec.AssignmentTargetHasIdentityReads(target) {
					return true
				}
			}
		}
		for _, row := range arm.Input.Expressions {
			for _, expr := range row {
				if exec.BoundHasIdentityReads(expr) {
					return true
				}
			}
		}
	}
	return false
}

// DELETE carries captured identity only; payload indexes name INSERT/UPDATE
// rows. All records remain private until the sole statement Finish.
type MergeActionRow struct {
	Action     parser.MergeAction
	Payload    int
	Deletion   visibility.DeleteCandidate
	UpdateMode lock.MultiMode
}

type MergeRows struct {
	index.WriteBatch
	Predecessors  []storage.RowVersion
	Actions       []MergeActionRow
	Returning     *exec.FinalRows
	StagedActions int // Accepted prefix already visible to later BEFORE invocations.
}

type MergeCandidate struct {
	UpdateCandidate
	Class    parser.MergeMatch
	Deletion visibility.DeleteCandidate
}

func MergeCardinalityError() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "MERGE command cannot affect row a second time", Hint: "Ensure that not more than one source row matches any one target row."}
}

func MergeMatches(evaluation *exec.Context, on expr.Expr) (bool, error) {
	value, err := on.Eval(evaluation)
	if err != nil {
		return false, err
	}
	truth, err := MergeOnTruth(value)
	return truth == plan.True, err
}

func SelectMergeArm(merge *analyze.MergeBinding, evaluation *exec.Context, class parser.MergeMatch) (*analyze.MergeArm, error) {
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

// MergeFollowStrong reports whether a MERGE follows an update chain with a
// FOR UPDATE row lock: it has a DELETE arm, or an UPDATE arm assigns one of
// the unique key slots.
func MergeFollowStrong(m *analyze.MergeBinding, keySlots []int) bool {
	if m.HasDelete {
		return true
	}
	for _, key := range keySlots {
		for _, target := range m.UpdateTargets {
			if key == target {
				return true
			}
		}
	}
	return false
}
