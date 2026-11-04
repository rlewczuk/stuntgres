package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (c *Context) DebugTextSearch(ctx context.Context, info expr.Info, explicit bool, configuration sqltype.Value, text string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing inspection statement authority")
	}
	return host.TextSearch().DebugTextSearch(ctx, c, info, explicit, configuration, text, result)
}
func (c *Context) StatTextSearch(ctx context.Context, text, weights string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	host := c.LiveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: missing inspection statement authority")
	}
	return host.TextSearch().StatTextSearch(ctx, text, weights, result)
}
