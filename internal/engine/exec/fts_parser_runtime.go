package exec

import (
	"fmt"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

func (c *Context) ResolveFTSParser(value sqltype.Value) (fts.ParserRuntime, error) {
	runtime, ok := c.LiveHost().(functions.FTSParserRuntime)
	if !ok {
		return fts.ParserRuntime{}, fmt.Errorf("stuntgres: missing text parser statement authority")
	}
	return runtime.ResolveFTSParser(value)
}
