package regobj

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestObjectIdentityUserMappingAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, role, server, identity string
		public                       bool
	}{
		{"role", "postgres", "et_dc_server", "postgres on server et_dc_server", false},
		{"public", "public", "et_dc_server", "public on server et_dc_server", true},
		{"quoted", "Role \" Name", "Server \" Name", "\"Role \"\" Name\" on server Server \" Name", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := &catalog.Owner{Domain: &ids.Domain{}, Principals: &authorization.Domain{}}
			root := authorization.Seed(owner.Principals)
			principal := owner.Principals.Principal(authorization.BootstrapPrincipalID)
			role, err := root.Read(principal).Descriptor(principal)
			if err != nil {
				t.Fatal(err)
			}
			role.Name = tc.role
			root, err = root.PutDescriptor(role)
			if err != nil {
				t.Fatal(err)
			}
			roles := root.Read(principal)
			server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 20001}, ObjectName: tc.server}
			mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 20002}, Server: server.Address(), MappedRole: principal, MappedName: "stale diagnostic name"}
			if tc.public {
				mapping.MappedRole = authorization.PrincipalRef{}
			}
			mapping.ObjectName = catalog.ForeignMappingName(mapping.Server, mapping.MappedRole)
			state := catalog.NewState(0)
			state.SetDescriptor(server.Address(), server)
			state.SetDescriptor(mapping.Address(), mapping)
			view := catalog.NewRead(owner, state)
			want := ObjectIdentity{Kind: "user mapping", Identity: tc.identity, Description: "user mapping for " + tc.role + " on server " + tc.server, Names: []string{tc.role}, Args: []string{tc.server}, Found: true}
			got, err := IdentifyAddress(view, roles, nil, mapping.Address(), ids.SessionToken{})
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("identity = %+v, %v; want %+v", got, err, want)
			}
			dropped, err := DroppedAddress(view, roles, nil, mapping.Address(), ids.SessionToken{})
			if err != nil || !dropped.Found || dropped.Address != mapping.Address() || dropped.Kind != want.Kind || dropped.Identity != want.Identity || dropped.HasSchema || dropped.HasName || dropped.Temporary || !reflect.DeepEqual(dropped.Names, want.Names) || !reflect.DeepEqual(dropped.Args, want.Args) {
				t.Fatalf("dropped identity = %+v, %v", dropped, err)
			}

			// Identity follows current descriptors, never cached diagnostic names or
			// the server/principal numbers encoded in the unchanged storage key.
			next := catalog.CloneState(state)
			renamed := *server
			renamed.ObjectName = "renamed server"
			next.SetDescriptor(server.Address(), &renamed)
			role.Name = "renamed role"
			changedRoles, err := root.PutDescriptor(role)
			if err != nil {
				t.Fatal(err)
			}
			current := catalog.NewRead(owner, next)
			username, quoted := "renamed role", `"renamed role"`
			if tc.public {
				username, quoted = "public", "public"
			}
			changed, err := IdentifyAddress(current, changedRoles.Read(principal), nil, mapping.Address(), ids.SessionToken{})
			if err != nil || !changed.Found || changed.Identity != quoted+" on server renamed server" || !reflect.DeepEqual(changed.Names, []string{username}) || !reflect.DeepEqual(changed.Args, []string{"renamed server"}) {
				t.Fatalf("current identity = %+v, %v", changed, err)
			}
			retained, err := IdentifyAddress(view, roles, nil, mapping.Address(), ids.SessionToken{})
			if err != nil || !reflect.DeepEqual(retained, want) {
				t.Fatalf("retained identity = %+v, %v; want %+v", retained, err, want)
			}
			if !tc.public {
				if _, err := IdentifyAddress(view, authorization.Read{}, nil, mapping.Address(), ids.SessionToken{}); err == nil {
					t.Fatal("mapping accepted missing principal authority")
				}
			}
			missingServer := *mapping
			missingServer.Server.ObjectID = 29999
			next.SetDescriptor(mapping.Address(), &missingServer)
			if _, err := IdentifyAddress(catalog.NewRead(owner, next), roles, nil, mapping.Address(), ids.SessionToken{}); err == nil {
				t.Fatal("mapping accepted absent server identity")
			}
			missing, err := IdentifyAddress(view, roles, nil, ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 29999}, ids.SessionToken{})
			if err != nil || missing.Found {
				t.Fatalf("absent mapping = %+v, %v", missing, err)
			}
		})
	}
}
