package exec

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type statementStatisticsRuntime interface {
	ValidatePgStatStatements(plan.InstalledPgStatStatementsExpr) error
	PgStatStatements(bool, bool, *sqltype.Descriptor) ([]sqltype.Value, error)
}

func (c *Context) statementStatisticsRuntime() (statementStatisticsRuntime, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	runtime, ok := c.LiveHost().(statementStatisticsRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: statement statistics lacks statement scope")
	}
	return runtime, nil
}
func (c *Context) PgStatStatements(show, info bool, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	runtime, err := c.statementStatisticsRuntime()
	if err != nil {
		return nil, err
	}
	return runtime.PgStatStatements(show, info, result)
}
func (c *Context) OpenPgStatStatements(n plan.InstalledPgStatStatementsExpr) (expr.SourceCursor, error) {
	runtime, err := c.statementStatisticsRuntime()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err = runtime.ValidatePgStatStatements(n); err != nil {
		return expr.SourceCursor{}, err
	}
	return n.PgStatStatements.OpenSource(c)
}
func (c *Context) EvalPgStatStatements(n plan.InstalledPgStatStatementsExpr) (sqltype.Value, error) {
	runtime, err := c.statementStatisticsRuntime()
	if err != nil {
		return sqltype.Value{}, err
	}
	if err = runtime.ValidatePgStatStatements(n); err != nil {
		return sqltype.Value{}, err
	}
	return n.PgStatStatements.Eval(c)
}
