package tablecmds_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestAlterAccessMethodPreparation(t *testing.T) {
	e:=engine.New();s:=enginetest.StartSession(t,e,nil)
	for _,q:=range []string{"CREATE TABLE t(id int)","CREATE ACCESS METHOD heap2 TYPE TABLE HANDLER heap_tableam_handler"} {enginetest.SessionSQL(t,s,q)}
	read:=e.CatalogRead();original:=read.MustRelation(roleTableRef(t,e,"t").Address)
	noop,err:=table.PrepareAccessMethod(read,original,parser.AlterTableAction{Action:parser.SetAccessMethod,Method:parser.Identifier{Text:"heap"}},"heap",table.AccessMethodPreparation{})
	if err!=nil || noop.Changed {t.Fatal(noop,err)}
	change,err:=table.PrepareAccessMethod(read,original,parser.AlterTableAction{Action:parser.SetAccessMethod,Method:parser.Identifier{Text:"heap2"}},"heap",noop)
	if err!=nil || !change.Changed || change.Method==2 {t.Fatal(change,err)}
	_,err=table.PrepareAccessMethod(read,original,parser.AlterTableAction{Action:parser.SetAccessMethod,Method:parser.Identifier{Text:"missing_am"}},"heap",change)
	var diagnostic *sqlstate.Error
	if !errors.As(err,&diagnostic) || diagnostic.Code!="0A000" || diagnostic.Message!="cannot have multiple SET ACCESS METHOD subcommands" {t.Fatal(err)}
	root:=*original;root.RelationKind=catalog.PartitionedRelation;root.TableMethod=2
	cleared,err:=table.PrepareAccessMethod(read,&root,parser.AlterTableAction{Action:parser.SetAccessMethod,MethodDefault:true},"heap2",table.AccessMethodPreparation{})
	if err!=nil || !cleared.Changed || cleared.Method!=0 {t.Fatal(cleared,err)}
	for _,only:=range []bool{false,true} {if table.InheritanceRecursiveAction(original,parser.AlterTableAction{Action:parser.SetAccessMethod},only) {t.Fatal("method intent propagated")}}
}

func TestAlterAccessMethodPartitionedIndexEligibility(t *testing.T) {
	// Public partitioned-index construction is unavailable. Retain genuine
	// partition keys, index method/keys and identity from existing constructors,
	// changing only the private index's parent to exercise kind admission.
	e:=engine.New();s:=enginetest.StartSession(t,e,nil)
	for _,q:=range []string{"CREATE TABLE parts(id int) PARTITION BY RANGE(id)","CREATE TABLE t(id int)","CREATE INDEX part_idx ON t(id)"} {enginetest.SessionSQL(t,s,q)}
	base:=e.CatalogRead()
	parent:=base.MustRelation(roleTableRef(t,e,"parts").Address)
	idx:=*base.MustRelation(roleTableRef(t,e,"part_idx").Address)
	definition:=*idx.SQLIndex;definition.Parent=parent.Address();idx.SQLIndex=&definition
	state:=catalog.CloneState(base.State());state.SetDescriptor(idx.Address(),&idx)
	read:=base.WithState(state)
	op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
	scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)};defer scope.Finish(nil)
	e.Mu.Lock()
	auth:=enginetest.CatalogAuthorization(e,read)
	_,err=tablecmds.PrepareAlterTableActions(e,scope,op,auth,&idx,e.Catalog.Domain.Ref(read.Epoch(),idx.Address()),[]parser.AlterTableAction{{Action:parser.SetAccessMethod,Method:parser.Identifier{Text:"heap"}}})
	e.Mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err,&diagnostic) || diagnostic.Code!="42809" || diagnostic.Message!=`ALTER action SET ACCESS METHOD cannot be performed on relation "part_idx"` || diagnostic.Detail!="This operation is not supported for partitioned indexes." || diagnostic.Hint!="" || diagnostic.Position!="" {t.Fatal(err)}
}

func TestAlterAccessMethodRootReentry(t *testing.T) {
	e,parent,child:=inheritanceDMLPublishedGraph(t)
	s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"CREATE ACCESS METHOD heap2 TYPE TABLE HANDLER heap_tableam_handler")
	command:=inheritanceAlterAdmissionScope(t,e,"postgres","ALTER TABLE p SET ACCESS METHOD heap2, ADD COLUMN method_extra int")
	holder:=inheritanceAlterAXHolder(t,e,child)
	result,joined:=inheritanceAlterWorker(t,command,holder)
	state:=inheritanceAlterPending(t,command,child)
	method,_:=e.CatalogRead().LookupAccessMethod("heap2")
	if !state.AccessMethod.Changed || state.AccessMethod.Method!=method.Address().ObjectID || state.Root.Address()!=parent.Address() {t.Fatal("root lost original method intent",state.AccessMethod)}
	holder.Finish(nil)
	if err:=inheritanceAlterJoin(t,result,joined);err!=nil {t.Fatal(err)}
	if err:=command.scope.Finish(nil);err!=nil {t.Fatal(err)}
	if e.CatalogRead().MustRelation(parent.Address()).TableMethod!=method.Address().ObjectID || e.CatalogRead().MustRelation(child.Address()).TableMethod!=child.TableMethod {t.Fatal("root-only intent lost on reentry")}
}
