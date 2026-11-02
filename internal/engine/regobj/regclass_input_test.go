package regobj

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestClassInputRelationLookupContexts(t *testing.T) {
	state := catalog.NewState(1)
	owner := &catalog.Owner{Domain: &ids.Domain{}, Principals: &authorization.Domain{}}
	record := &access.RoleRecord{Root: authorization.Seed(owner.Principals), Revision: 1}
	bootstrap := owner.Principals.Principal(authorization.BootstrapPrincipalID)
	database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: "memory", Owner: bootstrap, AllowConnections: true, ConnectionLimit: -1}
	state.SetDescriptor(database.Address(), database)
	state.BindName(catalog.DescriptorNameKey(database), database.Address())
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}, ObjectName: "lookup_schema", Owner: bootstrap}
	state.SetDescriptor(ns.Address(), ns)
	state.BindName(catalog.DescriptorNameKey(ns), ns.Address())
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ}
		state.SetDescriptor(d.Address(), d)
	}
	for i, name := range []string{"target", "123", "-"} {
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(20001 + i)}, ObjectNamespace: ns.Address(), ObjectName: name}
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	view := catalog.NewRead(owner, state)
	roles := access.RoleRead(owner, owner.Principals, view, record)
	roleState := authorization.RoleState{Authenticated: bootstrap, SessionUser: bootstrap, CurrentUser: bootstrap}
	input := NewClassInput(InputCapture{
		Authority: Authority{View: access.View{Owner: owner, Catalog: view, PairedCatalog: view, RoleRecord: record, Roles: roles, State: roleState, SessionRoleState: roleState}},
		Path:      []ids.ObjectAddress{ns.Address()},
		Database:  "memory",
	})
	for _, mode := range []struct {
		name    string
		resolve func(context.Context, string) (uint32, error)
		open    bool
		numeric uint32
		dash    uint32
	}{
		{"regclass", input.ResolveRegclass, false, 123, 0},
		{"relation_name", input.ResolveRelationName, false, 20002, 20003},
		{"relation_open", input.ResolveRelationOpenName, true, 20002, 20003},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, tc := range []struct {
				text, code, message string
				want                uint32
			}{
				{"target", "", "", 20001},
				{"lookup_schema.target", "", "", 20001},
				{`"lookup_schema"."target"`, "", "", 20001},
				{"memory.lookup_schema.target", "", "", 20001},
				{"123", "", "", mode.numeric},
				{"-", "", "", mode.dash},
				{"missing", "42P01", `relation "missing" does not exist`, 0},
				{"lookup_schema.missing", "42P01", `relation "lookup_schema.missing" does not exist`, 0},
				// A qualified missing namespace must never fall back to the
				// captured path's real target, even though its name matches.
				{"absent_schema.target", "42P01", `relation "absent_schema.target" does not exist`, 0},
				{`"Absent.Schema".target`, "42P01", `relation "Absent.Schema.target" does not exist`, 0},
				{"memory.absent_schema.target", "42P01", `relation "absent_schema.target" does not exist`, 0},
				{"pg_temp.target", "42P01", `relation "pg_temp.target" does not exist`, 0},
				{"", "42602", "invalid name syntax", 0},
				{"bad..name", "42602", "invalid name syntax", 0},
				{`"unclosed`, "42602", "invalid name syntax", 0},
				{"other.lookup_schema.target", "0A000", `cross-database references are not implemented: "other.lookup_schema.target"`, 0},
				{"a.b.c.d", "42601", "improper relation name (too many dotted names): a.b.c.d", 0},
			} {
				t.Run(tc.text, func(t *testing.T) {
					if mode.open {
						switch tc.text {
						case "absent_schema.target", "memory.absent_schema.target":
							tc.code, tc.message = "3F000", `schema "absent_schema" does not exist`
						case `"Absent.Schema".target`:
							tc.code, tc.message = "3F000", `schema "Absent.Schema" does not exist`
						case "pg_temp.target":
							tc.code, tc.message = "3F000", `schema "pg_temp" does not exist`
						}
					}
					got, err := mode.resolve(context.Background(), tc.text)
					if tc.code == "" {
						if err != nil || got != tc.want {
							t.Fatalf("resolved identity = %d, %v; want %d", got, err, tc.want)
						}
						return
					}
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
					var diagnostic *sqlstate.Error
					if got != 0 || !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
						t.Fatalf("resolved identity = %d, diagnostic = %#v (%v); want %#v", got, diagnostic, err, want)
					}
				})
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if got, err := mode.resolve(ctx, "absent_schema.target"); got != 0 || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled lookup = %d, %v", got, err)
			}
		})
	}
}
