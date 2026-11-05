package exec

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (c *Context) BackendStateValue(kind functions.BackendStateKind, id int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: backend state lacks statement authority")
	}
	return host.SessionInquiry().BackendStateValue(kind, id, result)
}
