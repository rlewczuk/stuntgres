package exec

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (c *Context) BackendIdentityIDs(result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: backend identity lacks statement authority")
	}
	return host.SessionInquiry().BackendIdentityIDs(result)
}
func (c *Context) BackendIdentityValue(kind functions.BackendIdentityKind, id int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: backend identity lacks statement authority")
	}
	return host.SessionInquiry().BackendIdentityValue(kind, id, result)
}
