package enginetests_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestInfoReferentialConstraintsProducer(t *testing.T) {
	f:=newDefaultUnitFixture(t)
	state:=catalog.CloneState(f.e.Catalog.State)
	address:=func(id ids.OID)ids.ObjectAddress{return ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:id}}
	integer,_:=f.e.CatalogRead().LookupType(sqltype.Int4)
	parent:=&catalog.Relation{ObjectAddress:address(24500),ObjectNamespace:f.schema,ObjectName:"same",ObjectRevision:1,Owner:f.reader,RelationKind:catalog.TableRelation}
	parent.Constraints.Unique=[]catalog.UniqueConstraint{{OID:24600,Name:"misleading",Index:address(24502)},{OID:24601,Name:"actual",Index:address(24501),Primary:true}}
	state.SetDescriptor(parent.Address(),parent)
	for _,id:=range []ids.OID{24501,24502,24503} {
		index:=&catalog.Relation{ObjectAddress:address(id),ObjectNamespace:f.schema,ObjectName:"same",ObjectRevision:1,Owner:f.reader,SQLIndex:&catalog.IndexDefinition{Parent:parent.Address(),Unique:true,Keys:[]catalog.IndexKey{{Attribute:1}}}}
		state.SetDescriptor(index.Address(),index)
	}
	other:=*parent;other.ObjectAddress=address(24504);other.Constraints.Unique=[]catalog.UniqueConstraint{{OID:24602,Name:"same",Index:address(24505)}};state.SetDescriptor(other.Address(),&other)
	state.SetDescriptor(address(24505),&catalog.Relation{ObjectAddress:address(24505),ObjectNamespace:f.schema,ObjectName:"actual",Owner:f.reader,SQLIndex:&catalog.IndexDefinition{Parent:other.Address(),Unique:true}})
	child:=&catalog.Relation{ObjectAddress:address(24510),ObjectNamespace:f.schema,ObjectName:"child",ObjectRevision:1,Owner:f.owner,RelationKind:catalog.TableRelation,Columns:[]catalog.Column{{Name:"pid",Usage:integer.Usage()},{Name:"extra",Usage:integer.Usage()}},Attributes:[]int32{1,2}}
	child.Security.ACL=authorization.ACLValue{Explicit:true}
	child.Constraints.ForeignKeys=[]catalog.ForeignKeyConstraint{
		{OID:24700,Name:"duplicate",Target:parent.Address(),Index:address(24501)},
		{OID:24701,Name:"restrict_fk",Target:parent.Address(),Index:address(24501)},
		{OID:24702,Name:"standalone",Target:parent.Address(),Index:address(24503)},
		{OID:24703,Name:"other",Target:other.Address(),Index:address(24505)},
		{OID:24704,Name:"full",Target:parent.Address(),Index:address(24502),Full:true,NotValid:true,Attributes:[]int32{1,2},Referenced:[]int32{1,2}},
	}
	actions:=[]parser.ForeignKeyAction{parser.ForeignKeyNoAction,parser.ForeignKeyRestrict,parser.ForeignKeyCascade,parser.ForeignKeySetNull,parser.ForeignKeySetDefault}
	labels:=[]string{"NO ACTION","RESTRICT","CASCADE","SET NULL","SET DEFAULT"}
	for i:=range actions {child.Constraints.ForeignKeys[i].UpdateAction=actions[i];child.Constraints.ForeignKeys[i].DeleteAction=actions[(i+1)%5]}
	state.SetDescriptor(child.Address(),child)
	input:=func(s *catalog.State,actor authorization.PrincipalRef)pgcatalog.Input{
		view:=f.e.Catalog.Read(s);auth:=dtpUnitAuthority(t,f,view);auth.State.CurrentUser=actor
		source:=view.MustRelation(address(pgcatalog.InfoReferentialConstraintsOID))
		return pgcatalog.Input{Context:t.Context(),Owner:view.Owner(),Catalog:view,Roles:auth.Roles,Ref:view.Owner().Domain.Ref(view.Epoch(),source.Address()),Source:source,Runtime:core.EngineCatalogRuntime{Auth:auth,Database:"captured_database"}}
	}
	produce,ok:=pgcatalog.ProducerFor(pgcatalog.InfoReferentialConstraintsOID);if !ok{t.Fatal("missing producer")}
	in:=input(state,f.owner)
	names:=[]string{"constraint_catalog","constraint_schema","constraint_name","unique_constraint_catalog","unique_constraint_schema","unique_constraint_name","match_option","update_rule","delete_rule"}
	source:=in.Source
	if source.Name()!="referential_constraints"||source.Namespace().ObjectID!=13229||!source.VirtualView||source.RowType!=13362{t.Fatal("source identity",source)}
	row:=in.Catalog.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:13362});array:=in.Catalog.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:13361})
	if row.OwningRelation!=source.Address()||row.Array!=13361||array.SQLType.Element()!=row.SQLType||array.OwningRelation!=source.Address(){t.Fatal("row/array ownership")}
	readerAuth:=input(state,f.reader).Runtime.(core.EngineCatalogRuntime)
	rights,err:=readerAuth.Effective(in.Ref,false);if err!=nil||rights&authorization.SelectPrivilege==0{t.Fatal("PUBLIC SELECT",rights,err)}
	for i,column:=range source.Columns{
		id,base:=sqltype.ID(13248),sqltype.Name;if i>=6{id,base=13246,sqltype.VarChar}
		domain,_:=in.Catalog.LookupType(id);physical,_:=in.Catalog.LookupType(base)
		if column.Name!=names[i]||column.Usage.Type!=domain||domain.BaseType()!=physical||!column.Nullable||!column.NullabilityKnown||column.Usage.Typmod != -1||column.Usage.Collation.ID!=950||column.DeclaredDimensions!=0||source.Attributes[i]!=int32(i+1)||column.OriginTable!=16239||column.OriginAttribute!=int16(i+1){t.Fatal("column authority",i,column)}
	}
	bag:=func(rows [][]sqltype.Value)map[[9]string]int{
		out:=map[[9]string]int{}
		for _,row:=range rows {if len(row)!=9{t.Fatal("width")};var key [9]string
			for i,value:=range row {if value.Type()!=source.Columns[i].Usage.Type||!value.ValidResult(source.Columns[i].Usage){t.Fatal("cell authority",i)};if value.IsNull(){key[i]="<NULL>"}else{base,err:=value.BaseValue();if err!=nil{t.Fatal(err)};key[i],err=base.Text();if err!=nil{t.Fatal(err)}}};out[key]++}
		return out
	}
	want:=map[[9]string]int{}
	for i,fk:=range child.Constraints.ForeignKeys {
		j:=i
		unique,match:="actual","NONE";if j==3{unique="same"};if j==4{unique,match="misleading","FULL"}
		key:=[9]string{"captured_database","default_schema",fk.Name,"captured_database","default_schema",unique,match,labels[j],labels[(j+1)%5]}
		if j==2{key[3],key[4],key[5]="<NULL>","<NULL>","<NULL>"};want[key]++
	}
	check:=func(in pgcatalog.Input,want map[[9]string]int){t.Helper();captured,err:=core.CaptureCatalogSourceRows(in,produce);if err!=nil{t.Fatal(err)};defer captured.Close();if captured.Anonymous==nil||captured.View!=nil||captured.Ref!=in.Ref{t.Fatal("anonymous source identity")};if got:=bag(captured.Anonymous);!reflect.DeepEqual(got,want){t.Fatal("exact bag",got,want)}}
	for _,actor:=range []authorization.PrincipalRef{f.owner,f.member,f.op.Session.Authorization.State.CurrentUser}{check(input(state,actor),want)}
	check(input(state,f.reader),map[[9]string]int{})
	for _,privilege:=range []authorization.Privileges{authorization.SelectPrivilege,authorization.MaintainPrivilege,authorization.InsertPrivilege,authorization.UpdatePrivilege,authorization.DeletePrivilege,authorization.TruncatePrivilege,authorization.ReferencesPrivilege,authorization.TriggerPrivilege}{
		granted:=catalog.CloneState(state);copy:=*child;copy.Security.ACL=authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{defaultUnitReaderEntry(f,privilege,0)}};granted.SetDescriptor(copy.Address(),&copy)
		if privilege==authorization.SelectPrivilege||privilege==authorization.MaintainPrivilege{check(input(granted,f.reader),map[[9]string]int{})}else{check(input(granted,f.reader),want)}
	}
	for _,privilege:=range []authorization.Privileges{authorization.SelectPrivilege,authorization.InsertPrivilege,authorization.UpdatePrivilege,authorization.ReferencesPrivilege}{
		granted:=catalog.CloneState(state);copy:=*child;copy.Security.ColumnACL=map[int32]authorization.ACLValue{2:{Explicit:true,Entries:[]authorization.ACLEntry{defaultUnitReaderEntry(f,privilege,0)}}};granted.SetDescriptor(copy.Address(),&copy)
		if privilege==authorization.SelectPrivilege{check(input(granted,f.reader),map[[9]string]int{})}else{check(input(granted,f.reader),want)}
		copy.Attributes=[]int32{1,-2};granted=catalog.CloneState(state);granted.SetDescriptor(copy.Address(),&copy);check(input(granted,f.reader),map[[9]string]int{})
	}
	check(input(catalog.CloneState(f.e.Catalog.State),f.owner),map[[9]string]int{})
	// Same user name on another relation yields a second identical tuple.
	duplicates:=catalog.CloneState(state)
	sibling:=*child
	sibling.ObjectAddress=address(24511)
	sibling.ObjectName="sibling"
	key:=child.Constraints.ForeignKeys[0]
	key.OID=24705
	sibling.Constraints.ForeignKeys=[]catalog.ForeignKeyConstraint{key}
	duplicates.SetDescriptor(sibling.Address(),&sibling)
	duplicateWant:=make(map[[9]string]int,len(want))
	for key,count:=range want { duplicateWant[key]=count }
	duplicateWant[[9]string{"captured_database","default_schema","duplicate","captured_database","default_schema","actual","NONE","NO ACTION","RESTRICT"}]++
	check(input(duplicates,f.owner),duplicateWant)
}
