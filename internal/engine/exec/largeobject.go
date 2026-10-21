package exec

import (
	"fmt"
	"stuntgres/internal/engine/functions"
)

var _ functions.LargeObjectRuntime = (*Context)(nil)

func (c *Context) LargeObject(r functions.LargeObjectRequest) (functions.LargeObjectResult, error) {
	if err := EvaluationContext(c); err != nil {
		return functions.LargeObjectResult{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return functions.LargeObjectResult{}, fmt.Errorf("stuntgres: large object call lacks statement authority")
	}
	return host.LargeObject(r)
}
