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

type PageinspectProvider struct{ control catalog.ExtensionControl }

var StockPageinspect = &PageinspectProvider{control: catalog.ExtensionControl{Version: "1.13", Superuser: true, Relocatable: true, Comment: new("inspect the contents of database pages at a low level")}}

func (p *PageinspectProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PageinspectProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.13"}}
}
func PageinspectProviderOf(p *catalog.Procedure) *PageinspectProvider {
	if p == nil {
		return nil
	}
	provider, _ := p.Implementation.(*PageinspectProvider)
	return provider
}

type PageinspectMember struct {
	Name, Source string
	Signature    catalog.ProcedureSignature
	Result       ids.OID
	Declaration  catalog.InstalledRoutineDeclaration
}

var PageinspectMembers = [...]PageinspectMember{
	{"brin_page_type", "brin_page_type", "17", 25, catalog.InstalledRoutineDeclaration{Parallel: 's', ArgumentNames: [4]string{"page"}}},
	{"brin_metapage_info", "brin_metapage_info", "17", 2249, catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: catalog.NewRoutineOutputs([]ids.OID{25, 23, 23, 20}, "page", "magic", "version", "pagesperrange", "lastrevmappage")}},
	{"brin_revmap_data", "brin_revmap_data", "17", 27, catalog.InstalledRoutineDeclaration{Parallel: 's', ReturnsSet: true, Rows: 1000, Outputs: catalog.NewRoutineOutputs([]ids.OID{27}, "page", "pages")}},
	{"brin_page_items", "brin_page_items", "17,2205", 2249, catalog.InstalledRoutineDeclaration{Parallel: 'r', ReturnsSet: true, Rows: 1000, Outputs: catalog.NewRoutineOutputs([]ids.OID{23, 20, 23, 16, 16, 16, 16, 25}, "page", "index_oid", "itemoffset", "blknum", "attnum", "allnulls", "hasnulls", "placeholder", "empty", "value")}},
	{"bt_page_items", "bt_page_items_bytea", "17", 2249, catalog.InstalledRoutineDeclaration{Parallel: 's', ReturnsSet: true, Rows: 1000, Outputs: catalog.NewRoutineOutputs([]ids.OID{21, 27, 21, 16, 16, 25, 16, 27, 1010}, "page", "itemoffset", "ctid", "itemlen", "nulls", "vars", "data", "dead", "htid", "tids")}},
	{"fsm_page_contents", "fsm_page_contents", "17", 25, catalog.InstalledRoutineDeclaration{Parallel: 's', ArgumentNames: [4]string{"page"}}},
	{"gin_metapage_info", "gin_metapage_info", "17", 2249, catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: catalog.NewRoutineOutputs([]ids.OID{20, 20, 23, 20, 20, 20, 20, 20, 20, 23}, "page", "pending_head", "pending_tail", "tail_free_size", "n_pending_pages", "n_pending_tuples", "n_total_pages", "n_entry_pages", "n_data_pages", "n_entries", "version")}},
	{"gin_page_opaque_info", "gin_page_opaque_info", "17", 2249, catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: catalog.NewRoutineOutputs([]ids.OID{20, 23, 1009}, "page", "rightlink", "maxoff", "flags")}},
	{"gin_leafpage_items", "gin_leafpage_items", "17", 2249, catalog.InstalledRoutineDeclaration{Parallel: 's', ReturnsSet: true, Rows: 1000, Outputs: catalog.NewRoutineOutputs([]ids.OID{27, 21, 1010}, "page", "first_tid", "nbytes", "tids")}},
}

func (p *PageinspectProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	if p != StockPageinspect || !currentManifest(v, ext) || ext.Name() != "pageinspect" || ext.Version != "1.13" || len(ext.Members) != len(PageinspectMembers) {
		return fmt.Errorf("stuntgres: invalid pageinspect manifest")
	}
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing pageinspect member")
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}
func (p *PageinspectProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing pageinspect installation")
	}
	if err := p.ValidateExtension(v, ext); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc {
		return fmt.Errorf("stuntgres: foreign pageinspect procedure")
	}
	return nil
}
func ValidatePageinspectIdentity(v catalog.Read, proc *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if v.Owner() == nil || !ok || proc == nil || actual != proc || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || PageinspectProviderOf(proc) != StockPageinspect {
		return fmt.Errorf("stuntgres: invalid stored pageinspect identity")
	}
	return StockPageinspect.ValidateProcedure(v, proc)
}
func (p *PageinspectProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, len(PageinspectMembers))
	for i, m := range PageinspectMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: m.Name, Signature: m.Signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: m.Result, Volatility: expr.Volatile, Strict: true, Installed: m.Declaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: m.Source}, Implementation: p}
		proc.Display.Binary, proc.Display.HasBinary = "$libdir/pageinspect", true
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		ext.Members[i] = address
	}
	return nil
}

func ValidatePageinspectCall(v catalog.Read, p *catalog.Procedure, call functions.Pageinspect) error {
	if p == nil || PageinspectProviderOf(p) != StockPageinspect || call.RoutineReference != p {
		return fmt.Errorf("stuntgres: invalid pageinspect call identity")
	}
	symbol, err := ProcedureSymbol(v, p)
	if err != nil {
		return err
	}
	if symbol != call.Member {
		return fmt.Errorf("stuntgres: invalid pageinspect call member")
	}
	if err := call.ValidateArguments(call.Args); err != nil {
		return err
	}
	usage := call.ResultUsage()
	outputs := p.OutputDeclaration()
	if p.Result != 2249 {
		d, ok := v.LookupType(sqltype.ID(p.Result))
		if !ok || usage != d.Usage() {
			return fmt.Errorf("stuntgres: invalid pageinspect scalar result")
		}
	} else {
		if usage.Type == nil || outputs == nil || usage.Type.CompositeLen() != outputs.Len() {
			return fmt.Errorf("stuntgres: invalid pageinspect record result")
		}
		for i := range outputs.Len() {
			m := usage.Type.CompositeMember(i)
			d, ok := v.LookupType(sqltype.ID(outputs.Type(i)))
			if !ok || m.Attribute != int32(i+1) || m.Name != outputs.Name(outputs.Inputs()+i) || m.Usage != d.Usage() {
				return fmt.Errorf("stuntgres: invalid pageinspect OUT field")
			}
		}
	}
	return nil
}
