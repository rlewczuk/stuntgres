package stored

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidateInstalledCube(n plan.InstalledCubeExpr, view catalog.Read) error {
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
	proc, ok := view.Procedure(n.Ref.Address)
	if !ok || proc != n.Procedure || extension.CubeProviderOf(proc) != extension.StockCube || proc.Name() != n.Name() || string(proc.Signature) != n.Signature() || proc.Result != ids.OID(n.ResultUsage().Type.ID()) {
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
