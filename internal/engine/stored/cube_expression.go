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

func ValidateInstalledCube(n plan.InstalledCubeExpr, view catalog.Read) error {
	captured, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return err
	}
	if captured == nil || captured != n.Procedure {
		return fmt.Errorf("stuntgres: invalid stored cube identity")
	}
	proc, symbol, err := currentInstalledProcedure(view, n.Ref, captured)
	if err != nil {
		return err
	}
	if err := n.Validate(); err != nil {
		return err
	}
	for _, arg := range n.Args {
		actual, ok := view.LookupType(arg.ResultUsage().Type.ID())
		if !ok || actual != arg.ResultUsage().Type {
			return fmt.Errorf("stuntgres: foreign stored cube formal generation")
		}
	}
	result, ok := view.LookupType(n.ResultUsage().Type.ID())
	if !ok || result != n.ResultUsage().Type {
		return fmt.Errorf("stuntgres: foreign stored cube result generation")
	}
	if extension.CubeProviderOf(proc) != extension.StockCube || symbol != n.Name() || string(proc.Signature) != n.Signature() || proc.Result != ids.OID(n.ResultUsage().Type.ID()) {
		return fmt.Errorf("stuntgres: invalid stored cube identity")
	}
	ext, ok := view.Extension(proc.Extension)
	if !ok || len(ext.Members) == 0 {
		return fmt.Errorf("stuntgres: missing cube installation")
	}
	base, ok := view.Type(ext.Members[0])
	if !ok || base.SQLType != n.CubeType {
		return fmt.Errorf("stuntgres: foreign stored cube generation")
	}
	return extension.StockCube.ValidateProcedure(view, proc)
}

func sameMappedCubeSignature(n plan.InstalledCubeExpr, current *catalog.Procedure, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if current == nil || extension.CubeProviderOf(current) != extension.StockCube || len(n.Args) > 3 {
		return false
	}
	var parts [3]string
	for i, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
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
