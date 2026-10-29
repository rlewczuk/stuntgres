package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/enginetest"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var testSessionID atomic.Uint64

func testOperation(e *core.Engine, ctx context.Context) (*session.Operation, func(), error) {
	s, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		return nil, nil, err
	}
	op, err := s.Acquire(ctx)
	cleanup := func() {
		if op != nil {
			op.Done()
		}
		s.RequestEnd(session.CleanClose)
		_ = s.WaitEnd(context.Background())
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return op, cleanup, nil
}
func testExecute(e *core.Engine, ctx context.Context, stmt parser.Statement, args []driver.NamedValue) (core.Result, error) {
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		return core.Result{}, err
	}
	defer cleanup()
	return testExecuteFrame(e, op, stmt, args)
}
func testDescribe(e *core.Engine, stmt parser.Statement) (analyze.ParameterInfo, error) {
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		return analyze.ParameterInfo{}, err
	}
	defer cleanup()
	description, err := e.Describe(op, stmt)
	return description.Parameters, err
}
func statement(t *testing.T, sql string) parser.Statement {
	t.Helper()
	s, err := parser.Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 {
		t.Fatalf("expected single statement, got %d", len(s))
	}
	return s[0]
}
func run(t *testing.T, e *core.Engine, sql string, args ...any) core.Result {
	t.Helper()
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	r, err := testExecute(e, context.Background(), statement(t, sql), named)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != nil {
		t.Cleanup(func() {
			if err := r.Rows.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return r
}

// Tuple assertions use an independent retained cursor so lifetime tests may
// inspect the original result later without rewinding its public position.
func independentFinalRows(original exec.RowResult) *exec.FinalRows {
	old := original.(*exec.FinalRows)
	r := exec.NewFinalRows(old.Columns, old.Count)
	r.Values, r.Selection, r.Slots = old.Values, old.Selection, old.Slots
	r.Anonymous = old.Anonymous
	r.RegclassOutput = old.RegclassOutput
	if old.Source != nil {
		source := *old.Source
		if source.View != nil {
			source.View = source.View.Retain()
		}
		r.Source = &source
	}
	return r
}
func tuples(r core.Result) [][]driver.Value {
	cursor := independentFinalRows(r.Rows)
	defer cursor.Close()
	rows := make([][]driver.Value, 0, cursor.Count)
	values := make([]sqltype.Value, len(cursor.Schema()))
	for {
		present, err := cursor.Next(context.Background(), values)
		if err != nil {
			panic(err)
		}
		if !present {
			break
		}
		row := make([]driver.Value, len(values))
		for j, v := range values {
			row[j] = testExport(v)
		}
		rows = append(rows, row)
	}
	return rows
}

func testExport(v sqltype.Value) driver.Value {
	if v.IsNull() {
		return nil
	}
	if v.Type().BaseType().IsArray() || v.Type().BaseType().IsEnum() {
		s, err := v.Type().Output(context.Background(), v)
		if err != nil {
			panic(err)
		}
		return s
	}
	switch v.Type().ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		n, err := v.Integer()
		if err != nil {
			panic(err)
		}
		return n
	case sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name:
		s, err := v.Text()
		if err != nil {
			panic(err)
		}
		return s
	case sqltype.Bool:
		b, err := v.Boolean()
		if err != nil {
			panic(err)
		}
		return b
	}
	panic("invalid test value")
}
func testExecuteFrame(e *core.Engine, op *session.Operation, stmt parser.Statement, args []driver.NamedValue) (core.Result, error) {
	raw := make([]driver.Value, len(args))
	for i, a := range args {
		if a.Name != "" || a.Ordinal != i+1 {
			return core.Result{}, fmt.Errorf("invalid test argument")
		}
		v, err := driver.DefaultParameterConverter.ConvertValue(a.Value)
		if err != nil {
			return core.Result{}, err
		}
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		raw[i] = v
	}
	return e.Execute(op, stmt, len(raw), func(ctx context.Context, p analyze.ParameterInfo, dst []sqltype.Value, input sqltype.InputEnvironment) error {
		for i, v := range raw {
			if v == nil {
				dst[i] = sqltype.Null(p.Types[i].Type)
				continue
			}
			var s string
			switch v := v.(type) {
			case string:
				s = v
			case int64:
				s = strconv.FormatInt(v, 10)
			case bool:
				s = strconv.FormatBool(v)
			default:
				return core.SQLError("0A000", "unsupported parameter value")
			}
			value, err := p.Types[i].Type.InputWithEnvironment(ctx, s, -1, input)
			if err != nil {
				if p.Types[i].Type.ContainsRegclass() {
					return analyze.SequenceInputError(err, parser.Position{}, "", i+1)
				}
				return err
			}
			dst[i] = value
		}
		return nil
	})
}
func state(t *testing.T, err error, want string) {
	t.Helper()
	var s interface{ SQLState() string }
	if !errors.As(err, &s) || s.SQLState() != want {
		t.Fatalf("error %v, want SQLSTATE %s", err, want)
	}
}
