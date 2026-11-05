package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledXML2XPathTable(n plan.InstalledXML2XPathTableExpr, v catalog.Read) error {
	if err := n.XML2XPathTable.Validate(); err != nil {
		return err
	}
	if n.Procedure == nil || n.RoutineReference != n.Procedure || !ValidExpressionUsage(v, n.Usage) {
		return fmt.Errorf("invalid stored xpath_table capture")
	}
	text, ok := v.LookupType(sqltype.Text)
	if !ok {
		return fmt.Errorf("missing xpath_table text authority")
	}
	for _, arg := range n.Args {
		if arg.ResultUsage().Type != text {
			return fmt.Errorf("foreign xpath_table operand generation")
		}
	}
	current, symbol, err := currentInstalledProcedure(v, n.Ref, n.Procedure)
	if err != nil {
		return err
	}
	if symbol != "xpath_table" {
		return fmt.Errorf("invalid stored xpath_table selector")
	}
	return extension.ValidateXML2XPathTableIdentity(v, current, n.Ref)
}
