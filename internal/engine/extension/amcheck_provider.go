package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const VerifyHeapamSignature catalog.ProcedureSignature = "2205,16,16,25,20,20"

var verifyHeapamOutputs = catalog.NewRoutineOutputs([]ids.OID{20, 23, 23, 25},
	"relation", "on_error_stop", "check_toast", "skip", "startblock", "endblock", "blkno", "offnum", "attnum", "msg")

var verifyHeapamDefaults = catalog.NewRoutineLiteralDefaults(
	catalog.RoutineLiteral{SourceType: 16, Text: "false"},
	catalog.RoutineLiteral{SourceType: 16, Text: "false"},
	catalog.RoutineLiteral{SourceType: 25, Text: "none"},
	catalog.RoutineLiteral{SourceType: 20, Null: true},
	catalog.RoutineLiteral{SourceType: 20, Null: true},
)

func verifyHeapamDeclaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 'u', ReturnsSet: true, Rows: 1000, Defaults: 5, Outputs: verifyHeapamOutputs}
}

// AmcheckMember is one released B-tree declaration. The installation reserves
// slot zero for verify_heapam; these declarations occupy slots one through seven.
// A mutable SQL name is not an implementation selector.
type AmcheckMember struct {
	Name        string
	Signature   catalog.ProcedureSignature
	Declaration catalog.InstalledRoutineDeclaration
}

var AmcheckMembers = [...]AmcheckMember{
	{"bt_index_check", "2205", amcheckDeclaration("index")},
	{"bt_index_check", "2205,16", amcheckDeclaration("index", "heapallindexed")},
	{"bt_index_check", "2205,16,16", amcheckDeclaration("index", "heapallindexed", "checkunique")},
	{"bt_index_parent_check", "2205", amcheckDeclaration("index")},
	{"bt_index_parent_check", "2205,16", amcheckDeclaration("index", "heapallindexed")},
	{"bt_index_parent_check", "2205,16,16", amcheckDeclaration("index", "heapallindexed", "rootdescend")},
	{"bt_index_parent_check", "2205,16,16,16", amcheckDeclaration("index", "heapallindexed", "rootdescend", "checkunique")},
}

func amcheckDeclaration(names ...string) catalog.InstalledRoutineDeclaration {
	d := catalog.InstalledRoutineDeclaration{Parallel: 'r'}
	copy(d.ArgumentNames[:], names)
	return d
}

type AmcheckProvider struct{ control catalog.ExtensionControl }

var StockAmcheck = &AmcheckProvider{control: catalog.ExtensionControl{
	Version: "1.5", Superuser: true, Relocatable: true,
	Comment: new("functions for verifying relation integrity"),
}}

func (p *AmcheckProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*AmcheckProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.5"}}
}
func AmcheckProviderOf(p *catalog.Procedure) *AmcheckProvider {
	if p == nil {
		return nil
	}
	provider, _ := p.Implementation.(*AmcheckProvider)
	return provider
}

func (p *AmcheckProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	if p != StockAmcheck || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "amcheck" || ext.Version != "1.5" || len(ext.Members) != 1+len(AmcheckMembers) {
		return fmt.Errorf("stuntgres: invalid amcheck manifest")
	}
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing amcheck member")
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}
func (p *AmcheckProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok || v.Get(proc.Address()) != proc {
		return fmt.Errorf("stuntgres: foreign amcheck procedure")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	for _, address := range ext.Members {
		if address == proc.Address() {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: foreign amcheck procedure")
}
func ValidateVerifyHeapamIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || AmcheckProviderOf(proc) != StockAmcheck {
		return fmt.Errorf("stuntgres: invalid stored verify_heapam identity")
	}
	if err := StockAmcheck.ValidateProcedure(v, proc); err != nil {
		return err
	}
	symbol, err := ProcedureSymbol(v, proc)
	if err != nil {
		return err
	}
	if symbol != "verify_heapam" {
		return fmt.Errorf("stuntgres: invalid stored verify_heapam identity")
	}
	return nil
}
func ValidateAmcheckIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	if v.Owner() == nil || proc == nil || v.Get(ref.Address) != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || AmcheckProviderOf(proc) != StockAmcheck {
		return fmt.Errorf("stuntgres: invalid stored amcheck identity")
	}
	if err := StockAmcheck.ValidateProcedure(v, proc); err != nil {
		return err
	}
	symbol, err := ProcedureSymbol(v, proc)
	if err != nil {
		return err
	}
	if symbol != "bt_index_check" && symbol != "bt_index_parent_check" {
		return fmt.Errorf("stuntgres: invalid stored amcheck identity")
	}
	return nil
}

// ValidateAmcheckCall validates the kernel shape against the exact selected
// member, independently of a wrapper's reference lifetime and EXECUTE check.
func ValidateAmcheckCall(v catalog.Read, proc *catalog.Procedure, call functions.Amcheck) error {
	symbol, err := ProcedureSymbol(v, proc)
	if err != nil {
		return err
	}
	result, ok := v.LookupType(sqltype.Void)
	if !ok || AmcheckProviderOf(proc) != StockAmcheck || symbol != "bt_index_check" && symbol != "bt_index_parent_check" || call.CallRoutineReference() != proc || call.ResultUsage().Type != result || call.Parent != (symbol == "bt_index_parent_check") {
		return fmt.Errorf("stuntgres: invalid installed amcheck call")
	}
	if _, err := call.Rebuild(expr.InfoOf(call), call.Args); err != nil {
		return err
	}
	count := 0
	for _, name := range proc.Installed.ArgumentNames {
		if name != "" {
			count++
		}
	}
	if count != len(call.Args) {
		return fmt.Errorf("stuntgres: mismatched amcheck overload")
	}
	return nil
}
func (p *AmcheckProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	for slot := 0; slot <= len(AmcheckMembers); slot++ {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, RoutineKind: catalog.FunctionRoutine, Owner: owner, Volatility: expr.Volatile, Implementation: p}
		if slot == 0 {
			proc.ObjectName, proc.Signature, proc.Result = "verify_heapam", VerifyHeapamSignature, 2249
			proc.Installed = verifyHeapamDeclaration()
			proc.LiteralDefaults = verifyHeapamDefaults
		} else {
			member := AmcheckMembers[slot-1]
			proc.ObjectName, proc.Signature, proc.Result = member.Name, member.Signature, 2278
			proc.Strict, proc.Installed = true, member.Declaration
		}
		proc.Display = catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: proc.Name()}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		proc.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege}}}
		if err := stage.Create(proc); err != nil {
			return err
		}
		ext.Members = append(ext.Members, address)
	}
	return nil
}
