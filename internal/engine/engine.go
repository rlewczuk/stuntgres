package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type Engine struct {
	mu     sync.Mutex
	tables map[string]*table
}
type table struct {
	name    string
	columns []parser.Column
	index   map[string]int
	rows    [][]driver.Value
}
type ParameterInfo struct {
	Count int
	Types []parser.Type
}
type Result struct {
	Count   int64
	Columns []parser.Column
	Rows    *Snapshot
}
type binding struct {
	table      *table
	targets    []int
	projection []int
	predicate  int
	order      int
	parameters ParameterInfo
}

func New() *Engine { return &Engine{tables: make(map[string]*table)} }
func sqlError(code, message, table, column string) error {
	e := &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message, Table: table, Column: column,
	}
	if table != "" {
		e.Schema = "public"
	}
	return e
}
func (e *Engine) Describe(stmt parser.Statement) (ParameterInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, err := e.describe(stmt)
	if err != nil {
		return ParameterInfo{}, err
	}
	return b.parameters, nil
}
func (e *Engine) describe(stmt parser.Statement) (binding, error) {
	b := binding{predicate: -1, order: -1}
	used := make(map[int]parser.Type)
	bind := func(v parser.Value, c parser.Column, where bool) error {
		if v.Kind != parser.ParameterValue {
			_, err := literal(v, c.Type, where)
			return err
		}
		if v.Parameter <= 0 {
			return sqlError("42P02", "undefined parameter", "", c.Name)
		}
		if typ, ok := used[v.Parameter]; ok && typ != c.Type {
			return sqlError("0A000", "parameter reused with different destination types", "", c.Name)
		}
		used[v.Parameter] = c.Type
		if v.Parameter > b.parameters.Count {
			b.parameters.Count = v.Parameter
		}
		return nil
	}
	lookup := func(name string) (*table, error) {
		t := e.tables[name]
		if t == nil {
			return nil, sqlError("42P01", "table does not exist", name, "")
		}
		return t, nil
	}
	column := func(name string) (int, error) {
		i, ok := b.table.index[name]
		if !ok {
			return 0, sqlError("42703", "column does not exist", b.table.name, name)
		}
		return i, nil
	}
	switch s := stmt.(type) {
	case *parser.CreateTable:
		if e.tables[s.Name] != nil {
			return b, sqlError("42P07", "table already exists", s.Name, "")
		}
		seen := make(map[string]bool, len(s.Columns))
		if len(s.Columns) == 0 {
			return b, sqlError("42601", "table requires columns", s.Name, "")
		}
		for _, c := range s.Columns {
			if seen[c.Name] {
				return b, sqlError("42701", "duplicate column", s.Name, c.Name)
			}
			seen[c.Name] = true
			if c.Type > parser.Bool {
				return b, sqlError("0A000", "unsupported column type", s.Name, c.Name)
			}
		}
	case *parser.Insert:
		var err error
		b.table, err = lookup(s.Name)
		if err != nil {
			return b, err
		}
		if len(s.Targets) == 0 {
			width := len(s.Rows[0])
			if width > len(b.table.columns) {
				return b, insertError("42601", "INSERT has more expressions than target columns", s.Rows[0][len(b.table.columns)].Pos)
			}
			b.targets = make([]int, width)
			for i := range b.targets {
				b.targets[i] = i
			}
		} else {
			seen := make(map[int]bool, len(s.Targets))
			for _, target := range s.Targets {
				i, ok := b.table.index[target.Name]
				if !ok {
					return b, insertError("42703", fmt.Sprintf("column %q of relation %q does not exist", target.Name, s.Name), target.Pos)
				}
				if seen[i] {
					return b, insertError("42701", fmt.Sprintf("column %q specified more than once", target.Name), target.Pos)
				}
				seen[i] = true
				b.targets = append(b.targets, i)
			}
		}
		for rowIndex, row := range s.Rows {
			if rowIndex > 0 && len(row) != len(s.Rows[0]) {
				return b, insertError("42601", "VALUES lists must all be the same length", row[0].Pos)
			}
			if len(row) > len(b.targets) {
				return b, insertError("42601", "INSERT has more expressions than target columns", row[len(b.targets)].Pos)
			}
			if rowIndex == 0 && len(s.Targets) != 0 && len(row) < len(b.targets) {
				return b, insertError("42601", "INSERT has more target columns than expressions", s.Targets[len(row)].Pos)
			}
			for j, v := range row {
				c := b.table.columns[b.targets[j]]
				if err := bind(v, c, false); err != nil {
					return b, insertValueError(err, c, v)
				}
			}
		}
	case *parser.Select:
		var err error
		b.table, err = lookup(s.Name)
		if err != nil {
			return b, err
		}
		if s.Star {
			b.projection = make([]int, len(b.table.columns))
			for i := range b.projection {
				b.projection[i] = i
			}
		} else {
			for _, name := range s.Columns {
				i, err := column(name)
				if err != nil {
					return b, err
				}
				b.projection = append(b.projection, i)
			}
		}
		if s.Where != nil {
			b.predicate, err = column(s.Where.Column)
			if err != nil {
				return b, err
			}
			c := b.table.columns[b.predicate]
			if err = bind(s.Where.Value, c, true); err != nil {
				return b, annotate(err, s.Name, c.Name)
			}
		}
		if s.Order != nil {
			b.order, err = column(s.Order.Column)
			if err != nil {
				return b, err
			}
		}
	default:
		return b, sqlError("0A000", "unsupported statement", "", "")
	}
	// Gaps must fail before allocating a potentially huge parameter slice.
	if len(used) != b.parameters.Count {
		return b, sqlError("42P18", "could not determine unused parameter type", "", "")
	}
	b.parameters.Types = make([]parser.Type, b.parameters.Count)
	for n, t := range used {
		b.parameters.Types[n-1] = t
	}
	return b, nil
}
func annotate(err error, table, column string) error {
	var e *sqlstate.Error
	if errors.As(err, &e) {
		if e.Table == "" && table != "" {
			e.Table = table
		}
		if e.Column == "" && column != "" {
			e.Column = column
		}
		if e.Schema == "" && table != "" && e.Table == table {
			e.Schema = "public"
		}
	}
	return err
}


func insertError(code, message string, pos parser.Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: strconv.Itoa(pos.Character)}
}

func insertValueError(err error, column parser.Column, value parser.Value) error {
	sqlErr, ok := err.(*sqlstate.Error)
	if !ok {
		return err
	}
	sqlErr.Schema, sqlErr.Table, sqlErr.Column = "", "", ""
	switch sqlErr.Code {
	case "42804":
		source := "unknown"
		switch value.Kind {
		case parser.IntegerValue:
			source = "integer"
			if value.Integer < -2147483648 || value.Integer > 2147483647 {
				source = "bigint"
			}
		case parser.StringValue:
			source = "text"
		case parser.BooleanValue:
			source = "boolean"
		}
		destination := "integer"
		switch column.Type {
		case parser.Int8:
			destination = "bigint"
		case parser.Text:
			destination = "text"
		case parser.Bool:
			destination = "boolean"
		}
		sqlErr.Message = fmt.Sprintf("column %q is of type %s but expression is of type %s", column.Name, destination, source)
		sqlErr.Hint = "You will need to rewrite or cast the expression."
		sqlErr.Position = strconv.Itoa(value.Pos.Character)
	case "22P02", "22003":
		if value.Kind == parser.StringValue {
			sqlErr.Position = strconv.Itoa(value.Pos.Character)
		}
	}
	return sqlErr
}

func notNullError(table, column string, row []driver.Value) error {
	err := sqlError("23502", fmt.Sprintf(
		"null value in column \"%s\" of relation \"%s\" violates not-null constraint",
		column, table,
	), table, column)
	err.(*sqlstate.Error).Detail = failingRowDetail(row)
	return err
}


func prepareArgs(args []driver.NamedValue) ([]driver.Value, error) {
	values := make([]driver.Value, len(args))
	for i, a := range args {
		if a.Name != "" {
			return nil, fmt.Errorf("stuntgres: named parameters are not supported")
		}
		if a.Ordinal != i+1 {
			return nil, fmt.Errorf("stuntgres: parameter ordinal %d, want %d", a.Ordinal, i+1)
		}
		v, err := driver.DefaultParameterConverter.ConvertValue(a.Value)
		if err != nil {
			if _, ok := a.Value.(driver.Valuer); ok {
				return nil, err
			}
			return nil, sqlError("0A000", "unsupported parameter value", "", "")
		}
		switch v.(type) {
		case nil, int64, string, []byte, bool:
		default:
			return nil, sqlError("0A000", "unsupported parameter value", "", "")
		}
		values[i] = v
	}
	return values, nil
}

func parameterColumn(stmt parser.Statement) string {
	if s, ok := stmt.(*parser.Select); ok && s.Where != nil {
		return s.Where.Column
	}
	return ""
}

func (e *Engine) Execute(ctx context.Context, stmt parser.Statement, args []driver.NamedValue) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	values, err := prepareArgs(args)
	if err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	if err = ctx.Err(); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	b, err := e.describe(stmt)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if len(values) != b.parameters.Count {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", len(values), b.parameters.Count)
	}
	canonical := make([]driver.Value, len(values))
	for i, v := range values {
		canonical[i], err = parameter(v, b.parameters.Types[i])
		if err != nil {
			if _, isInsert := stmt.(*parser.Insert); isInsert {
				e.mu.Unlock()
				return Result{}, err
			}
			e.mu.Unlock()
			return Result{}, annotate(err, b.table.name, parameterColumn(stmt))
		}
	}
	resolve := func(v parser.Value, t parser.Type, where bool) (driver.Value, error) {
		if v.Kind == parser.ParameterValue {
			return canonical[v.Parameter-1], nil
		}
		return literal(v, t, where)
	}
	switch s := stmt.(type) {
	case *parser.CreateTable:
		t := &table{name: s.Name, columns: append([]parser.Column(nil), s.Columns...), index: make(map[string]int, len(s.Columns))}
		for i, c := range t.columns {
			t.index[c.Name] = i
		}
		if err = ctx.Err(); err == nil {
			e.tables[s.Name] = t
		}
		e.mu.Unlock()
		return Result{}, err
	case *parser.Insert:
		rows := make([][]driver.Value, len(s.Rows))
		for i, source := range s.Rows {
			if err = ctx.Err(); err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			row := make([]driver.Value, len(b.table.columns))
			for j, v := range source {
				target := b.targets[j]
				c := b.table.columns[target]
				row[target], err = resolve(v, c.Type, false)
				if err != nil {
					e.mu.Unlock()
					return Result{}, insertValueError(err, c, v)
				}
			}
			for j, c := range b.table.columns {
				if row[j] == nil && !c.Nullable {
					e.mu.Unlock()
					return Result{}, notNullError(s.Name, c.Name, row)
				}
			}
			rows[i] = row
		}
		if err = ctx.Err(); err == nil {
			b.table.rows = append(b.table.rows, rows...)
		}
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		return Result{Count: int64(len(rows))}, nil
	case *parser.Select:
		var predicate driver.Value
		if s.Where != nil {
			predicate, err = resolve(s.Where.Value, b.table.columns[b.predicate].Type, true)
			if err != nil {
				e.mu.Unlock()
				return Result{}, annotate(err, s.Name, s.Where.Column)
			}
		}
		snap := &Snapshot{rows: b.table.rows, projection: b.projection}
		columns := make([]parser.Column, len(b.projection))
		for i, j := range b.projection {
			columns[i] = b.table.columns[j]
		}
		e.mu.Unlock()
		if err = snap.selectRows(ctx, b.predicate, predicate, b.order, s.Order != nil && s.Order.Desc); err != nil {
			return Result{}, err
		}
		return Result{Count: int64(snap.Len()), Columns: columns, Rows: snap}, nil
	}
	e.mu.Unlock()
	return Result{}, sqlError("0A000", "unsupported statement", "", "")
}
