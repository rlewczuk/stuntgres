package functions

import (
	"encoding/json"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// JSONTableLevel contains only immutable child indexes and output slots.
type JSONTableLevel struct {
	Path       int
	Columns    []JSONTableField
	Nested     []JSONTableLevel
	Start, End int
}
type JSONTableField struct {
	Slot, Child int
	Ordinality  bool
}

func BindJSONTable(b Binder, raw *parser.JSONTable) (expr.Expr, error) {
	literal, ok := raw.Path.(parser.LiteralExpr)
	if !ok || literal.Value.Kind != parser.StringValue {
		return nil, expr.ErrorAt("0A000", "only string constants are supported in JSON_TABLE path specification", raw.PathPos)
	}
	if raw.Error.Kind != "" && raw.Error.Kind != "error" && raw.Error.Kind != "empty array" {
		err := expr.ErrorAt("42601", "invalid ON ERROR behavior", raw.Error.Pos).(*sqlstate.Error)
		err.Detail = "Only EMPTY [ ARRAY ] or ERROR is allowed in the top-level ON ERROR clause."
		return nil, err
	}
	names := map[string]bool{}
	if raw.PathName == nil {
		names["json_table_path_0"] = true
	}
	add := func(name *parser.Identifier) error {
		if name == nil {
			return nil
		}
		if names[name.Text] {
			return expr.ErrorAt("42712", "duplicate JSON_TABLE column or path name: "+name.Text, name.Pos)
		}
		names[name.Text] = true
		return nil
	}
	if err := add(raw.PathName); err != nil {
		return nil, err
	}
	var validate func([]parser.JSONTableColumn) error
	validate = func(columns []parser.JSONTableColumn) error {
		ordinal := false
		for _, c := range columns {
			if len(c.Columns) > 0 {
				if err := add(c.PathName); err != nil {
					return err
				}
				if err := validate(c.Columns); err != nil {
					return err
				}
				continue
			}
			if err := add(&c.Name); err != nil {
				return err
			}
			if c.Ordinality {
				if ordinal {
					return expr.ErrorAt("42601", "only one FOR ORDINALITY column is allowed", c.Name.Pos)
				}
				ordinal = true
			}
		}
		return nil
	}
	if err := validate(raw.Columns); err != nil {
		return nil, err
	}
	document, err := sqlJSONQuerySource(b, raw.Document, sqltype.JSONB, raw.Input)
	if err != nil {
		return nil, err
	}
	if wrapped, ok := document.(sqlJSONDocument); ok {
		wrapped.Table = true
		document = wrapped
	}
	args := []expr.Expr{document}
	for _, p := range raw.Passing {
		operand, err := b.AnalyzeArgument(p.Value, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		arg := operand.Expression
		if operand.Usage.Type == nil {
			arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
			if err != nil {
				return nil, err
			}
		}
		if err := b.CaptureTypeInput(arg.ResultUsage().Type); err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	var members []sqltype.CompositeMember
	var bindLevel func(parser.Expr, []parser.JSONTableColumn) (JSONTableLevel, error)
	bindLevel = func(path parser.Expr, columns []parser.JSONTableColumn) (JSONTableLevel, error) {
		level := JSONTableLevel{Path: len(args), Start: len(members)}
		bound, err := sqlJSONQuerySource(b, path, sqltype.JSONPath, nil)
		if err != nil {
			return level, err
		}
		args = append(args, bound)
		for _, c := range columns {
			if len(c.Columns) > 0 {
				continue
			}
			usage := b.Type(sqltype.Int4).Usage()
			var node expr.Expr
			if c.Ordinality {
				operand, err := b.AnalyzeArgument(parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: c.Pos}}, AnalysisRequest{Intent: ComparisonArgument})
				if err != nil {
					return level, err
				}
				node, err = b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: usage})
				if err != nil {
					return level, err
				}
			} else {
				usage, err = SQLJSONReturningType(b, c.Type)
				if err != nil {
					return level, err
				}
				kind := "json_value"
				base := usage.Type.BaseType()
				if c.Format != nil || c.Wrapper != "" || c.Quotes != "" || base.ID() == sqltype.JSON || base.ID() == sqltype.JSONB || base.IsArray() || base.IsComposite() {
					kind = "json_query"
				}
				if c.Exists {
					kind = "json_exists"
				}
				path := c.Path
				if path == nil {
					name, _ := json.Marshal(c.Name.Text)
					path = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "$." + string(name), Pos: c.Name.Pos}}
				}
				null := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue, Pos: c.Pos}}, TypeName: parser.TypeName{Name: "jsonb", Pos: c.Pos}, Pos: c.Pos}
				query := parser.SQLJSONQueryExpr{Kind: kind, Document: null, Path: path, Passing: raw.Passing, Returning: &c.Type, Output: c.Format, Wrapper: c.Wrapper, Quotes: c.Quotes, Empty: c.Empty, Error: c.Error, Pos: c.Pos, TableColumn: c.Name.Text}
				node, err = BindSQLJSONQuery(b, query)
				if err != nil {
					return level, err
				}
			}
			b.Info(usage, c.Pos, expr.Properties{})
			level.Columns = append(level.Columns, JSONTableField{Slot: len(members), Child: len(args), Ordinality: c.Ordinality})
			members = append(members, sqltype.CompositeMember{Attribute: int32(len(members) + 1), Name: c.Name.Text, Usage: usage})
			args = append(args, node)
		}
		for _, c := range columns {
			if len(c.Columns) > 0 {
				child, err := bindLevel(c.Path, c.Columns)
				if err != nil {
					return level, err
				}
				level.Nested = append(level.Nested, child)
			}
		}
		level.End = len(members)
		return level, nil
	}
	level, err := bindLevel(raw.Path, raw.Columns)
	if err != nil {
		return nil, err
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	properties := expr.Properties{}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	namesList := make([]string, len(raw.Passing))
	passingUsages := make([]sqltype.Usage, len(raw.Passing))
	for i, p := range raw.Passing {
		namesList[i] = p.Name.Text
		passingUsages[i] = args[i+1].ResultUsage()
	}
	var mutable func(JSONTableLevel) bool
	mutable = func(level JSONTableLevel) bool {
		path, constant := b.ConstantValue(args[level.Path])
		if !constant || sqltype.SQLJSONPathMutable(path, namesList, passingUsages) {
			return true
		}
		for _, child := range level.Nested {
			if mutable(child) {
				return true
			}
		}
		return false
	}
	if mutable(level) {
		properties.Volatility = max(properties.Volatility, expr.Stable)
	}
	return NewJSONTable(b.Info(shape.Usage(), raw.Pos, properties), raw, args, level)
}
