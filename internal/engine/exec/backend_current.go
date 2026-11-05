package exec

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

func (c *Context) BackendActivity(pid sqltype.Value, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: backend activity lacks statement authority")
	}
	return host.SessionInquiry().BackendActivity(pid, result)
}
func (c *Context) BackendIO(pid int64, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: backend IO lacks statement authority")
	}
	return host.SessionInquiry().BackendIO(pid, result)
}
func (c *Context) BackendWAL(pid int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: backend WAL lacks statement authority")
	}
	return host.SessionInquiry().BackendWAL(pid, result)
}
