package exec

import (
	"errors"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// AdmissionAuthority is the borrowed local DDL admission authority of
// EvalAdmission. The runtime copies the slot with the context (the borrowed
// pointer, never the pointee) and reads it only through AdmissionAccess. A nil
// pointer is stored as an untyped nil.
type AdmissionAuthority interface{ AdmissionAccess() RoutineAccess }

type RoutineBodyContext struct {
	Outer     *catalog.Procedure
	Statement bool
}

func (context RoutineBodyContext) Annotate(err error) error {
	if err == nil || context.Outer == nil {
		return err
	}
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) && diagnostic.Code == "42501" {
		copy := *diagnostic
		phase := "during inlining"
		if context.Statement {
			phase = "statement 1"
		}
		copy.Context = fmt.Sprintf(`SQL function "%s" %s`, context.Outer.Name(), phase)
		return &copy
	}
	return err
}

func (c *Context) EvalSQLRoutine(n plan.SQLRoutineExpr) (value sqltype.Value, err error) {
	if err = EvaluationContext(c); err != nil {
		return value, err
	}
	frame := proc.NewFrame(n.Procedure.SQL.Inputs)
	defer frame.Close()
	for i, arg := range n.Args {
		v, err := arg.Eval(c)
		if err != nil {
			return value, err
		}
		if err = frame.Bind(i, v, arg.ResultUsage()); err != nil {
			return value, err
		}
	}
	// The statement's own host wins over a query source's host here.
	host := c.Host
	if host == nil && c.Query != nil && c.Query.Source != nil {
		host = c.Query.Source.LiveHost()
	}
	if host == nil {
		return value, fmt.Errorf("SQL routine invocation has no statement owner")
	}
	value, err = host.InvokeSQLRoutine(n.Procedure, frame, n.Usage)
	if err != nil {
		return value, err // The body owner annotates its actual command ordinal.
	}
	return frame.Result(value, n.Usage)
}

func SQLRoutineContext(err error, name string, runtime bool) error {
	statement := 0
	if runtime {
		statement = 1
	}
	return SQLRoutineStatementContext(err, name, statement)
}

// SQLRoutineStatementContext preserves leaf diagnostics and nesting order.
// Zero identifies producer-free definition validation.
func SQLRoutineStatementContext(err error, name string, statement int) error {
	if err == nil {
		return nil
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		return err
	}
	copy := *diagnostic
	context := "SQL function " + strconv.Quote(name)
	if statement != 0 {
		context += " statement " + strconv.Itoa(statement)
	}
	if copy.Context != "" {
		copy.Context += "\n"
	}
	copy.Context += context
	return &copy
}
