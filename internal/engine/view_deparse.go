package engine

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
)

func (v *viewDefinition) columnName(catalog CatalogRead, name string) (string, error) {
	attribute, ok := v.labels[name]
	if !ok {
		return "", fmt.Errorf("stuntgres: unannotated view column")
	}
	d, ok := catalog.state.descriptors[v.source.Address].(*relationDescriptor)
	if !ok {
		return "", ErrCatalogReplaced
	}
	for i, live := range d.attributes {
		if live == attribute {
			return quoteIdentifier(d.columns[i].Name), nil
		}
	}
	return "", fmt.Errorf("stuntgres: missing view column identity")
}

func (v *viewDefinition) selectSQL(catalog CatalogRead) (string, error) {
	s := v.syntax
	var targets []string
	if s.Star {
		d := catalog.state.descriptors[v.source.Address].(*relationDescriptor)
		for _, attribute := range v.attributes {
			found := false
			for i, live := range d.attributes {
				if attribute == live {
					targets = append(targets, quoteIdentifier(d.columns[i].Name))
					found = true
					break
				}
			}
			if !found {
				return "", fmt.Errorf("stuntgres: missing view star attribute")
			}
		}
	} else {
		for _, expr := range s.Targets {
			text, err := v.expressionSQL(catalog, expr)
			if err != nil {
				return "", err
			}
			targets = append(targets, text)
		}
	}
	for i := range targets {
		targets[i] += " AS " + quoteIdentifier(v.query.Root.Schema()[i].Name)
	}
	text := "SELECT " + strings.Join(targets, ", ")
	if v.source != (ObjectRef{}) {
		d := catalog.state.descriptors[v.source.Address].(*relationDescriptor)
		text += " FROM " + quoteIdentifier(catalog.state.descriptors[d.namespace].Name()) + "." + quoteIdentifier(d.name)
	}
	if s.Where != nil {
		part, err := v.expressionSQL(catalog, s.Where)
		if err != nil {
			return "", err
		}
		text += " WHERE " + part
	}
	if len(s.GroupBy) > 0 {
		var keys []string
		for _, expr := range s.GroupBy {
			part, err := v.expressionSQL(catalog, expr)
			if err != nil {
				return "", err
			}
			keys = append(keys, part)
		}
		text += " GROUP BY " + strings.Join(keys, ", ")
	}
	if s.Having != nil {
		part, err := v.expressionSQL(catalog, s.Having)
		if err != nil {
			return "", err
		}
		text += " HAVING " + part
	}
	if s.Order != nil {
		name, err := v.columnName(catalog, s.Order.Column)
		if err != nil {
			return "", err
		}
		text += " ORDER BY " + name
		if s.Order.Desc {
			text += " DESC"
		}
	}
	return text, nil
}

func (v *viewDefinition) expressionSQL(catalog CatalogRead, raw parser.Expr) (string, error) {
	child := func(expr parser.Expr) (string, error) { return v.expressionSQL(catalog, expr) }
	args := func(expressions []parser.Expr) (string, error) {
		parts := make([]string, len(expressions))
		for i, expr := range expressions {
			text, err := child(expr)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ", "), nil
	}
	switch e := raw.(type) {
	case parser.LiteralExpr:
		if ref, ok := v.sequenceRefs[e.Value.Pos.Offset]; ok {
			d, exists := catalog.state.descriptors[ref.Address].(*relationDescriptor)
			if !exists {
				return "'" + strconv.FormatUint(uint64(ref.Address.ObjectID), 10) + "'", nil
			}
			name := quoteIdentifier(catalog.state.descriptors[d.namespace].Name()) + "." + quoteIdentifier(d.name)
			return "'" + strings.ReplaceAll(name, "'", "''") + "'", nil
		}
		switch e.Value.Kind {
		case parser.NullValue:
			return "NULL", nil
		case parser.IntegerValue:
			return strconv.FormatInt(e.Value.Integer, 10), nil
		case parser.NumericValue:
			return e.Value.Text, nil
		case parser.BooleanValue:
			if e.Value.Boolean {
				return "TRUE", nil
			}
			return "FALSE", nil
		case parser.StringValue:
			return "'" + strings.ReplaceAll(e.Value.Text, "'", "''") + "'", nil
		}
	case parser.ColumnExpr:
		return v.columnName(catalog, e.Name.Parts[len(e.Name.Parts)-1].Text)
	case parser.TypedLiteralExpr:
		return "CAST('" + strings.ReplaceAll(e.Text, "'", "''") + "' AS " + syntaxTypeSQL(e.TypeName) + ")", nil
	case parser.CastExpr:
		text, err := child(e.Child)
		if err != nil {
			return "", err
		}
		return "CAST(" + text + " AS " + syntaxTypeSQL(e.TypeName) + ")", nil
	case parser.OperatorExpr:
		left, err := v.comparisonOperandSQL(catalog, e.Args[0])
		if err != nil {
			return "", err
		}
		op := e.Name.Parts[len(e.Name.Parts)-1].Text
		if len(e.Args) == 1 {
			return "(" + op + " " + left + ")", nil
		}
		right, err := v.comparisonOperandSQL(catalog, e.Args[1])
		if err != nil {
			return "", err
		}
		return "(" + left + " " + op + " " + right + ")", nil
	case parser.LogicalExpr:
		left, err := child(e.Left)
		if err != nil {
			return "", err
		}
		if e.Op == parser.LogicalNot {
			return "(NOT " + left + ")", nil
		}
		right, err := child(e.Right)
		if err != nil {
			return "", err
		}
		op := "AND"
		if e.Op == parser.LogicalOr {
			op = "OR"
		}
		return "(" + left + " " + op + " " + right + ")", nil
	case parser.PredicateExpr:
		left, err := child(e.Left)
		if err != nil {
			return "", err
		}
		operators := []string{"IS NULL", "IS NOT NULL", "IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN", "IS DISTINCT FROM", "IS NOT DISTINCT FROM"}
		text := "(" + left + " " + operators[e.Kind]
		if e.Right != nil {
			right, err := child(e.Right)
			if err != nil {
				return "", err
			}
			text += " " + right
		}
		return text + ")", nil
	case parser.AggregateExpr:
		return e.Name + "(*)", nil
	case parser.ConditionalExpr:
		text, err := args(e.Args)
		return e.Kind.String() + "(" + text + ")", err
	case parser.CaseExpr:
		text := "CASE"
		if e.Test != nil {
			part, err := child(e.Test)
			if err != nil {
				return "", err
			}
			text += " " + part
		}
		for _, arm := range e.Arms {
			condition, err := child(arm.Condition)
			if err != nil {
				return "", err
			}
			result, err := child(arm.Result)
			if err != nil {
				return "", err
			}
			text += " WHEN " + condition + " THEN " + result
		}
		if e.Else != nil {
			part, err := child(e.Else)
			if err != nil {
				return "", err
			}
			text += " ELSE " + part
		}
		return text + " END", nil
	case parser.FunctionExpr:
		text, err := args(e.Args)
		return objectNameText(e.Name) + "(" + text + ")", err
	case parser.CallExpr:
		text, err := args(e.Args)
		return e.Name.Text + "(" + text + ")", err
	case parser.RegclassExpr:
		text, err := child(e.Child)
		return "CAST(" + text + " AS regclass)", err
	}
	return "", fmt.Errorf("stuntgres: unsupported view deparser expression %T", raw)
}

// BETWEEN lowers to ordinary comparison/logical nodes. Predicate-valued
// operands need the same scalar CASE bridge used by stored defaults on replay.
func (v *viewDefinition) comparisonOperandSQL(catalog CatalogRead, raw parser.Expr) (string, error) {
	text, err := v.expressionSQL(catalog, raw)
	if err != nil {
		return "", err
	}
	switch e := raw.(type) {
	case parser.LogicalExpr, parser.PredicateExpr:
		return predicateOperandSQL(text), nil
	case parser.OperatorExpr:
		switch e.Name.Parts[len(e.Name.Parts)-1].Text {
		case "=", "<>", "!=", "<", "<=", ">", ">=":
			return predicateOperandSQL(text), nil
		}
	}
	return text, nil
}

func syntaxTypeSQL(t parser.TypeName) string {
	name := t.Name
	if t.Quoted {
		name = quoteIdentifier(name)
	}
	if len(t.Modifiers) > 0 {
		parts := make([]string, len(t.Modifiers))
		for i, m := range t.Modifiers {
			if m.Integer != nil {
				parts[i] = strconv.FormatInt(int64(*m.Integer), 10)
			} else {
				parts[i] = m.Raw
			}
		}
		name += "(" + strings.Join(parts, ",") + ")"
	}
	return name
}
