package index

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Only the exercised DDL adapter methods are implemented. Unexpected staging or
// allocation during admission panics through the embedded, deliberately nil Host.
// Resolution uses the real released identity index and an actual catalog read.
type familyAdmissionHost struct {
	FamilyDDLHost
	view     catalog.Read
	resolved []string
}

func (h *familyAdmissionHost) AccessView(ddl.Authority) access.View {
	return access.View{Catalog: h.view}
}
func (h *familyAdmissionHost) ResolveRegtype(_ ddl.Authority, raw parser.TypeName) (ids.OID, error) {
	h.resolved = append(h.resolved, raw.Name)
	switch raw.Name {
	case "integer":
		return 23, nil
	case "bigint":
		return 20, nil
	case "macaddr":
		return 829, nil
	}
	return 0, familyError("42704", "missing type "+raw.Name)
}
func (h *familyAdmissionHost) SelectFamilyFunction(_ ddl.Authority, name parser.ObjectName, signature *catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error) {
	if signature == nil {
		panic("unit requires explicit function signature")
	}
	identity, ok := catalog.BuiltinRoutineIdentityBySignature(name.Parts[len(name.Parts)-1].Text, *signature)
	if ok {
		return identity, 1, nil
	}
	return catalog.RoutineIdentity{}, 0, nil
}
func (h *familyAdmissionHost) SearchNamespaces(ddl.Authority, parser.ObjectName, ids.OID) ([]ids.ObjectAddress, error) {
	return []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}, nil
}
func (h *familyAdmissionHost) DependencyObject(_ ddl.Authority, address ids.ObjectAddress) (string, error) {
	return "pinned member", nil
}

func familyUnitAssociation(names ...string) []parser.TypeName {
	out := make([]parser.TypeName, len(names))
	for i, name := range names {
		out[i] = parser.TypeName{Name: name}
	}
	return out
}
func familyUnitFunction(number string) parser.OperatorFamilyItem {
	return parser.OperatorFamilyItem{Function: true, Number: number, Signature: true, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "btint4cmp"}}}, Arguments: familyUnitAssociation("integer", "integer")}
}
func familyUnitDrop(function bool, number string) parser.OperatorFamilyItem {
	return parser.OperatorFamilyItem{Function: function, Number: number, Association: familyUnitAssociation("integer")}
}

func TestOperatorFamilyMemberPhases(t *testing.T) {
	state := catalog.NewState(1)
	catalog.InstallBuiltinAccessMethods(state)
	handlers := routine.NativeAccessMethodProcedures(authorization.PrincipalRef{})
	for i := range handlers {
		state.SetDescriptor(handlers[i].Address(), &handlers[i])
		state.BindName(catalog.DescriptorNameKey(&handlers[i]), handlers[i].Address())
	}
	catalog.InstallBuiltinFamilies(state, authorization.PrincipalRef{})
	family := &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 90001}, Method: 403, ObjectName: "probe"}
	state.SetDescriptor(family.Address(), family)
	for _, d := range []catalog.Descriptor{
		&catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 97}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: "<", Left: 23, Right: 23, Result: 16},
		&catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: 90002}, Family: 90001, Left: 23, Right: 23, Number: 1, Function: 351},
		&catalog.OperatorFamilyOperator{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodOperatorClassID, ObjectID: 90003}, Family: 90001, Left: 23, Right: 23, Number: 1, Operator: 97, Purpose: 's', Method: 403},
	} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	view := catalog.NewRead(nil, state)
	pinned, _ := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 1976})
	cases := []struct {
		name, action  string
		family        *catalog.OperatorFamily
		items         []parser.OperatorFamilyItem
		code, message string
	}{
		{"same list before stored", "add", family, []parser.OperatorFamilyItem{familyUnitFunction("1"), familyUnitFunction("1")}, "42P17", "function number 1 for (23,23) appears more than once"},
		{"later invalid before stored", "add", family, []parser.OperatorFamilyItem{familyUnitFunction("1"), familyUnitFunction("7")}, "42P17", "invalid function number 7, must be between 1 and 6"},
		{"later invalid before missing", "drop", family, []parser.OperatorFamilyItem{familyUnitDrop(true, "2"), familyUnitDrop(true, "7")}, "42P17", "invalid function number 7, must be between 1 and 6"},
		{"later invalid before pin", "drop", pinned, []parser.OperatorFamilyItem{familyUnitDrop(true, "1"), familyUnitDrop(true, "7")}, "42P17", "invalid function number 7, must be between 1 and 6"},
		{"list duplicate before pin", "drop", pinned, []parser.OperatorFamilyItem{familyUnitDrop(true, "1"), familyUnitDrop(true, "1")}, "42P17", "function number 1 for (23,23) appears more than once"},
		{"pin before later missing", "drop", pinned, []parser.OperatorFamilyItem{familyUnitDrop(true, "1"), {Function: true, Number: "1", Association: familyUnitAssociation("macaddr")}}, "2BP01", "cannot drop pinned member because it is required by the database system"},
		{"operators before procedures", "drop", family, []parser.OperatorFamilyItem{familyUnitDrop(true, "3"), familyUnitDrop(false, "2")}, "42704", `operator 2(23,23) does not exist in operator family "probe"`},
		{"operator stored before function stored", "add", family, []parser.OperatorFamilyItem{familyUnitFunction("1"), {Number: "1", Signature: true, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "<"}}}, Arguments: familyUnitAssociation("integer", "integer")}}, "42710", `operator 1(23,23) already exists in operator family "probe"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &familyAdmissionHost{view: view}
			err := stageFamilyMembers(h, nil, tc.family, &parser.AlterOperatorFamily{Action: tc.action, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "probe"}}}, Items: tc.items})
			diag, ok := err.(*sqlstate.Error)
			if !ok || diag.Code != tc.code || diag.Message != tc.message {
				t.Fatalf("diagnostic = %#v, want %s %q", err, tc.code, tc.message)
			}
			if got, ok := view.FamilySupport(90001, 23, 23, 1); !ok || got.Address().ObjectID != 90002 {
				t.Fatal("admission failure changed original member", got)
			}
		})
	}
}

func TestOperatorFamilyAssociationResolution(t *testing.T) {
	cases := []struct {
		names, resolved []string
		left, right     ids.OID
		code, message   string
	}{
		{nil, nil, 0, 0, "", ""},
		{[]string{"integer"}, []string{"integer"}, 23, 23, "", ""},
		{[]string{"integer", "bigint"}, []string{"integer", "bigint"}, 23, 20, "", ""},
		{[]string{"missing", "integer", "bigint"}, []string{"missing"}, 0, 0, "42704", "missing type missing"},
		{[]string{"integer", "missing", "bigint"}, []string{"integer", "missing"}, 0, 0, "42704", "missing type missing"},
		{[]string{"integer", "bigint", "missing"}, []string{"integer", "bigint"}, 0, 0, "42601", "one or two argument types must be specified"},
	}
	for _, tc := range cases {
		h := &familyAdmissionHost{}
		left, right, err := familyAssociation(h, nil, familyUnitAssociation(tc.names...))
		if !reflect.DeepEqual(h.resolved, tc.resolved) {
			t.Fatalf("%v resolution = %v, want %v", tc.names, h.resolved, tc.resolved)
		}
		if tc.code == "" {
			if err != nil || left != tc.left || right != tc.right {
				t.Fatalf("%v = %d/%d/%v", tc.names, left, right, err)
			}
		} else if diag, ok := err.(*sqlstate.Error); !ok || diag.Code != tc.code || diag.Message != tc.message {
			t.Fatalf("%v diagnostic = %#v", tc.names, err)
		}
	}
}
