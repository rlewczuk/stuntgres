package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// The released 1.2 bodies bind their installation's actual kernel slot. Public
// renames, detach and replacement cannot retarget this immutable declaration.
var xml2Bodies = func() [len(functions.XML2Members)]sqltype.StructuralTree {
	var bodies [len(functions.XML2Members)]sqltype.StructuralTree
	empty := func() declarationParts { return declarationString("") }
	for slot, member := range functions.XML2Members {
		if member.Kernel < 0 {
			continue
		}
		var args []declarationParts
		switch slot {
		case 6:
			args = []declarationParts{declarationArg(0), declarationArg(1), empty(), declarationArg(2)}
		case 7:
			args = []declarationParts{declarationArg(0), declarationArg(1), empty(), empty()}
		case 9:
			args = []declarationParts{declarationArg(0), declarationArg(1), declarationString(",")}
		}
		kernel := functions.XML2Members[member.Kernel]
		var arguments [4]uint32
		for i := range kernel.Arity {
			arguments[i] = 25
		}
		part := proc.DeclarationMember("", member.Kernel, uint32(ids.ProcedureClassID), kernel.Name, arguments[:kernel.Arity]...)
		bodies[slot] = mustRoutineDeclarationBody(declarationCall(part, args...))
	}
	return bodies
}()

// SupportsRoutineConfiguration classifies only the genuine installed SQL body;
// callers still own current catalog identity and invocation authorization.
func (p *XML2Provider) SupportsRoutineConfiguration(d *catalog.Procedure) bool {
	if p != StockXML2 || d == nil || d.Implementation != p || !d.Origin.Valid(d.Address()) {
		return false
	}
	origin := d.Origin.Installation
	slot := d.Origin.Declaration
	if origin.Implementation != p || origin.Name() != "xml2" || origin.Version != "1.2" || len(origin.Members) != len(functions.XML2Members) || slot >= len(xml2Bodies) || xml2Bodies[slot] == nil || d.DeclarationBody != xml2Bodies[slot] {
		return false
	}
	member := functions.XML2Members[slot]
	return member.Kernel >= 0 && d.RoutineKind == catalog.FunctionRoutine && d.Signature == catalog.ProcedureSignature(member.Signature) && d.Result == ids.OID(member.Result) && d.Strict && d.Volatility == member.Volatility && !d.SecurityDefiner && d.Configuration.Valid() &&
		d.Installed == (catalog.InstalledRoutineDeclaration{Parallel: 's'}) && d.Display == xml2Display(slot) && d.SQL == nil && d.SQLBody == nil && d.Builtin == nil && d.Foreign == nil && d.Host == nil && d.HostHandler == nil
}

func (p *XML2Provider) VisitProcedureDependencies(_ catalog.Read, r *catalog.Procedure, yield func(ids.ObjectAddress) bool) bool {
	if !p.SupportsRoutineConfiguration(r) {
		return true
	}
	member := functions.XML2Members[r.Origin.Declaration]
	return yield(r.Origin.Installation.Members[member.Kernel])
}

// XML2Kernel follows only the sealed structural body's authentic member slot.
// It grants no EXECUTE authority and remains valid after ordinary detach/rename.
func XML2Kernel(v catalog.Read, wrapper *catalog.Procedure) (*catalog.Procedure, error) {
	if _, err := ProcedureSymbol(v, wrapper); err != nil {
		return nil, err
	}
	if !StockXML2.SupportsRoutineConfiguration(wrapper) {
		return nil, fmt.Errorf("stuntgres: invalid xml2 SQL wrapper")
	}
	member := functions.XML2Members[wrapper.Origin.Declaration]
	p, ok := v.Procedure(wrapper.Origin.Installation.Members[member.Kernel])
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	if _, err := ProcedureSymbol(v, p); err != nil {
		return nil, err
	}
	if p.Implementation != StockXML2 || p.Origin.Declaration != member.Kernel || p.Origin.Installation.Cohort != wrapper.Origin.Installation.Cohort {
		return nil, fmt.Errorf("stuntgres: foreign xml2 kernel")
	}
	return p, nil
}
