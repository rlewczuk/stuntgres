package sequence

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// A nil Stage is a stage without sequence work: every read answers empty.
// The first identity capture of a sequence is the one publication validates.
func TestSequenceStageSlot(t *testing.T) {
	var empty *Stage
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90030}
	if empty.Generator(address) != nil || empty.StagedGenerators() != nil || empty.IdentityOriginalMap() != nil || empty.IdentityDependencyList() != nil {
		t.Fatal("nil stage read")
	}
	if _, ok := empty.IdentityOriginal(address); ok {
		t.Fatal("nil stage capture")
	}
	empty.DeleteGenerator(address)
	f := newPreparedFixture(t)
	s := &Stage{}
	g := &storage.SequenceGenerator{Last: 3}
	s.SetGenerator(address, g)
	if s.Generator(address) != g || len(s.StagedGenerators()) != 1 {
		t.Fatal("staged generator")
	}
	s.DeleteGenerator(address)
	if s.Generator(address) != nil {
		t.Fatal("deleted generator")
	}
	s.SetGenerator(address, g)
	s.ResetGenerators(4)
	if len(s.StagedGenerators()) != 0 {
		t.Fatal("reset generators")
	}
	s.CaptureIdentityOriginal(address, f.plain)
	s.CaptureIdentityOriginal(address, f.temporary)
	if d, ok := s.IdentityOriginal(address); !ok || d != f.plain || len(s.IdentityOriginalMap()) != 1 {
		t.Fatal("first capture wins", d, ok)
	}
}

func TestAlterPersistenceSequenceEffects(t *testing.T) {
	for _,destination:=range []string{"logged","unlogged","temporary"} {
		for _,explicit:=range []string{"","logged","unlogged"} {
			var raw []parser.SequenceOption
			if explicit!="" {raw=[]parser.SequenceOption{{Name:explicit}}}
			options,err:=SplitIdentityOptions(parser.SequenceOption{Name:"as",Type:parser.TypeName{Name:"integer"}},raw)
			if err!=nil {t.Fatal(err)}
			want:=explicit;if want=="" {want=destination}
			if options.ResolvePersistence(destination)!=want {t.Fatal("identity option/default precedence",destination,explicit,want)}
			if destination=="temporary" && (options.CheckTemporary()!=nil)!=(explicit!="") {t.Fatal("TEMP option admission changed",explicit)}
		}
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90030}
	stage := &Stage{}
	generator := &storage.SequenceGenerator{Last: 5, Called: true, Generation: 2}
	stage.SetPersistenceGenerator(address, generator)
	if !stage.LazyPersistence(address) || stage.Generator(address) != generator {
		t.Fatal("persistence generator lost lazy adoption")
	}
	stage.SetGenerator(address, generator)
	if stage.LazyPersistence(address) {
		t.Fatal("ordinary generator replacement retained persistence marker")
	}
	stage.SetPersistenceGenerator(address, generator)
	stage.DeleteGenerator(address)
	if stage.LazyPersistence(address) || stage.Generator(address) != nil {
		t.Fatal("deleted generator retained marker")
	}
	stage.SetPersistenceGenerator(address, generator)
	stage.ResetGenerators(0)
	if stage.LazyPersistence(address) || len(stage.StagedGenerators()) != 0 {
		t.Fatal("reset generator retained marker")
	}
}
