package regobj_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func TestProcedureCommentInquiry(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	oid, err := sqltype.OIDValue(types[sqltype.Oid], 1346)
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1346},
		ObjectName:    "pow",
		Comment:       "exponentiation",
	}
	state := catalog.NewState(1)
	state.SetDescriptor(d.Address(), d)
	retained := catalog.NewRead(nil, state)
	inquire := func(view catalog.Read, kind functions.CommentInquiryKind, class, want string) {
		t.Helper()
		values := [2]sqltype.Value{oid, sqltype.String(types[sqltype.Name], class)}
		got, err := regobj.CommentText(view, authorization.Read{}, kind, values)
		if err != nil || got != want {
			t.Fatalf("comment kind %v class %q = %q, %v; want %q", kind, class, got, err, want)
		}
	}
	inquire(retained, functions.ObjectDescription, "pg_proc", "exponentiation")
	inquire(retained, functions.LegacyDescription, "", "exponentiation")
	inquire(retained, functions.ObjectDescription, "pg_class", "")
	inquire(retained, functions.SharedDescription, "pg_proc", "")

	// Current removal stays absent; a retained catalog still owns its old text.
	cleared := *d
	cleared.Comment = ""
	next := catalog.CloneState(state)
	next.SetDescriptor(d.Address(), &cleared)
	inquire(catalog.NewRead(nil, next), functions.ObjectDescription, "pg_proc", "")
	inquire(catalog.NewRead(nil, next), functions.LegacyDescription, "", "")
	inquire(retained, functions.ObjectDescription, "pg_proc", "exponentiation")
	next.RemoveDescriptor(d.Address())
	inquire(catalog.NewRead(nil, next), functions.ObjectDescription, "pg_proc", "")
}
