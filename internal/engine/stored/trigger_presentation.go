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
	renderer = triggerWhenRenderer(ctx, relation, renderer)
	for _, definition := range definitions {
		if err := ctx.Err(); err != nil {
			return err
		}
		fact, err := triggerPresentation(ctx, definition.parts, renderer, disclose)
		if err != nil {
			return err
		}
		if err := visit(fact); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func triggerWhenRenderer(ctx context.Context, relation *catalog.Relation, renderer Deparser) Deparser {
	renderer.ColumnSQL = func(column plan.ColumnExpr) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if column.Scope != analyze.RulePseudoScope || column.Frame != 1 && column.Frame != 2 || column.Origin.Address.ObjectID != relation.Address().ObjectID || !ReferenceEpochValid(renderer.View, relation, column.Origin) {
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
	return renderer
}

func triggerPresentation(ctx context.Context, p TriggerParts, renderer Deparser, disclose bool) (TriggerPresentation, error) {
	if err := ctx.Err(); err != nil {
		return TriggerPresentation{}, err
	}
	function, ok := renderer.View.Procedure(p.Procedure.Address)
	if !ok {
		return TriggerPresentation{}, ids.ErrCatalogReplaced
	}
	name, err := renderer.sqlRoutineName(function)
	if err != nil {
		return TriggerPresentation{}, err
	}
	var statement strings.Builder
	statement.WriteString("EXECUTE FUNCTION ")
	statement.WriteString(name)
	statement.WriteByte('(')
	for i, arg := range p.Arguments {
		if err := ctx.Err(); err != nil {
			return TriggerPresentation{}, err
		}
		if i != 0 {
			statement.WriteString(", ")
		}
		// Match simple_quote_literal, without an E prefix.
		statement.WriteByte('\'')
		for _, ch := range arg {
			if ch == '\'' || ch == '\\' && renderer.NonstandardStrings {
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
		return TriggerPresentation{}, err
	}
	fact.Orientation = "ROW"
	if p.Level == TriggerStatement {
		fact.Orientation = "STATEMENT"
	}
	if disclose && p.When != nil {
		fact.Condition, err = renderer.SQL(ctx, p.When, DeparseParent{})
		if err != nil {
			return TriggerPresentation{}, err
		}
		fact.HasCondition = true
	}
	return fact, ctx.Err()
}

// TriggerDefinitionSQL renders a registered user trigger or partition clone.
// Enable modes and clone replay annotations belong only to native serialization.
func TriggerDefinitionSQL(ctx context.Context, relation *catalog.Relation, definition Trigger, renderer Deparser) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if relation == nil || renderer.View.Get(relation.Address()) != relation {
		return "", ids.ErrCatalogReplaced
	}
	list, ok := relation.Triggers.(TriggerList)
	if !ok {
		return "", fmt.Errorf("stuntgres: invalid trigger payload")
	}
	if err := list.Validate(renderer.View, relation); err != nil {
		return "", err
	}
	renderer = triggerWhenRenderer(ctx, relation, renderer)
	p := definition.parts
	fact, err := triggerPresentation(ctx, p, renderer, true)
	if err != nil {
		return "", err
	}
	target, err := renderer.RelationName(relation, !renderer.Pretty)
	if err != nil {
		return "", err
	}
	var sql strings.Builder
	sql.WriteString("CREATE TRIGGER " + renderer.Identifier(p.Name) + " " + fact.Timing + " ")
	first := true
	for _, event := range [...]struct {
		bit  TriggerEvent
		name string
	}{{TriggerInsert, "INSERT"}, {TriggerDelete, "DELETE"}, {TriggerUpdate, "UPDATE"}, {TriggerTruncate, "TRUNCATE"}} {
		if p.Events&event.bit == 0 {
			continue
		}
		if !first {
			sql.WriteString(" OR ")
		}
		first = false
		sql.WriteString(event.name)
		if event.bit == TriggerUpdate && len(p.UpdateColumns) != 0 {
			sql.WriteString(" OF ")
			for i, attribute := range p.UpdateColumns {
				if i != 0 {
					sql.WriteString(", ")
				}
				name, err := renderer.Attribute(relation, attribute)
				if err != nil {
					return "", err
				}
				sql.WriteString(name)
			}
		}
	}
	sql.WriteString(" ON " + target + " FOR EACH " + fact.Orientation)
	if fact.HasCondition {
		sql.WriteString(" WHEN (" + fact.Condition + ")")
	}
	sql.WriteString(" " + fact.Statement)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return sql.String(), nil
}
