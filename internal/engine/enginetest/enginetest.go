// Package enginetest holds test helpers over the exported API of
// internal/engine/core. Only _test.go files import it.
package enginetest

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// sessionID numbers the sessions the helpers start, unique per test binary.
var sessionID atomic.Uint64

// Operation starts a session and acquires its operation. The cleanup releases
// the operation and ends the session.
func Operation(e *core.Engine, ctx context.Context) (*session.Operation, func(), error) {
	s, err := OpenSession(ctx, e, session.SessionStart{ID: ids.SessionID(sessionID.Add(1))})
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

// Execute runs stmt in a fresh session with positional arguments.
func Execute(e *core.Engine, ctx context.Context, stmt parser.Statement, args []driver.NamedValue) (core.Result, error) {
	op, cleanup, err := Operation(e, ctx)
	if err != nil {
		return core.Result{}, err
	}
	defer cleanup()
	return ExecuteFrame(e, op, stmt, args)
}

// Describe returns the parameter description of stmt in a fresh session.
func Describe(e *core.Engine, stmt parser.Statement) (analyze.ParameterInfo, error) {
	op, cleanup, err := Operation(e, context.Background())
	if err != nil {
		return analyze.ParameterInfo{}, err
	}
	defer cleanup()
	description, err := e.Describe(op, stmt)
	return description.Parameters, err
}

// Statement parses sql as exactly one statement.
func Statement(t testing.TB, sql string) parser.Statement {
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

// Run executes sql with args in a fresh session, fails the test on error and
// closes the result rows at test cleanup.
func Run(t testing.TB, e *core.Engine, sql string, args ...any) core.Result {
	t.Helper()
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	r, err := Execute(e, context.Background(), Statement(t, sql), named)
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

// ExecuteFrame executes stmt on op. Arguments must be positional; their values
// are converted to text and read with each parameter type's input function.
func ExecuteFrame(e *core.Engine, op *session.Operation, stmt parser.Statement, args []driver.NamedValue) (core.Result, error) {
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
				return &sqlstate.Error{
					Severity: "ERROR", SeverityUnlocalized: "ERROR",
					Code: "0A000", Message: "unsupported parameter value",
				}
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
