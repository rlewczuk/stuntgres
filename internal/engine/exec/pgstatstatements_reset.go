package exec

import (
	"fmt"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type statementStatisticsResetRuntime interface {
	ValidatePgStatStatementsReset(plan.InstalledPgStatStatementsResetExpr) error
	PgStatStatementsReset(uint32, uint32, int64, bool, *sqltype.Descriptor) (sqltype.Value, error)
}

func (c *Context) statementStatisticsResetRuntime() (statementStatisticsResetRuntime, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	runtime, ok := c.LiveHost().(statementStatisticsResetRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: statement statistics reset lacks statement scope")
	}
	return runtime, nil
}
func (c *Context) PgStatStatementsReset(user, database uint32, query int64, minmax bool, result *sqltype.Descriptor) (sqltype.Value, error) {
	runtime, err := c.statementStatisticsResetRuntime()
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.PgStatStatementsReset(user, database, query, minmax, result)
}
func (c *Context) EvalPgStatStatementsReset(n plan.InstalledPgStatStatementsResetExpr) (sqltype.Value, error) {
	runtime, err := c.statementStatisticsResetRuntime()
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := runtime.ValidatePgStatStatementsReset(n); err != nil {
		return sqltype.Value{}, err
	}
	return n.PgStatStatementsReset.Eval(c)
}
