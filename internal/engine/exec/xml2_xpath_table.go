package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) XML2XPathTable(args [5]string, result *sqltype.Descriptor, reference expr.RoutineReference) ([]sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	procedure, ok := reference.(*catalog.Procedure)
	host := c.LiveHost()
	if !ok || procedure == nil || host == nil {
		return nil, fmt.Errorf("xpath_table lacks installed statement authority")
	}
	return host.Contrib().XML2XPathTable(args, result, procedure)
}

func (c *Context) OpenXML2XPathTable(n plan.InstalledXML2XPathTableExpr) (expr.SourceCursor, error) {
	if err := EvaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("xpath_table lacks statement scope")
	}
	if err := host.Contrib().ValidateXML2XPathTable(n); err != nil {
		return expr.SourceCursor{}, err
	}
	return n.XML2XPathTable.OpenSource(c)
}
