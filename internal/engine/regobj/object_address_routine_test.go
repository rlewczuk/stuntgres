package regobj

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Only the catalog's helper-owner index reads this provider association; these
// fixtures carry declaration facts and grant no executable routine capability.
type addressKindProvider struct{}

func (addressKindProvider) Control() catalog.ExtensionControl                        { return catalog.ExtensionControl{} }
func (addressKindProvider) Versions() catalog.ExtensionVersions                      { return catalog.ExtensionVersions{} }
func (addressKindProvider) ValidateExtension(catalog.Read, *catalog.Extension) error { return nil }

func addressRoutineFixture(t *testing.T) (*catalog.State, AddressLookup, func(string, []string, []string) [3]sqltype.Value) {
	t.Helper()
	state := catalog.NewState(0)
	owner := &catalog.Owner{Domain: &ids.Domain{}, Principals: &authorization.Domain{}}
	record := &access.RoleRecord{Root: authorization.Seed(owner.Principals), Revision: 1}
	bootstrap := owner.Principals.Principal(authorization.BootstrapPrincipalID)
	database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectName: "memory", Owner: bootstrap, AllowConnections: true, ConnectionLimit: -1}
	state.SetDescriptor(database.Address(), database)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ}
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
		state.SetDescriptor(d.Address(), d)
	}
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
		}
	}
	array, err := sqltype.Array(text.AssociatedArray(), "_text", text)
	if err != nil {
		t.Fatal(err)
	}
	values := func(kind string, names, args []string) [3]sqltype.Value {
		t.Helper()
		n, err := ObjectAddressTextArray(array, names)
		if err != nil {
			t.Fatal(err)
		}
		a, err := ObjectAddressTextArray(array, args)
		if err != nil {
			t.Fatal(err)
		}
		return [3]sqltype.Value{sqltype.String(text, kind), n, a}
	}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: "pg_catalog", Owner: bootstrap}
	state.SetDescriptor(ns.Address(), ns)
	state.BindName(catalog.DescriptorNameKey(ns), ns.Address())
	view := catalog.NewRead(owner, state)
	roles := access.RoleRead(owner, owner.Principals, view, record)
	roleState := authorization.RoleState{Authenticated: bootstrap, SessionUser: bootstrap, CurrentUser: bootstrap}
	path := []ids.ObjectAddress{ns.Address()}
	return state, AddressLookup{
		Catalog: view,
		Capture: InputCapture{Authority: Authority{View: access.View{Owner: owner, Catalog: view, PairedCatalog: view, RoleRecord: record, Roles: roles, State: roleState, SessionRoleState: roleState}}, Path: path},
		Namespace: func(parser.ObjectName) (analyze.NamespaceSelection, error) {
			return analyze.NamespaceSelection{Qualified: ns.Address()}, nil
		},
	}, values
}

func TestAddressLookupRoutineKindsAndRetainedAuthority(t *testing.T) {
	state, lookup, values := addressRoutineFixture(t)
	ns := lookup.Capture.Path[0]
	for _, tc := range []struct {
		id   ids.OID
		kind catalog.RoutineKind
		want string
	}{
		{20001, catalog.FunctionRoutine, "function"},
		{20002, catalog.ProcedureRoutine, "procedure"},
		{20003, catalog.AggregateRoutine, "aggregate"},
		{20004, catalog.WindowRoutine, "function"},
		// A represented released aggregate OID must not override its descriptor.
		{2100, catalog.ProcedureRoutine, "procedure"},
	} {
		t.Run(tc.want+string(tc.kind), func(t *testing.T) {
			next := catalog.CloneState(state)
			p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.id}, ObjectNamespace: ns, ObjectName: "kind_target", RoutineKind: tc.kind}
			next.SetDescriptor(p.Address(), p)
			next.BindName(catalog.DescriptorNameKey(p), p.Address())
			q := lookup
			q.Catalog = catalog.NewRead(q.Capture.Authority.View.Owner, next)
			q.Capture.Authority.View.Recapture(q.Catalog)
			got, err := q.Lookup(context.Background(), values(tc.want, []string{"kind_target"}, nil))
			if err != nil || got != p.Address() {
				t.Fatalf("inverse = %v, %v; want %v", got, err, p.Address())
			}
			identity, err := IdentifyAddress(q.Catalog, authorization.Read{}, q.Capture.Path, p.Address(), q.Capture.Token)
			if err != nil || !identity.Found || identity.Kind != tc.want {
				t.Fatalf("forward = %+v, %v; want %s", identity, err, tc.want)
			}
			dropped, err := DroppedAddress(q.Catalog, authorization.Read{}, q.Capture.Path, p.Address(), q.Capture.Token)
			if err != nil || !dropped.Found || dropped.Kind != tc.want {
				t.Fatalf("dropped = %+v, %v; want %s", dropped, err, tc.want)
			}
			before := identity
			replacement := *p
			replacement.RoutineKind = catalog.FunctionRoutine
			replacement.ObjectName = "replacement"
			changed := catalog.CloneState(next)
			changed.SetDescriptor(p.Address(), &replacement)
			current := catalog.NewRead(q.Capture.Authority.View.Owner, changed)
			if objectRoutineKind(current, tc.id) != "function" {
				t.Fatal("replacement descriptor kind not observed")
			}
			after, err := IdentifyAddress(q.Catalog, authorization.Read{}, q.Capture.Path, p.Address(), q.Capture.Token)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("retained identity changed: %+v, %v; want %+v", after, err, before)
			}
			got, err = q.Lookup(context.Background(), values(tc.want, []string{"kind_target"}, nil))
			if err != nil || got != p.Address() {
				t.Fatalf("retained inverse = %v, %v", got, err)
			}
		})
	}
}

func TestAddressLookupRoutineReleasedAndHelperFallback(t *testing.T) {
	state, lookup, values := addressRoutineFixture(t)
	for _, tc := range []struct {
		id   ids.OID
		want string
	}{{2100, "aggregate"}, {870, "function"}} {
		if got := objectRoutineKind(lookup.Catalog, tc.id); got != tc.want {
			t.Fatalf("released kind %d = %s, want %s", tc.id, got, tc.want)
		}
		identity, err := IdentifyAddress(lookup.Catalog, authorization.Read{}, lookup.Capture.Path, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.id}, lookup.Capture.Token)
		if err != nil || !identity.Found || identity.Kind != tc.want {
			t.Fatalf("released identity %d = %+v, %v", tc.id, identity, err)
		}
		got, err := lookup.Lookup(context.Background(), values(tc.want, identity.Names, identity.Args))
		if err != nil || got.ObjectID != tc.id {
			t.Fatalf("released inverse %d = %v, %v", tc.id, got, err)
		}
	}
	helper := catalog.RoutineIdentity{ID: 20012, Namespace: ids.PGCatalogNamespaceOID, Name: "helper_target"}
	owner := &catalog.Procedure{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 20011}, ObjectNamespace: lookup.Capture.Path[0], ObjectName: "aggregate_owner", RoutineKind: catalog.AggregateRoutine,
		Implementation: addressKindProvider{}, Aggregate: &catalog.OwnedAggregate{Helpers: [2]catalog.RoutineIdentity{helper}},
	}
	next := catalog.CloneState(state)
	next.SetDescriptor(owner.Address(), owner)
	lookup.Catalog = catalog.NewRead(lookup.Capture.Authority.View.Owner, next)
	lookup.Capture.Authority.View.Recapture(lookup.Catalog)
	if RoutineDescriptorOwner(lookup.Catalog, helper.ID) != owner {
		t.Fatal("helper fixture lacks real aggregate owner")
	}
	got, err := lookup.Lookup(context.Background(), values("function", []string{helper.Name}, nil))
	if err != nil || got.ObjectID != helper.ID {
		t.Fatalf("helper inverse = %v, %v", got, err)
	}
	identity, err := IdentifyAddress(lookup.Catalog, authorization.Read{}, lookup.Capture.Path, got, lookup.Capture.Token)
	if err != nil || !identity.Found || identity.Kind != "function" {
		t.Fatalf("helper inherited aggregate kind: %+v, %v", identity, err)
	}
}

func TestAddressLookupRoutineArgumentCapPrecedence(t *testing.T) {
	_, lookup, values := addressRoutineFixture(t)
	args := make([]string, 101)
	for i := range args {
		args[i] = "integer"
	}
	for _, kind := range []string{"function", "procedure", "aggregate"} {
		noun := "functions"
		if kind == "procedure" {
			noun = "procedures"
		}
		v := values(kind, []string{"missing_schema", "f"}, args)
		_, err := lookup.Lookup(context.Background(), v)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "54023" || diagnostic.Message != noun+" cannot have more than 100 arguments" {
			t.Fatalf("%s count before schema = %v", kind, err)
		}
	}
	for _, tc := range []struct {
		first, code string
		count       int
	}{
		{"NONE", "42601", 101},
		{"missing_type", "54023", 101},
		{"missing_type", "42704", 100},
		{"integer", "42883", 100},
	} {
		input := append([]string(nil), args[:tc.count]...)
		input[0] = tc.first
		_, err := lookup.Lookup(context.Background(), values("function", []string{"f"}, input))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatalf("%s/%d = %v, want %s", tc.first, tc.count, err, tc.code)
		}
		if tc.code == "42883" && !strings.HasPrefix(diagnostic.Message, "function f(integer, integer") {
			t.Fatalf("100-input missing signature = %v", err)
		}
	}
}

func TestAddressLookupRoutineSyntaxNullBeforeCap(t *testing.T) {
	_, lookup, values := addressRoutineFixture(t)
	for _, syntaxFirst := range []bool{false, true} {
		v := values("function", []string{"missing_schema", "f"}, nil)
		array := v[2].Type()
		cells := make([]sqltype.Value, 101)
		for i := range cells {
			cells[i] = sqltype.String(array.Element(), "integer")
		}
		if syntaxFirst {
			cells[0], cells[1] = sqltype.String(array.Element(), "NONE"), sqltype.Null(array.Element())
		} else {
			cells[0], cells[1] = sqltype.Null(array.Element()), sqltype.String(array.Element(), "NONE")
		}
		var err error
		v[2], err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 101}}, cells)
		if err != nil {
			t.Fatal(err)
		}
		_, err = lookup.Lookup(context.Background(), v)
		code := "22023"
		if syntaxFirst {
			code = "42601"
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != code {
			t.Fatalf("syntaxFirst=%t: %v, want %s before cap/schema", syntaxFirst, err, code)
		}
	}
}
