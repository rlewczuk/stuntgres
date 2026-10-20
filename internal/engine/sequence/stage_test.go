package sequence

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
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
