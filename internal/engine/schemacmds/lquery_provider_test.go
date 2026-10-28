package schemacmds_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func lqueryProviderStage(t *testing.T, e *core.Engine, op *session.Operation) *core.CatalogStage {
	t.Helper()
	e.Mu.Lock()
	stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op:op}, &parser.CreateExtension{Name:parser.Identifier{Text:"ltree"}})
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ e.DiscardCatalogStage(stage) })
	return stage
}

func lqueryProviderInstalled(t *testing.T, displace bool) (*core.Engine, *session.Operation, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t,e,"postgres")
	t.Cleanup(done)
	if displace {
		e.Mu.Lock()
		_, err := e.Catalog.AllocateOIDLocked()
		e.Mu.Unlock()
		if err != nil { t.Fatal(err) }
	}
	stage := lqueryProviderStage(t,e,op)
	publication, err := e.ValidateCatalogStage(context.Background(),stage)
	if err != nil { t.Fatal(err) }
	if _, err := e.PublishCatalogOperation(op,nil,publication,core.PublicationToken{Epoch:stage.Base.Epoch()}); err != nil { t.Fatal(err) }
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"ltree"}))
	return e,op,ext,view.MustType(ext.Members[0])
}

func TestLqueryProviderIdentity(t *testing.T) {
	e,_,ext,base := lqueryProviderInstalled(t,false)
	view := e.CatalogRead()
	if err := extension.StockLtree.ValidateExtension(view,ext); err != nil { t.Fatal(err) }
	if ext.Version != "1.3" || len(ext.Members) != extension.LtreeMemberCount || !base.SQLType.IsLquery() || base.Owner != ext.Owner { t.Fatal("installed scalar manifest") }
	array, ok := view.LookupArray(base.SQLType)
	if !ok || !array.Concrete() || array.Element() != base.SQLType || array.ID() != base.Array { t.Fatal("concrete companion") }
	if err := (stored.TypeProvider{}).Validate(view,base); err != nil { t.Fatal(err) }
	for i, name := range []string{"lquery_in","lquery_out"} {
		proc := view.MustProcedure(ext.Members[i+1])
		symbol, err := extension.ProcedureSymbol(view,proc)
		if err != nil || symbol != name || extension.LtreeProviderOf(proc) != extension.StockLtree { t.Fatal("codec authority",symbol,err) }
		if err := extension.StockLtree.ValidateProcedure(view,proc); err != nil { t.Fatal(err) }
	}
	roles, err := extension.InstalledTypes(view,ext)
	if err != nil || len(roles) != 3 || roles[0].Name != "lquery" || roles[0].Type != base || roles[1].Name != "ltree" || !roles[1].Type.SQLType.IsLtree() || roles[2].Name != "ltxtquery" || !roles[2].Type.SQLType.IsLtxtquery() { t.Fatal("symbolic type roles",roles,err) }
	metadata, err := extension.OwnerMetadata(view,e.RoleStore.PublishedRead(&e.Catalog.Owner,view),ext)
	if err != nil { t.Fatal(err) }
	var records []extension.OwnerRecord
	_, err = parser.ParseWithOptions("CREATE EXTENSION ltree;\n"+metadata,parser.Options{
		Context:context.Background(), StandardConformingStrings:true,
		LineComment:func(comment string,_ parser.Position) error {
			record,found,err := extension.DecodeOwners(comment)
			if err == nil && found { records=append(records,record) }
			return err
		},
	})
	if err != nil || len(records) != 1 { t.Fatal("owner metadata decoding",records,err) }
	record := records[0]
	if record.Name != "ltree" || record.Version != "1.3" || record.Schema != "public" || record.Owner != "postgres" || len(record.Members) != extension.LtreeMemberCount { t.Fatal("owner recipe",record) }
	for _, member := range record.Members {
		if member.Owner != "postgres" { t.Fatal("member owner",member) }
		switch member.Slot {
		case 0:
			if member.Kind != "type" || member.Name != "lquery" || member.Signature != "" { t.Fatal("scalar owner role",member) }
		case 1:
			if member.Kind != "procedure" || member.Name != "lquery_in" || member.Signature != "2275" { t.Fatal("input owner role",member) }
		case 2:
			if member.Kind != "procedure" || member.Name != "lquery_out" || member.Signature != "lquery" { t.Fatal("output owner role",member) }
		case 3:
			if member.Kind != "type" || member.Name != "ltree" || member.Signature != "" { t.Fatal("scalar owner role",member) }
		case 4:
			if member.Kind != "procedure" || member.Name != "ltree_in" || member.Signature != "2275" { t.Fatal("input owner role",member) }
		case 5:
			if member.Kind != "procedure" || member.Name != "ltree_out" || member.Signature != "ltree" { t.Fatal("output owner role",member) }
		case 6:
			if member.Kind != "type" || member.Name != "ltxtquery" || member.Signature != "" { t.Fatal("scalar owner role",member) }
		case 7:
			if member.Kind != "procedure" || member.Name != "ltxtq_in" || member.Signature != "2275" { t.Fatal("input owner role",member) }
		case 8:
			if member.Kind != "procedure" || member.Name != "ltxtq_out" || member.Signature != "ltxtquery" { t.Fatal("output owner role",member) }
		case extension.LtreeBtreeFamilySlot:
			if member.Kind!="operator_family"||member.Name!="ltree_ops"||member.Signature!="btree"{t.Fatal("B-tree family owner role",member)}
		case extension.LtreeBtreeClassSlot:
			if member.Kind!="operator_class"||member.Name!="ltree_ops"||member.Signature!="btree"{t.Fatal("B-tree class owner role",member)}
		case extension.LtreeHashFamilySlot:
			if member.Kind!="operator_family"||member.Name!="hash_ltree_ops"||member.Signature!="hash"{t.Fatal("hash family owner role",member)}
		case extension.LtreeHashClassSlot:
			if member.Kind!="operator_class"||member.Name!="hash_ltree_ops"||member.Signature!="hash"{t.Fatal("hash class owner role",member)}
		default:
			switch{
			case member.Slot>=extension.LtreeHierarchyProcedureStart&&member.Slot<extension.LtreeHierarchyOperatorStart:
				name:=[]string{"ltree_isparent","ltree_risparent"}[member.Slot-extension.LtreeHierarchyProcedureStart]
				if member.Kind!="procedure"||member.Name!=name||member.Signature!="ltree,ltree"{t.Fatal("hierarchy procedure owner role",member)}
   case member.Slot>=extension.LtreeHierarchyOperatorStart&&member.Slot<extension.LtreeHierarchyMemberEnd:
				name:=[]string{"@>","<@"}[member.Slot-extension.LtreeHierarchyOperatorStart]
				if member.Kind!="operator"||member.Name!=name||member.Signature!="ltree,ltree"{t.Fatal("hierarchy operator owner role",member)}
   case member.Slot>=extension.LtreePathProcedureStart&&member.Slot<extension.LtreePathOperatorStart:
    index:=member.Slot-extension.LtreePathProcedureStart
    name:=[]string{"subltree","subpath","subpath","index","index","nlevel","ltree2text","text2ltree","lca","lca","lca","lca","lca","lca","lca","lca","ltree_addltree","ltree_addtext","ltree_textadd"}[index]
    signature:=[]string{
     "ltree,23,23","ltree,23,23","ltree,23","ltree,ltree","ltree,ltree,23",
     "ltree","ltree","25","ltree[]","ltree,ltree","ltree,ltree,ltree",
     "ltree,ltree,ltree,ltree","ltree,ltree,ltree,ltree,ltree",
     "ltree,ltree,ltree,ltree,ltree,ltree","ltree,ltree,ltree,ltree,ltree,ltree,ltree",
     "ltree,ltree,ltree,ltree,ltree,ltree,ltree,ltree","ltree,ltree","ltree,25","25,ltree",
    }[index]
    if member.Kind!="procedure"||member.Name!=name||member.Signature!=signature{t.Fatal("path procedure owner role",member)}
   case member.Slot>=extension.LtreePathOperatorStart&&member.Slot<extension.LtreeMemberCount:
    signature:=[]string{"ltree,ltree","ltree,25","25,ltree"}[member.Slot-extension.LtreePathOperatorStart]
    if member.Kind!="operator"||member.Name!="||"||member.Signature!=signature{t.Fatal("path operator owner role",member)}
			case member.Slot>=extension.LtreeComparisonProcedureStart&&member.Slot<extension.LtreeComparisonOperatorStart:
				index:=member.Slot-extension.LtreeComparisonProcedureStart
				name:=[]string{"ltree_eq","ltree_ne","ltree_lt","ltree_le","ltree_gt","ltree_ge","ltree_cmp","hash_ltree","hash_ltree_extended"}[index]
				signature:="ltree,ltree";if index==7{signature="ltree"};if index==8{signature="ltree,20"}
				if member.Kind!="procedure"||member.Name!=name||member.Signature!=signature{t.Fatal("comparison procedure owner role",member)}
			case member.Slot>=extension.LtreeComparisonOperatorStart&&member.Slot<extension.LtreeBtreeFamilySlot:
				name:=[]string{"=","<>","<","<=",">",">="}[member.Slot-extension.LtreeComparisonOperatorStart]
				if member.Kind!="operator"||member.Name!=name||member.Signature!="ltree,ltree"{t.Fatal("comparison operator owner role",member)}
			default:
				if member.Slot < extension.LtreeArrayProcedureStart || member.Slot >= extension.LtreeComparisonProcedureStart || member.Kind != "procedure" && member.Kind != "operator" { t.Fatal("unrecognized owner slot",member) }
			}
		}
	}
	original := slices.Clone(ext.Members)
	for _, corrupt := range []func(*catalog.State,*catalog.Extension){
		func(_ *catalog.State,d *catalog.Extension){d.Members[1]=d.Members[0]},
		func(_ *catalog.State,d *catalog.Extension){d.Version="1.2"},
		func(s *catalog.State,d *catalog.Extension){s.RemoveDescriptor(d.Members[1])},
		func(s *catalog.State,d *catalog.Extension){p:=*s.MustProcedure(d.Members[1]); p.Result++; s.SetDescriptor(p.Address(),&p)},
		func(s *catalog.State,d *catalog.Extension){p:=*s.MustProcedure(d.Members[2]); p.Signature="25"; s.SetDescriptor(p.Address(),&p)},
		func(s *catalog.State,d *catalog.Extension){p:=*s.MustProcedure(d.Members[1]); p.Implementation=extension.StockIntarray; s.SetDescriptor(p.Address(),&p)},
		func(s *catalog.State,d *catalog.Extension){p:=*s.MustProcedure(d.Members[1]); p.Strict=false; s.SetDescriptor(p.Address(),&p)},
		func(s *catalog.State,d *catalog.Extension){p:=*s.MustProcedure(d.Members[1]); p.Extension=ids.ObjectAddress{}; s.SetDescriptor(p.Address(),&p)},
		func(s *catalog.State,d *catalog.Extension){p:=*s.MustProcedure(d.Members[2]); p.Display.Source="lquery_in"; s.SetDescriptor(p.Address(),&p)},
		func(s *catalog.State,d *catalog.Extension){b:=*s.MustType(d.Members[0]); b.Array=0; s.SetDescriptor(b.Address(),&b)},
		func(s *catalog.State,d *catalog.Extension){b:=*s.MustType(d.Members[0]); b.SQLType=lqueryProviderForeign(t,b.SQLType); s.SetDescriptor(b.Address(),&b)},
		func(s *catalog.State,_ *catalog.Extension){a:=*s.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(base.Array)}); a.SQLType=sqltype.LookupArray(a.SQLType.ID(),a.Name(),lqueryProviderForeign(t,base.SQLType)); s.SetDescriptor(a.Address(),&a)},
	} {
		candidate := catalog.CloneState(view.State())
		bad := *ext; bad.Members=slices.Clone(ext.Members)
		candidate.SetDescriptor(bad.Address(),&bad)
		corrupt(candidate,&bad)
		if err := extension.StockLtree.ValidateExtension(e.Catalog.Read(candidate),&bad); err == nil { t.Fatal("corrupt provider accepted") }
		if !slices.Equal(original,ext.Members) || view.MustType(base.Address()) != base { t.Fatal("validation mutated published graph") }
	}
	if err := extension.StockLtree.ValidateExtension(view,ext); err != nil { t.Fatal("original damaged",err) }
}

func lqueryProviderForeign(t *testing.T, original *sqltype.Descriptor) *sqltype.Descriptor {
	t.Helper()
	d,err := sqltype.Lquery(original.ID(),original.Name(),original.SQLName())
	if err != nil {t.Fatal(err)}
	return d
}

func TestLqueryProviderPublication(t *testing.T) {
	e,op,installed,base := lqueryProviderInstalled(t,false)
	before := e.CatalogRead()
	// A genuinely staged second installation fails its occupied type check and
	// never publishes a prefix over the already installed descriptors.
	e.Mu.Lock()
	_,err := schemacmds.StageExtensionLocked(e,&core.StatementScope{Op:op},&parser.CreateExtension{Name:parser.Identifier{Text:"ltree"}})
	e.Mu.Unlock()
	if err == nil || e.CatalogRead().State() != before.State() || e.CatalogRead().MustType(base.Address()) != base { t.Fatal("failed install changed original",err) }
	if err := extension.StockLtree.ValidateExtension(before,installed); err != nil { t.Fatal(err) }
	fresh := engine.New()
	freshOp,done := enginetest.RoleOperation(t,fresh,"postgres"); defer done()
	stage := lqueryProviderStage(t,fresh,freshOp)
	candidate := fresh.StageRead(stage)
	ext := candidate.MustExtension(candidate.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"ltree"}))
	// This descriptor is owned exclusively by the unpublished stage.
	candidate.MustProcedure(ext.Members[2]).Result = 25
	original := fresh.CatalogRead().State()
	if _,err := fresh.ValidateCatalogStage(context.Background(),stage); err == nil { t.Fatal("invalid candidate published") }
	if fresh.CatalogRead().State() != original || fresh.CatalogRead().NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"ltree"}) != (ids.ObjectAddress{}) { t.Fatal("failed staged validation published a prefix") }
}

func TestLqueryProviderRemap(t *testing.T) {
	source,_,ext,base := lqueryProviderInstalled(t,false)
	target,_,destination,destBase := lqueryProviderInstalled(t,true)
	if base.Address() == destBase.Address() { t.Fatal("destination allocator not displaced") }
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address():destination.Address(),base.Address():destBase.Address(),{ClassID:ids.TypeClassID,ObjectID:ids.OID(base.Array)}:{ClassID:ids.TypeClassID,ObjectID:ids.OID(destBase.Array)}}
	for i := 1; i < len(ext.Members); i++ { mapping[ext.Members[i]]=destination.Members[i] }
	sourceTree := source.CatalogRead().MustType(ext.Members[3])
	targetTree := target.CatalogRead().MustType(destination.Members[3])
	mapping[ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(sourceTree.Array)}] = ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(targetTree.Array)}
	sourceText := source.CatalogRead().MustType(ext.Members[6])
	targetText := target.CatalogRead().MustType(destination.Members[6])
	mapping[ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(sourceText.Array)}] = ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(targetText.Array)}
	originalMembers := slices.Clone(ext.Members)
	mapped := extension.RemapDescriptor(ext,mapping).(*catalog.Extension)
	if !slices.Equal(mapped.Members,destination.Members) || !slices.Equal(ext.Members,originalMembers) { t.Fatal("manifest remap") }
	for i := 1; i < len(ext.Members); i++ {
		if ext.Members[i].ClassID != ids.ProcedureClassID { continue }
		old := source.CatalogRead().MustProcedure(ext.Members[i])
		next := extension.RemapDescriptor(old,mapping).(*catalog.Procedure)
		want := target.CatalogRead().MustProcedure(destination.Members[i])
		if next.Address() != want.Address() || next.Extension != want.Extension || next.Signature != want.Signature || next.Result != want.Result || next.Implementation != extension.StockLtree { t.Fatal("codec remap",next,want) }
	}
	candidate := catalog.CloneState(source.CatalogRead().State())
	if err := typedef.ImportScalarTypesLocked(candidate,mapping,func()(int64,error){t.Fatal("lquery allocated enum member");return 0,nil}); err != nil { t.Fatal(err) }
	if _,err := typedef.ReownDomainTypes(candidate,mapping,true,routine.RangeTypeRebinder(candidate,mapping)); err != nil { t.Fatal(err) }
	typedef.RefreshScalarImportDestinations(candidate)
	imported := candidate.MustType(base.Address())
	if !imported.SQLType.IsLquery() || imported.SQLType == base.SQLType || imported.SQLType.ID() != destBase.SQLType.ID() || imported.Array != destBase.Array { t.Fatal("fresh imported scalar",imported) }
	array := candidate.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(base.Array)})
	if array.SQLType.Element() != imported.SQLType || !array.SQLType.Concrete() || array.SQLType.ID() != destBase.Array { t.Fatal("imported companion graph") }
	// Use the source-keyed import authority at its final target. No conversion or
	// text re-input is permitted during stored datum reownership.
	imports := candidate.ScalarImports()
	if imports[base.SQLType] != imported.SQLType { t.Fatal("source-authorized import map not refreshed") }
	value,err := base.SQLType.Input(context.Background(),"Top.*{0,2}.sport*@.!football|tennis{1,}.Russ*|Spain",-1)
	if err != nil { t.Fatal(err) }
	for _,old := range []sqltype.Value{value,sqltype.Null(base.SQLType)} {
		moved,err := typedef.ReownDomainDatum(old,imported.SQLType,nil,imports,nil)
		if err != nil || moved.Type() != imported.SQLType || moved.IsNull() != old.IsNull() { t.Fatal("datum import",err) }
		back,err := moved.RemapLquery(base.SQLType)
		if err != nil || back != old { t.Fatal("immutable payload not shared",err) }
		if _,err := old.Retag(imported.SQLType); err == nil {t.Fatal("ordinary retag laundered old generation")}
	}
	text,err := base.SQLType.Output(context.Background(),value)
	if err != nil || text != "Top.*{,2}.sport@*.!football|tennis{1,}.Russ*|Spain" || source.CatalogRead().MustType(base.Address()) != base { t.Fatal("retained source descriptor",text,err) }
}
