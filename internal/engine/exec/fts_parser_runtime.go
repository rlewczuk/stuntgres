package exec

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (c *Context) ResolveFTSParser(value sqltype.Value) error {
	runtime, ok := c.LiveHost().(functions.FTSParserRuntime)
	if !ok { return fmt.Errorf("stuntgres: missing text parser statement authority") }
	return runtime.ResolveFTSParser(value)
}
