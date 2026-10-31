package exec

import (
	"fmt"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype"
)

func (c *Context) ResolveFTSParser(value sqltype.Value) (fts.ParserRuntime, error) {
	host := c.LiveHost()
	if host == nil {
		return fts.ParserRuntime{}, fmt.Errorf("stuntgres: missing text parser statement authority")
	}
	return host.TextSearch().ResolveFTSParser(value)
}
