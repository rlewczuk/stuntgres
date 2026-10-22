package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// JSONTable is immutable. A demanded invocation is fully materialized before
// publishing its first record; analyzed/prepared nodes never retain row state.
type JSONTable struct {
	expr.Info
	Raw   *parser.JSONTable
	Args  []expr.Expr
	Level JSONTableLevel
	Rows  sqltype.JSONTableRows
	Depth int
}

func NewJSONTable(info expr.Info, raw *parser.JSONTable, args []expr.Expr, level JSONTableLevel) (JSONTable, error) {
	if raw == nil || !info.Usage.Valid() || !info.Usage.Type.IsComposite() || len(args) < 1+len(raw.Passing) || level.Path < 0 || level.Path >= len(args) {
		return JSONTable{}, fmt.Errorf("invalid JSON_TABLE descriptor or children")
	}
	var validate func(JSONTableLevel) (int, error)
	validate = func(level JSONTableLevel) (int, error) {
		if level.Path < 0 || level.Path >= len(args) {
			return 0, fmt.Errorf("invalid JSON_TABLE path child")
		}
		for _, field := range level.Columns {
			if field.Slot < 0 || field.Slot >= info.Usage.Type.CompositeLen() || field.Child < 0 || field.Child >= len(args) || info.Usage.Type.CompositeMember(field.Slot).Usage != args[field.Child].ResultUsage() {
				return 0, fmt.Errorf("foreign JSON_TABLE column descriptor")
			}
		}
		depth := 1
		for _, child := range level.Nested {
			next, err := validate(child)
			if err != nil {
				return 0, err
			}
			depth = max(depth, 1+next)
		}
		return depth, nil
	}
	depth, err := validate(level)
	if err != nil {
		return JSONTable{}, err
	}
	usages := make([]sqltype.Usage, len(raw.Passing))
	names := make([]string, len(raw.Passing))
	for i, p := range raw.Passing {
		usages[i] = args[i+1].ResultUsage()
		names[i] = p.Name.Text
	}
	rows, err := sqltype.BindJSONTableRows(args[0].ResultUsage(), args[level.Path].ResultUsage(), args[0].ResultUsage(), usages, names)
	return JSONTable{Info: info, Raw: raw, Args: args, Level: level, Rows: rows, Depth: depth}, err
}
func (e JSONTable) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("JSON_TABLE requires a relational source")
}
func (e JSONTable) SourceAdmission() (bool, bool) { return true, true }
func (e JSONTable) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(JSONTable)
	return ok && e.Usage == o.Usage
}
func (e JSONTable) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	passing := make([]sqltype.Value, len(e.Raw.Passing))
	for i := range passing {
		passing[i], err = e.Args[i+1].Eval(c)
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	invocation, err := e.Rows.Start(ctx, passing, c.FunctionOutput())
	if err != nil {
		return expr.SourceCursor{}, err
	}
	document, err := e.Args[0].Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	width := e.Usage.Type.CompositeLen()
	// Active ancestors carry row item and ordinal, not cached column values:
	// PostgreSQL reevaluates parent columns for every expanded child output row.
	type activeLevel struct {
		level   *JSONTableLevel
		item    sqltype.Value
		ordinal int
	}
	active := make([]activeLevel, 0, e.Depth)
	var values []sqltype.Value
	emit := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		fields := make([]sqltype.Value, width)
		for i := range fields {
			fields[i] = sqltype.Null(e.Usage.Type.CompositeMember(i).Usage.Type)
		}
		for _, a := range active {
			for _, field := range a.level.Columns {
				var value sqltype.Value
				var err error
				if field.Ordinality {
					usage := e.Args[field.Child].ResultUsage()
					value, err = sqltype.Integer(usage.Type, int64(a.ordinal))
				} else {
					column, ok := e.Args[field.Child].(SQLJSONQuery)
					if !ok {
						return fmt.Errorf("invalid JSON_TABLE column provider")
					}
					value, err = column.evalDocument(ctx, c, a.item)
				}
				if err != nil {
					return err
				}
				fields[field.Slot] = value
			}
		}
		value, err := sqltype.OwnedCompositeValue(e.Usage.Type, fields)
		if err != nil {
			return err
		}
		values = append(values, value)
		return nil
	}
	selectRows := func(level *JSONTableLevel, parent sqltype.Value) ([]sqltype.Value, error) {
		path, err := e.Args[level.Path].Eval(c)
		if err != nil {
			return nil, err
		}
		return invocation.Select(ctx, parent, path, e.Raw.Error.Kind == "error", c.FunctionInput())
	}
	var expandSelected func(*JSONTableLevel, []sqltype.Value) (bool, error)
	expandSelected = func(level *JSONTableLevel, matches []sqltype.Value) (bool, error) {
		if len(matches) == 0 {
			return false, nil
		}
		nested := make([][]sqltype.Value, len(level.Nested))
		for i, item := range matches {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			active = append(active, activeLevel{level, item, i + 1})
			// Reset every immediate sibling before fetching descendants or
			// evaluating any active column. These vectors belong to this parent.
			for j := range level.Nested {
				var err error
				nested[j], err = selectRows(&level.Nested[j], item)
				if err != nil {
					return false, err
				}
			}
			present := false
			for j := range level.Nested {
				child, err := expandSelected(&level.Nested[j], nested[j])
				if err != nil {
					return false, err
				}
				present = present || child
			}
			if !present {
				if err := emit(); err != nil {
					return false, err
				}
			}
			active = active[:len(active)-1]
		}
		return len(matches) > 0, nil
	}
	matches, err := selectRows(&e.Level, document)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if _, err := expandSelected(&e.Level, matches); err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(e.Usage.Type, values), nil
}
func (e JSONTable) Children() []expr.Expr { return e.Args }
func (e JSONTable) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Args) {
		return nil, fmt.Errorf("invalid JSON_TABLE children")
	}
	members := make([]sqltype.CompositeMember, e.Usage.Type.CompositeLen())
	var collect func(JSONTableLevel)
	collect = func(level JSONTableLevel) {
		for _, field := range level.Columns {
			member := e.Usage.Type.CompositeMember(field.Slot)
			member.Usage = children[field.Child].ResultUsage()
			members[field.Slot] = member
		}
		for _, child := range level.Nested {
			collect(child)
		}
	}
	collect(e.Level)
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info.Usage = shape.Usage()
	return NewJSONTable(info, e.Raw, children, e.Level)
}
func (e JSONTable) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e JSONTable) IntrinsicVolatility() expr.Volatility           { return e.Property.Volatility }
func (e JSONTable) PlanningFold() expr.Fold                        { return expr.FoldNever }
func (e JSONTable) PreflightFold() expr.Preflight                  { return expr.PreflightChildren }
func (e JSONTable) ValidateStoredProvider() error                  { return nil }
func (e JSONTable) ClosureTransparent()                            {}
func (e JSONTable) ResultNullability(expr.ResultSite) (bool, bool) { return true, true }
func (e JSONTable) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONTable)
	if !ok || e.Raw.Error.Kind != o.Raw.Error.Kind || len(e.Args) != len(o.Args) || len(e.Raw.Passing) != len(o.Raw.Passing) {
		return false
	}
	for i, p := range e.Raw.Passing {
		if p.Name.Text != o.Raw.Passing[i].Name.Text {
			return false
		}
	}
	if e.Usage.Type.CompositeLen() != o.Usage.Type.CompositeLen() {
		return false
	}
	for i := range e.Usage.Type.CompositeLen() {
		a, b := e.Usage.Type.CompositeMember(i), o.Usage.Type.CompositeMember(i)
		if a.Name != b.Name || a.Usage != b.Usage {
			return false
		}
	}
	var levels func(JSONTableLevel, JSONTableLevel) bool
	levels = func(a, b JSONTableLevel) bool {
		if a.Path != b.Path || len(a.Columns) != len(b.Columns) || len(a.Nested) != len(b.Nested) {
			return false
		}
		for i, field := range a.Columns {
			if field != b.Columns[i] {
				return false
			}
		}
		for i, child := range a.Nested {
			if !levels(child, b.Nested[i]) {
				return false
			}
		}
		return true
	}
	if !levels(e.Level, o.Level) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// WrittenChildren pairs the raw source's actual expression occurrences with
// bound providers; synthetic column documents and repeated PASSING recipes are
// not extra written occurrences.
func (e JSONTable) WrittenChildren() []expr.Expr {
	document := e.Args[0]
	if wrapped, ok := document.(sqlJSONDocument); ok {
		document = wrapped.Child
	}
	result := []expr.Expr{document}
	result = append(result, e.Args[1:1+len(e.Raw.Passing)]...)
	columns := map[string]SQLJSONQuery{}
	var index func(JSONTableLevel)
	index = func(level JSONTableLevel) {
		for _, field := range level.Columns {
			if !field.Ordinality {
				query := e.Args[field.Child].(SQLJSONQuery)
				columns[query.Column] = query
			}
		}
		for _, child := range level.Nested {
			index(child)
		}
	}
	index(e.Level)
	var visit func([]parser.JSONTableColumn)
	visit = func(raw []parser.JSONTableColumn) {
		for _, column := range raw {
			if len(column.Columns) > 0 {
				visit(column.Columns)
				continue
			}
			query, ok := columns[column.Name.Text]
			if !ok {
				continue
			}
			child := 2 + len(query.Raw.Passing)
			if column.Empty.Default != nil {
				result = append(result, query.Args[child])
				child++
			}
			if column.Error.Default != nil {
				result = append(result, query.Args[child])
			}
		}
	}
	visit(e.Raw.Columns)
	return result
}

// SQL syntax belongs to the relational renderer, which supplies live declared
// member identities rather than pretending JSON_TABLE is an ordinary routine.
func (e JSONTable) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: "JSONTable", Inputs: e.Args}, nil
}

func (e JSONTable) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureSQLJSON {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: len(e.Raw.Expressions()), Indexed: true, JSONSources: true}, true, nil
}
func (e JSONTable) CaptureOperand(written int) (expr.Expr, error) {
	children := e.WrittenChildren()
	if written < 0 || written >= len(children) {
		return nil, fmt.Errorf("invalid JSON_TABLE capture operand")
	}
	return children[written], nil
}
