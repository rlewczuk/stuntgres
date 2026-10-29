package stored

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledEarth(n plan.InstalledEarthExpr, view catalog.Read) error {
	captured, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return err
	}
	if captured == nil || captured != n.Procedure {
		return fmt.Errorf("invalid stored earth identity")
	}
	proc, symbol, err := currentInstalledProcedure(view, n.Ref, captured)
	if err != nil {
		return err
	}
	if err := n.ValidateStoredProvider(); err != nil {
		return err
	}
	for _, arg := range n.Args {
		actual, ok := view.LookupType(arg.ResultUsage().Type.ID())
		if !ok || actual != arg.ResultUsage().Type {
			return fmt.Errorf("foreign stored earth formal generation")
		}
	}
	result, ok := view.LookupType(n.ResultUsage().Type.ID())
	if !ok || result != n.ResultUsage().Type {
		return fmt.Errorf("foreign stored earth result generation")
	}
	if extension.EarthdistanceProviderOf(proc) != extension.StockEarthdistance || symbol != n.Name() || string(proc.Signature) != n.Signature() || proc.Result != ids.OID(n.ResultUsage().Type.ID()) {
		return fmt.Errorf("invalid stored earth routine")
	}
	ext, ok := catalog.OriginInstallation(proc)
	if !ok || len(ext.Members) != extension.EarthMemberCount {
		return fmt.Errorf("missing earth installation")
	}
	if n.Plan.Operation() >= sqltype.EarthLL {
		earth, ok := view.Type(ext.Members[0])
		if !ok || earth.SQLType != n.Plan.Earth() {
			return fmt.Errorf("foreign stored earth generation")
		}
	} else if n.Plan.Earth() != nil {
		return fmt.Errorf("scalar earth call retained a nominal domain")
	}
	return extension.StockEarthdistance.ValidateProcedure(view, proc)
}
func sameMappedEarthSignature(n plan.InstalledEarthExpr, current *catalog.Procedure, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if current == nil || extension.EarthdistanceProviderOf(current) != extension.StockEarthdistance || len(n.Args) > 2 {
		return false
	}
	var parts [2]string
	for i, arg := range n.Args {
		if arg == nil {
			return false
		}
		mapped := typ(arg.ResultUsage().Type)
		if mapped == nil {
			return false
		}
		parts[i] = strconv.FormatUint(uint64(mapped.ID()), 10)
	}
	result := typ(n.ResultUsage().Type)
	return result != nil && string(current.Signature) == strings.Join(parts[:len(n.Args)], ",") && current.Result == ids.OID(result.ID())
}
