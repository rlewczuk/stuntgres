package dml

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqlstate"
)

func CopyRelationKind(relation *catalog.Relation, from bool) error {
	if !from && relation.RelationKind == catalog.MaterializedViewRelation && !relation.Populated {
		d := sqlError("0A000", fmt.Sprintf("cannot copy from unpopulated materialized view \"%s\"", relation.Name())).(*sqlstate.Error)
		d.Hint = "Use the REFRESH MATERIALIZED VIEW command."
		return d
	}
	if relation.RelationKind != catalog.TableRelation && !(from && (relation.RelationKind == catalog.PartitionedRelation || relation.RelationKind == catalog.ForeignRelation)) && !(!from && relation.RelationKind == catalog.MaterializedViewRelation) {
		kind := "non-table relation"
		switch relation.RelationKind {
		case catalog.ViewRelation:
			kind = "view"
		case catalog.PartitionedRelation:
			kind = "partitioned table"
		case catalog.MaterializedViewRelation:
			kind = "materialized view"
		case catalog.SequenceRelation:
			kind = "sequence"
		}
		direction := "from"
		if from {
			direction = "to"
		}
		d := sqlError("42809", fmt.Sprintf("cannot copy %s %s \"%s\"", direction, kind, relation.Name())).(*sqlstate.Error)
		if !from && (relation.RelationKind == catalog.ViewRelation || relation.RelationKind == catalog.PartitionedRelation) {
			d.Hint = "Try the COPY (SELECT ...) TO variant."
		}
		if from && relation.RelationKind == catalog.ViewRelation {
			d.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		}
		return d
	}
	return nil
}

// Physical input keeps its immediate codec/default/constraint events. Eligible
// COPY buffers then own a distinct insertion flush, unlike ordinary INSERT.
type CopyInsertionCursor interface {
	InsertionOrigin() (line int64, textBytes int)
}

type CopyInsertionBuffer struct {
	Group     *partition.InsertGroup
	Positions []int
}

type CopyInsertionState struct {
	Cursor       CopyInsertionCursor
	single       bool
	First, Bytes int
	Lines        []int64
	Buffers      []CopyInsertionBuffer
	Groups       map[*partition.InsertGroup]int
}

func NewCopyInsertionState(cursor CopyInsertionCursor, b analyze.Binding) *CopyInsertionState {
	state := &CopyInsertionState{Cursor: cursor}
	for _, expression := range b.Insert.Defaults {
		if exec.PlannedPhysicalVolatility(expression, true) == expr.Volatile {
			state.single = true
			break
		}
	}
	return state
}

func (state *CopyInsertionState) Full() bool {
	return state.single || len(state.Lines) >= 1000 || state.Bytes >= 65535
}

func CopyInsertionDiagnostic(err error, relation string, line int64) error {
	var record *sqlstate.Error
	if !errors.As(err, &record) {
		return err
	}
	result := *record
	result.Context = fmt.Sprintf("COPY %s, line %d", relation, line)
	if record.Context != "" {
		result.Context = record.Context + "\n" + result.Context
	}
	return &result
}

// CopyLineTargets is built once per statement from the target columns.
func CopyLineTargets(b analyze.Binding) []copyfmt.Target {
	targets := make([]copyfmt.Target, len(b.Targets))
	for i, target := range b.Targets {
		targets[i] = copyfmt.Target{Name: b.Relation.Columns[target].Name, HasDefault: len(b.Insert.Defaults) != 0 && b.Insert.Defaults[target] != nil}
	}
	return targets
}
