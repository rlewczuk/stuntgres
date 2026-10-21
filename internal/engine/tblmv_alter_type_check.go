package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Original definitions are captured once after DROP, before any TYPE changes
// their usages. Queue order is TYPE discovery, then retained declaration order.
type alterTypeCheck struct {
	index    int
	sql      string
	literals []plan.LiteralExpr
}

func captureAlterTypeChecks(op *session.Operation, view catalog.Read, original, survivor *catalog.Relation, actions []parser.AlterTableAction) ([]alterTypeCheck, error) {
	if len(survivor.Constraints.Check) == 0 || !slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool { return a.Action == parser.AlterColumnType }) {
		return nil, nil
	}
	ordered := orderedChecks(survivor)
	var queue []alterTypeCheck
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		slot, exists := original.Index[action.Column.Text]
		if !exists {
			continue // The TYPE owner supplies the missing-column diagnostic.
		}
		attribute := original.Attributes[slot]
		for _, check := range ordered {
			affected := false
			for _, dependency := range check.Dependencies {
				if dependency.Ref.Address.ClassID == survivor.Address().ClassID && dependency.Ref.Address.ObjectID == survivor.Address().ObjectID && dependency.Ref.Address.SubID == attribute {
					affected = true
					break
				}
			}
			if !affected {
				continue
			}
			index := slices.IndexFunc(survivor.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Ordinal == check.Ordinal })
			if slices.ContainsFunc(queue, func(c alterTypeCheck) bool { return c.index == index }) {
				continue
			}
			var literals []plan.LiteralExpr
			canonical, err := (stored.ScalarRenderer{Definition: op.Definition, Owner: survivor, CheckLiterals: &literals}).SQL(op.Context(), view, check.Expression)
			if err != nil {
				return nil, err
			}
			queue = append(queue, alterTypeCheck{index: index, sql: canonical, literals: literals})
		}
	}
	return queue, nil
}

func alterTypeCheckReparseError(err error) error {
	if diagnostic, ok := err.(*sqlstate.Error); ok {
		copy := *diagnostic
		copy.Position, copy.InternalPosition, copy.InternalQuery = "", "", ""
		return &copy
	}
	return err
}

// One completed-row validator is shared by TYPE rewriting and metadata-only NN
// scans. The CHECK traversal descriptor is ephemeral; storage stays alphabetic.
type alterRowValidator struct {
	relation *catalog.Relation
	checks   *catalog.Relation
	schema   string
	notNull  []int
}

func (v alterRowValidator) validate(row []sqltype.Value, evaluation *exec.Context) error {
	for _, slot := range v.notNull {
		column := v.relation.Columns[slot]
		value, err := logicalColumnValue(v.relation, slot, row, evaluation)
		if err != nil {
			return err
		}
		if value.IsNull() {
			diagnostic := sqlError("23502", fmt.Sprintf(`column "%s" of relation "%s" contains null values`, column.Name, v.relation.Name())).(*sqlstate.Error)
			diagnostic.Schema, diagnostic.Table, diagnostic.Column = v.schema, v.relation.Name(), column.Name
			return diagnostic
		}
	}
	if v.checks == nil {
		return nil
	}
	failed, err := validateCheckRow(v.checks, row, evaluation)
	if err != nil || failed == nil {
		return err
	}
	diagnostic := sqlError("23514", fmt.Sprintf(`check constraint "%s" of relation "%s" is violated by some row`, failed.Name, v.relation.Name())).(*sqlstate.Error)
	diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = v.schema, v.relation.Name(), failed.Name
	return diagnostic
}

func newAlterRowValidator(relation *catalog.Relation, schema string) alterRowValidator {
	v := alterRowValidator{relation: relation, schema: schema}
	for slot, column := range relation.Columns {
		if column.Nullable {
			continue
		}
		invalid := false
		for _, n := range relation.Constraints.NotNull {
			if n.Attribute == relation.Attributes[slot] {
				invalid = n.NotValid
				break
			}
		}
		if !invalid {
			v.notNull = append(v.notNull, slot)
		}
	}
	return v
}

func needsNotNullScan(original, next *catalog.Relation) bool {
	for _, n := range next.Constraints.NotNull {
		if n.NotValid {
			continue
		}
		valid := false
		for _, old := range original.Constraints.NotNull {
			if old.Attribute == n.Attribute {
				valid = !old.NotValid
				break
			}
		}
		if !valid {
			return true
		}
	}
	return false
}
