package exec

import (
	"fmt"

	"stuntgres/internal/engine/functions"
)

var _ functions.PgTrgmRuntime = (*Context)(nil)

func (c *Context) InitializePgTrgm() error {
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: pg_trgm initialization lacks statement authority")
	}
	return host.InitializePgTrgm()
}

func (c *Context) PgTrgmThreshold(name string) (float64, error) {
	host := c.LiveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: pg_trgm threshold lacks statement authority")
	}
	return host.PgTrgmThreshold(name)
}
