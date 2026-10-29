package core_test

import (
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestExtensionOwnershipEarthBodyDependencies(t *testing.T) {
	e, earth := ownershipUnitInstall(t, "earthdistance")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, `CREATE EXTENSION "uuid-ossp"`)
	base := e.CatalogRead()
	cube := base.MustExtension(base.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
	receiver := base.MustExtension(base.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
	lookup := func(name, signature string) ids.ObjectAddress {
		t.Helper()
		a := base.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: cube.TargetNamespace, Name: name, Signature: signature})
		if a == (ids.ObjectAddress{}) {
			t.Fatal("missing genuine released procedure", name, signature)
		}
		return a
	}
	cubeID := strconv.FormatUint(uint64(cube.Installation.Members[0].ObjectID), 10)
	earthID := strconv.FormatUint(uint64(earth.Installation.Members[0].ObjectID), 10)
	constructors := []ids.ObjectAddress{lookup("cube", "701"), lookup("cube", cubeID+",701")}
	for i, address := range constructors {
		current := base.MustProcedure(address)
		if symbol, err := extension.ProcedureSymbol(base, current); err != nil || symbol != "cube" {
			t.Fatal("constructor SQL identity confused with C source", symbol, err)
		}
		if current.Display.Source != []string{"cube_f8", "cube_c_f8"}[i] {
			t.Fatal("genuine constructor lost its distinct C declaration", current.Display.Source)
		}
	}
	coordinate := lookup("cube_ll_coord", cubeID+",23")
	radius := lookup("earth", "")
	cases := []struct {
		name      string
		signature string
		want      []ids.ObjectAddress
	}{
		{"ll_to_earth", "701,701", []ids.ObjectAddress{radius, constructors[0], constructors[1]}},
		{"latitude", earthID, []ids.ObjectAddress{radius, coordinate}},
		{"longitude", earthID, []ids.ObjectAddress{coordinate}},
	}
	for _, mode := range []string{"attached", "detached", "foreign", "absent"} {
		t.Run(mode, func(t *testing.T) {
			state := catalog.CloneState(base.State())
			if mode != "attached" {
				membership := ids.ObjectAddress{}
				other := *receiver
				other.Members = slices.Clone(receiver.Members)
				if mode == "foreign" {
					membership = receiver.Address()
				}
				for _, origin := range []*catalog.Extension{earth, cube} {
					for _, address := range origin.Members {
						current := state.Get(address)
						detached, err := catalog.WithExtensionMembership(current, membership, current.Revision()+1)
						if err != nil {
							t.Fatal(err)
						}
						state.SetDescriptor(address, detached)
						if mode == "foreign" {
							other.Members = append(other.Members, address)
						}
					}
					if mode == "absent" {
						state.UnbindName(catalog.DescriptorNameKey(origin))
						state.RemoveDescriptor(origin.Address())
					} else {
						header := *origin
						header.Members = nil
						state.SetDescriptor(header.Address(), &header)
					}
				}
				state.SetDescriptor(other.Address(), &other)
			}
			// Both overloads retain their actual typed identities under unrelated
			// public labels; captured body spelling is not dependency authority.
			for _, address := range constructors {
				current := state.MustProcedure(address)
				renamed := *current
				renamed.ObjectName = "retained_constructor"
				state.UnbindName(catalog.DescriptorNameKey(current))
				state.SetDescriptor(address, &renamed)
				state.BindName(catalog.DescriptorNameKey(&renamed), address)
			}
			view := base.WithState(state)
			for _, tc := range cases {
				original := base.MustProcedure(lookup(tc.name, tc.signature))
				current := view.MustProcedure(original.Address())
				if symbol, err := extension.ProcedureSymbol(view, current); err != nil || symbol != tc.name {
					t.Fatal("lawful current membership lost body authority", tc.name, symbol, err)
				}
				if current.DeclarationBody != original.DeclarationBody || current.Origin != original.Origin {
					t.Fatal("membership reconstructed immutable body or origin", tc.name)
				}
				var got []ids.ObjectAddress
				if !catalog.VisitProcedureCapturedDefinitionDependencies(view, current, func(address ids.ObjectAddress) bool {
					got = append(got, address)
					return true
				}) || !slices.Equal(got, tc.want) {
					t.Fatal("lost exact released direct callee identities", tc.name, got, tc.want)
				}
				for _, address := range tc.want {
					if !catalog.ProcedureDefinitionDependsOn(view, current, address) {
						t.Fatal("definition retirement seam lost callee", tc.name, address)
					}
				}
				for stop := 1; stop <= len(tc.want); stop++ {
					calls := 0
					if catalog.VisitProcedureCapturedDefinitionDependencies(view, current, func(ids.ObjectAddress) bool {
						calls++
						return calls < stop
					}) || calls != stop {
						t.Fatal("callee visitor ignored early stop", tc.name, stop, calls)
					}
				}
			}
			ll := view.MustProcedure(lookup("ll_to_earth", "701,701"))
			for _, corruption := range []string{"missing", "copied_origin", "wrong_slot", "signature", "source", "principal", "body_pointer"} {
				invalid := catalog.CloneState(state)
				helper := *view.MustProcedure(constructors[0])
				switch corruption {
				case "missing":
					invalid.RemoveDescriptor(helper.Address())
				case "copied_origin":
					origin := *helper.Origin
					recipe := *origin.Installation
					origin.Installation = &recipe
					helper.Origin = &origin
				case "wrong_slot":
					origin := *helper.Origin
					origin.Declaration++
					helper.Origin = &origin
				case "signature":
					helper.Signature = "23"
				case "source":
					helper.Display.Source = "cube"
				case "principal":
					helper.Owner = catalog.Procedure{}.Owner
				case "body_pointer":
					changed := *ll
					changed.DeclarationBody = view.MustProcedure(radius).DeclarationBody
					invalid.SetDescriptor(ll.Address(), &changed)
				}
				if corruption != "missing" {
					invalid.SetDescriptor(helper.Address(), &helper)
				}
				broken := view.WithState(invalid)
				if _, err := extension.ProcedureSymbol(broken, broken.MustProcedure(ll.Address())); err == nil {
					t.Fatal("invalid current callee/body authority admitted", corruption)
				}
			}
		})
	}
	t.Run("remapped_callee_identities", func(t *testing.T) {
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{
			constructors[0]: {ClassID: ids.ProcedureClassID, ObjectID: 940001},
			constructors[1]: {ClassID: ids.ProcedureClassID, ObjectID: 940002},
			coordinate:      {ClassID: ids.ProcedureClassID, ObjectID: 940003},
		}
		remap := extension.DescriptorRemapper(mapping)
		state := catalog.CloneState(base.State())
		for address, descriptor := range base.Descriptors() {
			mapped := remap(descriptor)
			if mapped == descriptor {
				continue
			}
			state.UnbindName(catalog.DescriptorNameKey(descriptor))
			state.RemoveDescriptor(address)
			state.SetDescriptor(mapped.Address(), mapped)
			state.BindName(catalog.DescriptorNameKey(mapped), mapped.Address())
		}
		view := base.WithState(state)
		for _, tc := range cases {
			current := view.MustProcedure(lookup(tc.name, tc.signature))
			var got []ids.ObjectAddress
			catalog.VisitProcedureCapturedDefinitionDependencies(view, current, func(address ids.ObjectAddress) bool {
				got = append(got, address)
				return true
			})
			want := slices.Clone(tc.want)
			for i, address := range want {
				if mapped, ok := mapping[address]; ok {
					want[i] = mapped
				}
			}
			if !slices.Equal(got, want) {
				t.Fatal("remapped recipe retained foreign callee coordinates", tc.name, got, want)
			}
			if symbol, err := extension.ProcedureSymbol(view, current); err != nil || symbol != tc.name {
				t.Fatal("remapped callee authority rejected", tc.name, symbol, err)
			}
		}
	})
}
