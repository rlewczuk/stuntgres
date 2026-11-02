package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// statementCallJumble is an immutable selected projection of erased OUT actuals.
// It cannot retain a descriptor, expression graph or executable/fold authority.
type statementCallJumble struct {
	hash       uint64
	locations  []int
	lists      [][2]int
	parameters []int
	highest    int
}

func (j *StatementJumble) binding(statement parser.Statement, b *Binding) {
	_, selecting := statement.(*parser.Select)
	if b.Relation != nil && !selecting && statement != nil {
		name := b.Relation.Name()
		var alias parser.Identifier
		switch s := statement.(type) {
		case *parser.Insert:
			alias = s.Alias
		case *parser.Update:
			alias = s.Alias
		case *parser.Delete:
			alias = s.Alias
		case *parser.Merge:
			alias = s.Alias
		}
		if alias.Text != "" {
			name = alias.Text
		}
		j.text(name)
	}
	j.number(uint64(len(b.Targets)))
	for _, target := range b.Targets {
		j.number(uint64(target + 1))
	}
	if b.Query != nil {
		j.query(b.Query.Root)
	}
	for _, row := range b.Expressions {
		j.expressions(row)
	}
	if _, calling := statement.(*parser.Call); calling && b.callOutputJumble != nil {
		output := b.callOutputJumble
		j.number(output.hash)
		j.Locations = append(j.Locations, output.locations...)
		j.Lists = append(j.Lists, output.lists...)
		j.parameterLocations = append(j.parameterLocations, output.parameters...)
		j.HighestParameter = max(j.HighestParameter, output.highest)
	}
	j.expression(b.Predicate)
	j.update(b.Update)
	if b.Delete.Using != nil {
		j.query(b.Delete.Using)
	}
	if b.CurrentOf != "" {
		j.text(b.CurrentOf)
	}
	if b.Returning != nil {
		j.expressions(b.Returning.Expressions)
	}
	if c := b.Insert.Conflict; c != nil {
		j.text("OnConflictExpr")
		j.number(uint64(c.Constraint.ObjectID))
		j.flag(c.Update != nil)
		for _, key := range c.Keys {
			j.number(uint64(key.Attribute))
			j.number(uint64(key.Collation))
			j.expression(key.Expression)
		}
		j.expression(c.Predicate)
		j.update(c.Update)
		for _, target := range c.UpdateTargets {
			j.number(uint64(target + 1))
		}
	}
	if m := b.Merge; m != nil {
		j.query(m.Source)
		j.expression(m.On)
		for i := range m.Arms {
			arm := &m.Arms[i]
			j.number(uint64(arm.Match))
			j.number(uint64(arm.Action))
			j.expression(arm.Condition)
			j.binding(nil, &arm.Input)
		}
	}
	for _, cte := range b.CTEs {
		j.text("CommonTableExpr")
		j.text(cte.Name)
		j.number(uint64(cte.Hint))
		j.flag(cte.Recursive)
		if mutation, ok := MutationBinding(cte); ok {
			j.binding(cte.Statement, mutation)
		} else if cte.Query != nil {
			j.query(cte.Query.Root)
		}
	}
}
func (j *StatementJumble) update(update *UpdateBinding) {
	if update == nil {
		return
	}
	j.expressions(update.Assignments)
	j.expression(update.Predicate)
	j.query(update.From)
}
func (j *StatementJumble) expressions(nodes []expr.Expr) {
	j.number(uint64(len(nodes)))
	for _, node := range nodes {
		j.expression(node)
	}
}
func (j *StatementJumble) column(n plan.ColumnExpr) {
	j.number(uint64(n.QueryOccurrence))
	j.number(uint64(n.Origin.Address.SubID))
	j.number(j.outerDepth(n.Frame))
}
func (j *StatementJumble) outerDepth(frame uint32) uint64 {
	for i := len(j.frames) - 1; i >= 0; i-- {
		if j.frames[i] == frame {
			return uint64(len(j.frames) - 1 - i)
		}
	}
	return 0
}
