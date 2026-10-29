package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (c *Context) DebugTextSearch(ctx context.Context, explicit bool, configuration sqltype.Value, text string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	runtime, ok := c.LiveHost().(functions.FTSInspectionRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing inspection statement authority")
	}
	return runtime.DebugTextSearch(ctx, explicit, configuration, text, result)
}
func (c *Context) StatTextSearch(ctx context.Context, text, weights string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	runtime, ok := c.LiveHost().(functions.FTSInspectionRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing inspection statement authority")
	}
	return runtime.StatTextSearch(ctx, text, weights, result)
}
