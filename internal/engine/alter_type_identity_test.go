package engine

import (
	"context"
	"reflect"
	"testing"
)

func TestAlterTypeACLAndOwnedSequenceIdentity(t *testing.T) {
	e := New()
	run(t, e, "CREATE USER alter_type_reader")
	run(t, e, "CREATE USER alter_type_outsider")
	reader, _ := e.Roles().Lookup("alter_type_reader")
	run(t, e, "CREATE TABLE t(c INT)")
	run(t, e, "INSERT INTO t VALUES(7)")
	run(t, e, "CREATE SEQUENCE q START 10 CACHE 7 OWNED BY t.c")
	tableRef, sequenceRef := roleTableRef(t, e, "t"), roleTableRef(t, e, "q")
	table := e.state.descriptors[tableRef.Address].(*relationDescriptor)
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: table.owner, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}}}
	for _, subID := range []int32{0, table.attributes[0]} {
		ref := tableRef
		ref.Address.SubID = subID
		if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	before := e.state.descriptors[tableRef.Address].(*relationDescriptor)
	sequence := e.state.descriptors[sequenceRef.Address]
	s := sessionStart(t, e, nil)
	sequenceScalar(t, s, "SELECT nextval('q')", 10)
	generator, memory := e.sequences[sequenceRef.Address], s.sequences[sequenceRef.Address]
	generatorValue, memoryValue := *generator, *memory
	run(t, e, "ALTER TABLE t ALTER c TYPE BIGINT USING c+1")
	after := e.state.descriptors[tableRef.Address].(*relationDescriptor)
	if after.address != before.address || after.owner != before.owner || !reflect.DeepEqual(after.attributes, before.attributes) || !reflect.DeepEqual(after.acl, before.acl) || !reflect.DeepEqual(after.columnACL, before.columnACL) {
		t.Fatal("TYPE changed ACL/attribute/owner identity", before, after)
	}
	if e.state.descriptors[sequenceRef.Address] != sequence || e.sequences[sequenceRef.Address] != generator || *generator != generatorValue || s.sequences[sequenceRef.Address] != memory || *memory != memoryValue {
		t.Fatal("TYPE changed owned sequence identity/generator/cache")
	}
	sequenceScalar(t, s, "SELECT nextval('q')", 11)
	if got := tuples(run(t, e, "SELECT c FROM t")); len(got) != 1 || got[0][0] != int64(8) {
		t.Fatal(got)
	}
	readerOp, readerDone := roleOperation(t, e, "alter_type_reader")
	defer readerDone()
	if rows := tuples(roleExecute(t, e, readerOp, "SELECT c FROM t")); len(rows) != 1 || rows[0][0] != int64(8) {
		t.Fatal(rows)
	}
	outsider, outsiderDone := roleOperation(t, e, "alter_type_outsider")
	defer outsiderDone()
	roleError(t, e, outsider, "SELECT c FROM t", "42501", "permission denied for table t", "")
}
