package analyze

import (
	"fmt"

	"stuntgres/internal/parser"
)

// JumbleUtility projects the released raw utility fields. Every statement root
// is explicitly classified; no unknown root falls back to a descriptor, SQL
// formatter or arbitrary runtime graph. Ordinary analyzed queries use their
// separate pre-fold typed visitor.
func JumbleUtility(statement parser.Statement) (StatementJumble, int) {
	return jumbleUtility(statement, nil)
}

// JumbleUtilityBound consumes the genuine accepted analysis of the one child
// which REL_18_6 transforms before jumbling CTAS, DECLARE, EXPLAIN or CALL.
func JumbleUtilityBound(statement parser.Statement, binding *Binding) (StatementJumble, int) {
	return jumbleUtility(statement, binding)
}

func utilityAnalyzedChild(statement parser.Statement) parser.Statement {
	switch s := statement.(type) {
	case *parser.CreateQueryTable:
		return s.Query
	case *parser.DeclareCursor:
		return s.Query
	case *parser.Call:
		return s
	case *parser.Explain:
		switch child := s.Statement.(type) {
		case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
			return child
		default:
			return utilityAnalyzedChild(s.Statement)
		}
	}
	return nil
}

func jumbleUtility(statement parser.Statement, binding *Binding) (StatementJumble, int) {
	var frames [16]uint32
	var parameters [16]int
	j := StatementJumble{hash: 14695981039346656037, frames: frames[:0], parameterLocations: parameters[:0]}
	if binding != nil {
		j.utilityChild = utilityAnalyzedChild(statement)
		j.utilityBinding = binding
	}
	anchor := 0
	j.utilitySyntax(statement, &anchor)
	j.finish()
	return j, anchor
}
func (j *StatementJumble) utilityLocation(position parser.Position) {
	if position.Character > 0 {
		j.UtilityLocations = append(j.UtilityLocations, position.Offset)
	}
}
func (j *StatementJumble) variableSet(kind uint64, name string, local bool) {
	j.text("VariableSetStmt")
	j.number(kind)
	j.text(name)
	j.flag(local)
}
func (j *StatementJumble) utilitySpecial(node any, anchor *int) bool {
	position := func(p parser.Position) {
		if p.Character > 0 && (*anchor == 0 || p.Offset < *anchor) {
			*anchor = p.Offset
		}
	}
	switch s := node.(type) {
	case *parser.DeallocateSQL:
		if s == nil {
			j.number(0)
			return true
		}
		j.text("DeallocateStmt")
		j.flag(s.All)
		position(s.Pos)
		if !s.All {
			j.utilityLocation(s.Name.Pos)
		}
	case *parser.TransactionControl:
		if s == nil {
			j.number(0)
			return true
		}
		j.text("TransactionStmt")
		j.text("control")
		j.number(uint64(s.Kind))
		j.flag(s.StartKeyword)
		j.flag(s.Chain)
		position(s.Pos)
		utilitySlice(j, s.Modes, anchor)
		j.utilityLocation(s.GIDPosition)
	case *parser.SavepointControl:
		if s == nil {
			j.number(0)
			return true
		}
		j.text("TransactionStmt")
		j.text("savepoint")
		j.number(uint64(s.Kind))
		position(s.Pos)
		j.utilityLocation(s.Name.Pos)
	case *parser.Set:
		if s == nil {
			j.number(0)
			return true
		}
		kind := uint64(0)
		if s.Default {
			kind = 1
		} else if s.FromCurrent {
			kind = 2
		}
		j.variableSet(kind, s.Name.Text, s.Local)
		position(s.Pos)
		if s.JumbleArgs {
			j.number(uint64(len(s.Values)))
			for _, value := range s.Values {
				j.text(value)
			}
			if s.Interval != nil {
				j.text(s.Interval.Text)
				j.number(uint64(s.Interval.Precision))
				j.text(s.Interval.Qualifier)
			}
		} else if kind == 0 {
			j.utilityLocation(s.ValuePosition)
		}
	case *parser.Reset:
		if s == nil {
			j.number(0)
			return true
		}
		kind := uint64(4)
		if s.All {
			kind = 5
		}
		j.variableSet(kind, s.Name.Text, false)
		position(s.Pos)
	case *parser.SessionAuthorization:
		if s == nil {
			j.number(0)
			return true
		}
		kind := uint64(0)
		if s.ResetCommand {
			kind = 4
		} else if s.Default {
			kind = 1
		}
		j.variableSet(kind, "session_authorization", false)
		position(s.Pos)
		if kind == 0 {
			j.utilityLocation(s.ValuePosition)
		}
	case *parser.RoleSelection:
		if s == nil {
			j.number(0)
			return true
		}
		kind := uint64(0)
		if s.ResetCommand {
			kind = 4
		} else if s.Reset {
			kind = 1
		}
		j.variableSet(kind, "role", s.Local)
		position(s.Pos)
		if kind == 0 {
			j.utilityLocation(s.ValuePosition)
		}
	case *parser.TransactionCharacteristics:
		if s == nil {
			j.number(0)
			return true
		}
		name := "TRANSACTION"
		if s.Session {
			name = "SESSION CHARACTERISTICS"
		}
		if s.Snapshot != nil {
			name = "TRANSACTION SNAPSHOT"
		}
		j.variableSet(3, name, false)
		position(s.Pos)
		if s.Snapshot != nil {
			j.utilityLocation(s.SnapshotPos)
		} else {
			utilitySlice(j, s.Modes, anchor)
		}
	default:
		return false
	}
	return true
}
func (j *StatementJumble) utilityTypeName(raw parser.TypeName, anchor *int) {
	t := parser.UtilityTypeName(raw)
	j.text("TypeName")
	switch {
	case t.System:
		j.number(2)
		j.text("pg_catalog")
		j.text(t.Name)
	case len(t.Parts) > 0:
		j.number(uint64(len(t.Parts)))
		for _, part := range t.Parts {
			j.text(part.Text)
		}
	case t.Schema != nil:
		j.number(2)
		j.text(t.Schema.Text)
		j.text(t.Name)
	default:
		j.number(1)
		j.text(t.Name)
	}
	j.flag(t.Setof)
	j.flag(t.PctType)
	utilitySlice(j, t.Modifiers, anchor)
	utilitySlice(j, t.ArrayBounds, anchor)
}
func (j *StatementJumble) utilityPosition(p parser.Position, anchor *int) {
	if p.Character > 0 && (*anchor == 0 || p.Offset < *anchor) {
		*anchor = p.Offset
	}
}

func (j *StatementJumble) utilitySyntax(node any, anchor *int) {
	if j.utilityChild != nil {
		if child, ok := node.(parser.Statement); ok && child == j.utilityChild {
			j.text("Query")
			switch s := child.(type) {
			case *parser.Select:
				j.text("*parser.Select")
				j.utilityPosition(s.Pos, anchor)
			case *parser.Insert:
				j.text("*parser.Insert")
				j.utilityPosition(s.Pos, anchor)
			case *parser.Update:
				j.text("*parser.Update")
				j.utilityPosition(s.Pos, anchor)
			case *parser.Delete:
				j.text("*parser.Delete")
				j.utilityPosition(s.Pos, anchor)
			case *parser.Merge:
				j.text("*parser.Merge")
				j.utilityPosition(s.Pos, anchor)
			case *parser.Call:
				j.text("*parser.Call")
				j.utilityPosition(s.Pos, anchor)
			default:
				panic(fmt.Sprintf("stuntgres: unclassified analyzed utility child %T", child))
			}
			j.binding(child, j.utilityBinding)
			return
		}
	}
	if j.utilitySpecial(node, anchor) || j.utilityStatementFields(node, anchor) || j.utilitySyntaxFields(node, anchor) {
		return
	}
	switch n := node.(type) {
	case nil:
		j.number(0)
	case parser.Position:
		j.utilityPosition(n, anchor)
	case parser.TypeName:
		j.utilityTypeName(n, anchor)
	case *parser.TypeName:
		if n == nil {
			j.number(0)
		} else {
			j.utilityTypeName(*n, anchor)
		}
	case parser.Value:
		j.utilityValue(&n, anchor)
	case *parser.Value:
		if n == nil {
			j.number(0)
		} else {
			j.utilityValue(n, anchor)
		}
	case []parser.Expr:
		utilitySlice(j, n, anchor)
	case string:
		j.text(n)
	case bool:
		j.flag(n)
	case int:
		j.number(uint64(n))
	case int8:
		j.number(uint64(n))
	case int32:
		j.number(uint64(n))
	case int64:
		j.number(uint64(n))
	case uint8:
		j.number(uint64(n))
	case uint64:
		j.number(n)
	default:
		panic(fmt.Sprintf("stuntgres: unclassified utility syntax %T", node))
	}
}

func utilitySlice[T any](j *StatementJumble, values []T, anchor *int) {
	j.number(uint64(len(values)))
	// Interface and pointer slices already carry their operands indirectly.
	// Other syntax slices expose element addresses, avoiding a boxed struct copy.
	switch selected := any(values).(type) {
	case []parser.Expr:
		for i := range selected {
			j.utilitySyntax(selected[i], anchor)
		}
	case []parser.Statement:
		for i := range selected {
			j.utilitySyntax(selected[i], anchor)
		}
	case []*parser.CreateTable:
		for i := range selected {
			j.utilitySyntax(selected[i], anchor)
		}
	case []*parser.JSONFormat:
		for i := range selected {
			j.utilitySyntax(selected[i], anchor)
		}
	case []string:
		for i := range selected {
			j.text(selected[i])
		}
	case []int32:
		for i := range selected {
			j.number(uint64(selected[i]))
		}
	case [][]parser.Expr:
		for i := range selected {
			utilitySlice(j, selected[i], anchor)
		}
	default:
		for i := range values {
			j.utilitySyntax(&values[i], anchor)
		}
	}
}

func (j *StatementJumble) utilityValue(n *parser.Value, anchor *int) {
	j.text("A_Const")
	j.number(uint64(n.Kind))
	j.utilityPosition(n.Pos, anchor)
	switch n.Kind {
	case parser.IntegerValue:
		j.number(uint64(n.Integer))
	case parser.NumericValue, parser.StringValue, parser.BitStringValue:
		j.text(n.Text)
	case parser.BooleanValue:
		j.flag(n.Boolean)
	case parser.NullValue:
	default:
		panic(fmt.Sprintf("stuntgres: unclassified raw utility literal %d", n.Kind))
	}
}
