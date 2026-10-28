package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestAlterPersistencePreparedOwners(t *testing.T) {
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"CREATE TABLE t(id serial,note text)")
	enginetest.SessionSQL(t,s,"INSERT INTO t VALUES(7,'keep')")
	enginetest.SessionSQL(t,s,"BEGIN")
	cycle:=s.NewProtocolCycle()
	command:=func(fn func(*core.StatementScope) error) {preparedFoldProtocolCommand(t,e,s,cycle,fn)}
	query:="SELECT COALESCE(-(7::integer),0) AS folded,note FROM t"
	command(func(scope *core.StatementScope) error {_,err:=scope.ParseProtocol("persistence",query,nil);return err})
	registry:=core.PayloadOf(s).PreparedSQL
	definition:=registry.Lookup("persistence")
	bind:=func(name string) {command(func(scope *core.StatementScope) error {_,err:=scope.BindProtocolPortal(name,"persistence",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}});return err})}
	bind("before")
	owner:=definition.Description.Folds
	accepted:=owner.Current()
	analysis:=definition.Description.Analysis
	binding:=registry.Binding("before")
	root:=binding.Resource.Root
	if len(accepted.Entries)==0 || root==nil || analysis==nil {t.Fatal("missing genuine accepted definition/fold/graph")}
	entry:=&accepted.Entries[0]
	insertSQL:="INSERT INTO t VALUES(COALESCE(-(7::integer),0),'held')"
	command(func(scope *core.StatementScope) error {_,err:=scope.ParseProtocol("persistence_insert",insertSQL,nil);return err})
	bindInsert:=func(name string) {command(func(scope *core.StatementScope) error {_,err:=scope.BindProtocolPortal(name,"persistence_insert",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}});return err})}
	bindInsert("insert_before")
	insertBinding:=registry.Binding("insert_before")
	insertBindingAnalysis:=insertBinding.Entry.Description.Analysis
	if insertBindingAnalysis==nil || insertBinding.Folds==nil || len(insertBinding.Folds.Entries)==0 {t.Fatal("missing accepted INSERT analysis/folds")}
	insertBindingFolds:=*insertBinding.Folds
	insertBindingEntry:=&insertBindingFolds.Entries[0]
	insertDefinition:=registry.Lookup("persistence_insert")
	insertAnalysis:=insertDefinition.Description.Analysis
	insertOwner:=insertDefinition.Description.Folds
	insertAccepted:=insertOwner.Current()
	if len(insertAccepted.Entries)==0 {t.Fatal("missing actual INSERT accepted backing")}
	insertEntry:=&insertAccepted.Entries[0]
	enginetest.SessionSQL(t,s,"ALTER TABLE t SET LOGGED")
	bind("noop")
	current:=owner.Current()
	if definition.Description.Folds!=owner || definition.Description.Analysis!=analysis || current.Generation!=accepted.Generation || &current.Entries[0]!=entry {t.Fatal("no-op replaced accepted authority/backing")}
	bindInsert("insert_noop")
	insertCurrent:=insertOwner.Current()
	if insertDefinition.Description.Analysis!=insertAnalysis || insertCurrent.Generation!=insertAccepted.Generation || &insertCurrent.Entries[0]!=insertEntry {t.Fatal("no-op invalidated INSERT authority")}
	enginetest.SessionSQL(t,s,"SAVEPOINT before_bad")
	bad,badDescription:=sequencePreparedDescription(t,e,s,"ALTER TABLE t SET UNLOGGED, ADD CONSTRAINT bad CHECK(id<0)")
	if _,err:=sequencePreparedExecute(e,s,bad,&badDescription);err==nil {t.Fatal("failed rewrite accepted")}
	enginetest.SessionSQL(t,s,"ROLLBACK TO before_bad")
	current=owner.Current()
	if definition.Description.Analysis!=analysis || current.Generation!=accepted.Generation || &current.Entries[0]!=entry || binding.Resource.Root!=root || insertDefinition.Description.Analysis!=insertAnalysis {t.Fatal("rejected candidate published accepted authority")}
	enginetest.SessionSQL(t,s,"ALTER TABLE t SET UNLOGGED")
	bind("changed")
	bindInsert("insert_changed")
	changedInsert:=registry.Binding("insert_changed")
	changedSelect:=registry.Binding("changed")
	// Bind owns a private description; it never republishes portal analysis
	// into the named definition (protocol_portal.go). Inspect those real
	// accepted descriptions, retaining the earlier group independently.
	if changedInsert.Entry.Description.Analysis==insertBindingAnalysis || insertDefinition.Description.Analysis!=insertAnalysis || insertDefinition.Description.Folds!=insertOwner {t.Fatal("INSERT missing private binding invalidation or replaced definition authority")}
	if insertBinding.Entry.Description.Analysis!=insertBindingAnalysis || insertBinding.Folds.Generation!=insertBindingFolds.Generation || &insertBinding.Folds.Entries[0]!=insertBindingEntry || !reflect.DeepEqual(*insertBinding.Folds,insertBindingFolds) {t.Fatal("INSERT mutated retained accepted generation/backing")}
	if changedInsert.Folds==nil || changedInsert.Folds.Generation!=insertOwner.Current().Generation {t.Fatal("new INSERT binding did not accept its actual owner generation")}
	if changedSelect.Entry.Description.Analysis==binding.Entry.Description.Analysis || binding.Entry.Description.Analysis!=analysis {t.Fatal("SELECT missing private binding invalidation or mutated accepted analysis")}
	if binding.Resource.Root!=root || !reflect.DeepEqual(accepted.Entries[0],*entry) || binding.Folds.Generation!=accepted.Generation || &binding.Folds.Entries[0]!=entry {t.Fatal("reanalysis mutated retained portal immutable backing")}
	ast,d:=sequencePreparedDescription(t,e,s,"SELECT nextval('t_id_seq')")
	oldCell:=d.Sequences
	oldConstant:=&oldCell.Constants[0]
	oldStamp:=oldConstant.Stamp
	oldConstantValue:=*oldConstant
	enginetest.SessionSQL(t,s,"ALTER TABLE t SET LOGGED")
	r,err:=sequencePreparedExecute(e,s,ast,&d);if err!=nil {t.Fatal(err)};r.Rows.Close()
	// AcceptAnalysis preserves the mutable description cell while replacing
	// its captured group. The old constant backing must remain immutable.
	if d.Sequences!=oldCell || &d.Sequences.Constants[0]==oldConstant || d.Sequences.Constants[0].Stamp==oldStamp || d.Sequences.Constants[0].Ref!=oldConstantValue.Ref || !reflect.DeepEqual(*oldConstant,oldConstantValue) {t.Fatal("sequence rewrite failed group reanalysis or mutated retained backing")}
	enginetest.SessionSQL(t,s,"ROLLBACK")
}

func TestAlterPersistenceRejectedPublication(t *testing.T) {
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"CREATE TABLE t(id serial,note text)")
	enginetest.SessionSQL(t,s,"INSERT INTO t VALUES(7,'keep')")
	ast,d:=sequencePreparedDescription(t,e,s,"SELECT nextval('t_id_seq')")
	cell:=d.Sequences
	constant:=&cell.Constants[0]
	ref:=constant.Ref.Address
	state:=e.Catalog.State
	generator:=e.Storage.Sequences[ref]
	before:=*generator
	body,utility:=sequencePreparedDescription(t,e,s,"ALTER TABLE t SET UNLOGGED, ADD COLUMN added int DEFAULT 9, ADD CONSTRAINT bad CHECK(id<0)")
	if _,err:=sequencePreparedExecute(e,s,body,&utility);err==nil {t.Fatal("late failure accepted")}
	if e.Catalog.State!=state || e.Storage.Sequences[ref]!=generator || *generator!=before || d.Sequences!=cell || &cell.Constants[0]!=constant {t.Fatal("rejected candidate published catalog/generator/accepted owner")}
	r,err:=sequencePreparedExecute(e,s,ast,&d);if err!=nil {t.Fatal(err)};r.Rows.Close()
	if d.Sequences!=cell || &cell.Constants[0]!=constant {t.Fatal("rejected rewrite invalidated accepted analysis backing")}
}
