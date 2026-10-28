package stored

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// TriggerPresentation is immutable presentation, not an executable recipe.
type TriggerPresentation struct {
	OID                    ids.OID
	Name                   string
	Insert, Update, Delete bool
	Condition, Statement   string
	HasCondition           bool
	Timing, Orientation    string
}

// VisitTriggerDefinitions validates real definitions against the captured root.
// Rendering never invokes the routine or evaluates its WHEN expression.
func VisitTriggerDefinitions(ctx context.Context, read catalog.Read, relation *catalog.Relation, presentation Presentation, disclose bool, visit func(TriggerPresentation) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relation == nil || read.Get(relation.Address()) != relation {
		return ids.ErrCatalogReplaced
	}
	if relation.Triggers == nil {
		return nil
	}
	definitions, ok := relation.Triggers.(TriggerList)
	if !ok {
		return fmt.Errorf("stuntgres: invalid trigger payload")
	}
	if err := definitions.Validate(read, relation); err != nil {
		return err
	}
	renderer := Deparser{View: read, Path: presentation.Path, Output: presentation.Output, NonstandardStrings: presentation.NonstandardStrings}
	renderer.ColumnSQL = func(column plan.ColumnExpr) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if column.Scope != analyze.RulePseudoScope || column.Frame != 1 && column.Frame != 2 || column.Origin.Address.ObjectID != relation.Address().ObjectID || !ReferenceEpochValid(read, relation, column.Origin) {
			return "", fmt.Errorf("stuntgres: invalid trigger WHEN column")
		}
		side := "old"
		if column.Frame == 2 {
			side = "new"
		}
		for i, attribute := range relation.Attributes {
			if attribute == column.Origin.Address.SubID {
				return renderer.Identifier(side) + "." + renderer.Identifier(relation.Columns[i].Name), nil
			}
		}
		return "", fmt.Errorf("stuntgres: missing trigger WHEN attribute")
	}
	for _, definition := range definitions {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := definition.parts
		function, ok := read.Procedure(p.Procedure.Address)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		name, err := renderer.sqlRoutineName(function)
		if err != nil {
			return err
		}
		var statement strings.Builder
		statement.WriteString("EXECUTE FUNCTION ")
		statement.WriteString(name)
		statement.WriteByte('(')
		for i, arg := range p.Arguments {
			if err := ctx.Err(); err != nil {
				return err
			}
			if i != 0 {
				statement.WriteString(", ")
			}
			// PostgreSQL 18 simple_quote_literal doubles backslashes only
			// with standard_conforming_strings off, without an E prefix.
			statement.WriteByte('\'')
			for _, ch := range arg {
				if ch == '\'' || ch == '\\' && presentation.NonstandardStrings {
					statement.WriteRune(ch)
				}
				statement.WriteRune(ch)
			}
			statement.WriteByte('\'')
		}
		statement.WriteByte(')')
		fact := TriggerPresentation{OID: p.OID, Name: p.Name, Insert: p.Events&TriggerInsert != 0, Update: p.Events&TriggerUpdate != 0, Delete: p.Events&TriggerDelete != 0, Statement: statement.String()}
		fact.Timing, err = triggerTimingSQL(p.Timing)
		if err != nil {
			return err
		}
		fact.Orientation = "ROW"
		if p.Level == TriggerStatement {
			fact.Orientation = "STATEMENT"
		}
		if disclose && p.When != nil {
			fact.Condition, err = renderer.SQL(ctx, p.When, DeparseParent{})
			if err != nil {
				return err
			}
			fact.HasCondition = true
		}
		if err := visit(fact); err != nil {
			return err
		}
	}
	return ctx.Err()
}
