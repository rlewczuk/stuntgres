package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestTypeDiagnosticNameAtDefinitions(t *testing.T) {
	state := NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &Type{SQLType:typ}
		state.SetDescriptor(d.Address(), d)
		state.BindName(DescriptorNameKey(d), d.Address())
	}
	base := Read{state:state}
	candidate := NewCandidate(base)
	first := &Namespace{ObjectAddress:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:91001},ObjectName:"First"}
	second := &Namespace{ObjectAddress:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:91002},ObjectName:"second"}
	typ := &Type{SQLType:sqltype.LookupOnly(91003,"value",91004),ObjectNamespace:second.Address()}
	array := &Type{SQLType:sqltype.LookupArray(91004,"_value",typ.SQLType),ObjectNamespace:second.Address()}
	shadow := &Type{SQLType:sqltype.LookupOnly(91005,"value",0),ObjectNamespace:first.Address()}
	for _, d := range []Descriptor{first,second,typ,array,shadow} {
		if err := candidate.Create(d); err != nil { t.Fatal(err) }
	}
	reader := candidate.Definitions()
	for _, tc := range []struct{ typ *Type; path []ids.ObjectAddress; want string }{
		{typ,[]ids.ObjectAddress{second.Address()},"value"},
		{typ,[]ids.ObjectAddress{first.Address(),second.Address()},"second.value"},
		{shadow,[]ids.ObjectAddress{second.Address()},"\"First\".value"},
		{array,[]ids.ObjectAddress{second.Address()},"value[]"},
		{array,[]ids.ObjectAddress{first.Address(),second.Address()},"second.value[]"},
	} {
		if got := TypeDiagnosticNameAt(base,reader,tc.typ,tc.path); got != tc.want { t.Fatalf("staged type: got %q want %q",got,tc.want) }
	}
	integer := base.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(sqltype.Int4)})
	if got := TypeDiagnosticNameAt(base,reader,integer,[]ids.ObjectAddress{}); got != "integer" { t.Fatal("staged reader changed builtin alias",got) }
	if _, exists := base.Descriptor(typ.Address()); exists { t.Fatal("diagnostic formatting published staged type") }
}
