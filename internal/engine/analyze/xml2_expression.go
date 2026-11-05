package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// ValidInstalledXML2Shape checks the immutable declaration and typed recipe.
// Catalog owners additionally authenticate current descriptor/type generations.
func ValidInstalledXML2Shape(n plan.InstalledXML2Expr) bool {
	p := n.Procedure
	if p == nil || n.Inline || extension.XML2ProviderOf(p) != extension.StockXML2 || n.RoutineReference != p || n.Ref.Domain() == nil || n.Ref.Epoch == 0 || n.Ref.Address != p.Address() || n.Ref.Address.ClassID != ids.ProcedureClassID || n.Ref.Address.SubID != 0 || n.Validate() != nil {
		return false
	}
	origin := p.Origin
	if !origin.Valid(n.Ref.Address) || origin.Installation.Implementation != extension.StockXML2 || origin.Installation.Name() != "xml2" || origin.Installation.Version != "1.2" || origin.Declaration >= len(functions.XML2Members) {
		return false
	}
	m := functions.XML2Members[origin.Declaration]
	if m.Name != n.Name || m.Operation != n.Operation || m.Arity != len(n.Args) || p.Signature != catalog.ProcedureSignature(m.Signature) || p.Result != ids.OID(m.Result) || p.Volatility != m.Volatility || !p.Strict {
		return false
	}
	if m.Kernel < 0 {
		if n.Kernel != nil {
			return false
		}
	} else {
		kernel := n.Kernel
		if kernel == nil || extension.XML2ProviderOf(kernel) != extension.StockXML2 || !kernel.Origin.Valid(kernel.Address()) || kernel.Origin.Installation != origin.Installation || kernel.Origin.Declaration != m.Kernel || kernel.Address() != origin.Installation.Members[m.Kernel] {
			return false
		}
		inner := functions.XML2Members[m.Kernel]
		if kernel.Signature != catalog.ProcedureSignature(inner.Signature) || kernel.Result != ids.OID(inner.Result) || kernel.Volatility != inner.Volatility || !kernel.Strict || kernel.DeclarationBody != nil {
			return false
		}
	}
	want := expr.Properties{Volatility: m.Volatility}
	for _, arg := range n.Args {
		want = expr.MergeProperties(want, arg.Properties())
	}
	got := n.Properties()
	// Aggregate reconstruction adds stage dependence to the carrier itself.
	got.StageDependent, want.StageDependent = false, false
	return got == want
}
