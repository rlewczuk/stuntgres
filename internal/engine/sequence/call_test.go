package sequence

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func wantSQLState(t *testing.T, err error, code, message string) {
	t.Helper()
	var e *sqlstate.Error
	if !errors.As(err, &e) || e.Code != code || e.Message != message {
		t.Fatalf("got %v, want %s %q", err, code, message)
	}
}

func callRelation() *catalog.Relation {
	options := catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 2, Min: 1, Max: 50, Start: 1, Cache: 3}
	return NewRelation(RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90020}, Name: "s", Revision: 4, Options: options})
}

// CallLocked is the currval/lastval/nextval/setval semantics over one
// session memory and the shared generator.
func TestSequenceCallLocked(t *testing.T) {
	d := callRelation()
	g := &storage.SequenceGenerator{Last: 1, Generation: 4}
	var memories SessionMemory
	call := func(request functions.SequenceRequest) (int64, error) {
		return CallLocked(request, d, g, &memories)
	}
	_, err := call(functions.SequenceRequest{Name: "currval"})
	wantSQLState(t, err, "55000", `currval of sequence "s" is not yet defined in this session`)
	_, err = call(functions.SequenceRequest{Name: "lastval"})
	wantSQLState(t, err, "55000", "lastval is not yet defined in this session")
	if memories.Len() != 1 || memories.Last() != (ids.ObjectAddress{}) {
		t.Fatal("undefined reads demand the memory but set no lastval target", memories.Len(), memories.Last())
	}
	for _, want := range []int64{1, 3} {
		if v, err := call(functions.SequenceRequest{Name: "nextval"}); err != nil || v != want {
			t.Fatal("nextval", v, err, want)
		}
	}
	if memories.Last() != d.Address() || memories.Memory(d.Address()).CachedTail() != 1 {
		t.Fatal("nextval memory", memories.Last(), memories.Memory(d.Address()).CachedTail())
	}
	reserved := *g
	if v, err := call(functions.SequenceRequest{Name: "nextval"}); err != nil || v != 5 || *g != reserved {
		t.Fatal("cached nextval touched the generator", v, err, *g)
	}
	for _, name := range []string{"currval", "lastval"} {
		if v, err := call(functions.SequenceRequest{Name: name}); err != nil || v != 5 {
			t.Fatal(name, v, err)
		}
	}
}

func TestSequenceCallLockedSetval(t *testing.T) {
	d := callRelation()
	g := &storage.SequenceGenerator{Last: 1, Generation: 4}
	var memories SessionMemory
	int8 := builtin(t, sqltype.Int8)
	boolean := builtin(t, sqltype.Bool)
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(int8, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if v, err := CallLocked(functions.SequenceRequest{Name: "nextval"}, d, g, &memories); err != nil || v != 1 {
		t.Fatal("nextval", v, err)
	}
	before := *g
	_, err := CallLocked(functions.SequenceRequest{Name: "setval", Value: value(100)}, d, g, &memories)
	wantSQLState(t, err, "22003", `setval: value 100 is out of bounds for sequence "s" (1..50)`)
	if *g != before {
		t.Fatal("rejected setval changed the generator", *g)
	}
	request := functions.SequenceRequest{Name: "setval", Value: value(20), Called: sqltype.Boolean(boolean, false), HasCalled: true}
	if v, err := CallLocked(request, d, g, &memories); err != nil || v != 20 || g.Last != 20 || g.Called {
		t.Fatal("setval", v, err, *g)
	}
	m := memories.Memory(d.Address())
	if current, valid := m.Current(); !valid || current != 1 || m.CachedTail() != 0 {
		t.Fatal("uncalled setval kept currval and dropped the tail", current, valid, m.CachedTail())
	}
	if v, err := CallLocked(functions.SequenceRequest{Name: "nextval"}, d, g, &memories); err != nil || v != 20 {
		t.Fatal("nextval after uncalled setval", v, err)
	}
	if v, err := CallLocked(functions.SequenceRequest{Name: "setval", Value: value(30)}, d, g, &memories); err != nil || v != 30 || !g.Called {
		t.Fatal("called setval", v, err, *g)
	}
	if current, _ := m.Current(); current != 30 {
		t.Fatal("called setval currval", current)
	}
	// A new generator generation drops the session's older tail.
	g.Generation = 5
	if v, err := CallLocked(functions.SequenceRequest{Name: "nextval"}, d, g, &memories); err != nil || v != 32 || m.Generation() != 5 {
		t.Fatal("nextval after generation change", v, err, m.Generation())
	}
}

func TestSequenceCallPrivilegesAndReadOnly(t *testing.T) {
	for name, want := range map[string]authorization.Privileges{
		"currval": authorization.UsagePrivilege | authorization.SelectPrivilege,
		"lastval": authorization.UsagePrivilege | authorization.SelectPrivilege,
		"nextval": authorization.UsagePrivilege | authorization.UpdatePrivilege,
		"setval":  authorization.UpdatePrivilege,
	} {
		if got := CallPrivileges(name); got != want {
			t.Errorf("%s privileges %v, want %v", name, got, want)
		}
	}
	for _, name := range []string{"currval", "lastval"} {
		if err := ReadOnlyCallError(name); err != nil {
			t.Errorf("%s in a read-only transaction: %v", name, err)
		}
	}
	for _, name := range []string{"nextval", "setval"} {
		wantSQLState(t, ReadOnlyCallError(name), "25006", "cannot execute "+name+"() in a read-only transaction")
	}
}
