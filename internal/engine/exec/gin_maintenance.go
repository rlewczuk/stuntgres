package exec

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

var _ functions.GinMaintenanceRuntime = (*Context)(nil)

func (c *Context) GinCleanPendingList(oid ids.OID) (int64, error) {
	if err := EvaluationContext(c); err != nil {
		return 0, err
	}
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: GIN cleanup lacks statement authority")
	}
	return host.GinCleanPendingList(oid)
}
