package engine

import (
	"errors"
	"fmt"
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The relation admission owner has already checked ownership and acquired AX.
// Only detached generation maps enter the shared ALTER candidate pipeline.
func (e *Engine) generationCandidate(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, int32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	name := action.Column.Text
	if catalog.IsSystemColumn(name) {
		return nil, 0, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, name))
	}
	slot, exists := old.Index[name]
	if !exists {
		return nil, 0, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, old.Name()))
	}
	attribute := old.Attributes[slot]
	definition, generated := old.ColumnMeta.Generations[attribute]
	if !generated {
		if action.Action == parser.DropExpression && action.IfExists {
			op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`column "%s" of relation "%s" is not a generated column, skipping`, name, old.Name())})
			return old, 0, nil
		}
		return nil, 0, sqlError("55000", fmt.Sprintf(`column "%s" of relation "%s" is not a generated column`, name, old.Name()))
	}
	if definition.Kind == catalog.GenerationVirtual {
		message := ""
		if action.Action == parser.DropExpression {
			message = "ALTER TABLE / DROP EXPRESSION is not supported for virtual generated columns"
		} else if len(old.Constraints.Check) != 0 {
			// PostgreSQL18.0 rejects even an unrelated CHECK; newer releases differ.
			message = "ALTER TABLE / SET EXPRESSION is not supported for virtual generated columns in tables with check constraints"
		}
		if message != "" {
			diagnostic := sqlError("0A000", message).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf(`Column "%s" of relation "%s" is a virtual generated column.`, name, old.Name())
			return nil, 0, diagnostic
		}
	}
	next := *old
	next.ColumnMeta.Generations = maps.Clone(old.ColumnMeta.Generations)
	if action.Action == parser.DropExpression {
		delete(next.ColumnMeta.Generations, attribute)
		return &next, 0, nil
	}
	if action.Generation == nil {
		return nil, 0, fmt.Errorf("stuntgres: missing replacement generation expression")
	}
	replacement, err := (alterHost{ddlHost{e: e, op: op}, scope}).BindGeneration(auth, &next, slot, action.Generation.Expression)
	if err != nil {
		// ALTER utility cooking has no semantic caret, including unsimplified
		// virtual security checks. CREATE and lexer syntax positions are unchanged.
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
		return nil, 0, err
	}
	next.ColumnMeta.Generations[attribute] = replacement
	return &next, attribute, nil
}
