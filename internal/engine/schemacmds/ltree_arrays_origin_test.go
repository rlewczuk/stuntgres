package schemacmds_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestLtreeArraySurvivorAuthority(t *testing.T) {
	e, op, origin, _ := lqueryProviderInstalled(t, false)
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "uuid-ossp", "public"))
	view := e.CatalogRead()
	recipe := origin.Installation
	if !recipe.Valid() || len(recipe.Members) != extension.LtreeMemberCount || len(recipe.Declarations) != extension.LtreeMemberCount {
		t.Fatal("incomplete genuine released recipe")
	}
	// A second genuine installation in this engine has distinct cohort identity.
	// Retain the first immutable read while ordinary SQL retires its live graph.
	enginetest.RoleExecute(t, e, op, "DROP EXTENSION ltree")
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "ltree", "public"))
	other := e.CatalogRead().MustExtension(e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"})).Installation
	for _, foreign := range []bool{false, true} {
		state := catalog.CloneState(view.State())
		// Use a genuine registered receiving installation, never a renamed origin.
		receiver := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
		received := *receiver
		received.Members = slices.Clone(receiver.Members)
		for _, address := range origin.Members {
			d := state.Get(address)
			membership := ids.ObjectAddress{}
			if foreign {
				membership = receiver.Address()
				received.Members = append(received.Members, address)
			}
			next, err := catalog.WithExtensionMembership(d, membership, d.Revision())
			if err != nil {
				t.Fatal(err)
			}
			state.SetDescriptor(address, next)
		}
		if foreign {
			state.SetDescriptor(received.Address(), &received)
		}
		state.UnbindName(catalog.DescriptorNameKey(origin))
		state.RemoveDescriptor(origin.Address())
		current := view.WithState(state)
		ltreeComparisonSurvivorSupport(t, current, recipe, other)
		for index := range int(sqltype.LtreeArrayOperationCount) {
			proc := current.MustProcedure(recipe.Members[extension.LtreeArrayProcedureStart+index])
			operation, ok := extension.LtreeArrayProcedure(current, proc)
			if !ok || operation != sqltype.LtreeArrayOperation(index) {
				t.Fatal("surviving released role lost", foreign, index)
			}
			symbol, err := extension.ProcedureSymbol(current, proc)
			if err != nil || symbol != proc.Display.Source {
				t.Fatal("surviving symbol lost", err)
			}
			operator, _ := current.Operator(recipe.Members[extension.LtreeArrayOperatorStart+index])
			if err := catalog.ValidateInstalledClassOrigin(current, operator); err != nil {
				t.Fatal("surviving operator lost", err)
			}
			for _, cause := range []string{"copied_origin", "foreign_cohort", "principal", "source", "signature", "result", "provider", "missing_origin"} {
				broken := catalog.CloneState(state)
				bad := *proc
				switch cause {
				case "copied_origin":
					copied := *recipe
					bad.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: proc.Origin.Declaration}
				case "foreign_cohort":
					bad.Origin = &catalog.InstalledOrigin{Installation: other, Declaration: proc.Origin.Declaration}
				case "principal":
					bad.Owner = (&authorization.Domain{}).Principal(proc.Owner.ID())
				case "source":
					bad.Display.Source = "ltree_in"
				case "signature":
					bad.Signature = "25,25"
				case "result":
					bad.Result = 25
				case "provider":
					bad.Implementation = extension.StockHstore
				case "missing_origin":
					bad.Origin = nil
				}
				broken.SetDescriptor(bad.Address(), &bad)
				if _, ok := extension.LtreeArrayProcedure(view.WithState(broken), &bad); ok {
					t.Fatal("forged procedure granted array role", cause, index)
				}
				if err := catalog.ValidateInstalledClassOrigin(view.WithState(broken), operator); err == nil {
					t.Fatal("operator ignored forged current backer", cause, index)
				}
			}
		}
		for _, slot := range []int{0, 3, 6} {
			scalar := current.MustType(recipe.Members[slot])
			if err := extension.ValidateTypeOrigin(current, scalar); err != nil {
				t.Fatal("surviving nominal rejected", err)
			}
			array := current.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(scalar.Array)})
			if array.SQLType.Concrete() != (slot != 6) {
				t.Fatal("membership granted lookup-only array execution")
			}
			if err := (stored.TypeProvider{}).Validate(current, array); err != nil {
				t.Fatal("surviving companion rejected", err)
			}
			for _, cause := range []string{"copied_origin", "principal", "association"} {
				broken := catalog.CloneState(state)
				bad := *scalar
				switch cause {
				case "copied_origin":
					copied := *recipe
					bad.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: slot}
				case "principal":
					bad.Owner = (&authorization.Domain{}).Principal(scalar.Owner.ID())
				case "association":
					bad.Array++
				}
				broken.SetDescriptor(bad.Address(), &bad)
				if err := extension.ValidateTypeOrigin(view.WithState(broken), &bad); err == nil {
					t.Fatal("forged nominal accepted", cause)
				}
			}
		}
	}
}

func TestLtreeArrayOwnerCodec(t *testing.T) {
	e, _, origin, _ := lqueryProviderInstalled(t, false)
	view := e.CatalogRead()
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, view)
	metadata, err := extension.OwnerMetadata(view, roles, origin)
	if err != nil {
		t.Fatal(err)
	}
	record, present, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !present {
		t.Fatal("genuine sole v3 decode", err)
	}
	if len(record.Members) != len(origin.Installation.Members)+3 {
		t.Fatal("recipe/generated companion owner inventory", len(record.Members))
	}
	seen := make([]bool, extension.LtreeMemberCount)
	first := -1
	for index, member := range record.Members {
		if member.Declaration == nil {
			continue
		}
		slot := *member.Declaration
		if slot < 0 || slot >= len(seen) || seen[slot] {
			t.Fatal("invalid released owner slot", slot)
		}
		seen[slot] = true
		if slot == extension.LtreeArrayProcedureStart {
			first = index
		}
	}
	for slot, present := range seen {
		if !present {
			t.Fatal("missing released declaration", slot)
		}
	}
	if first < 0 {
		t.Fatal("missing array declaration")
	}
	if _, err := extension.ImportOwners(context.Background(), view.WithState(catalog.CloneState(view.State())), roles, []extension.OwnerRecord{record}); err != nil {
		t.Fatal("genuine full-recipe owner import", err)
	}
	for _, cause := range []string{"declaration", "signature", "member", "version"} {
		bad := record
		bad.Members = slices.Clone(record.Members)
		switch cause {
		case "declaration":
			slot := extension.LtreeArrayOperatorStart
			bad.Members[first].Declaration = &slot
		case "signature":
			bad.Members[first].Signature = "pg_catalog.text, pg_catalog.text"
		case "member":
			bad.Members = append(bad.Members, bad.Members[first])
		case "version":
			bad.Version = "1.2"
		}
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		decoded, present, err := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data))
		if !present {
			t.Fatal("unrecognized sole v3 marker")
		}
		if err == nil {
			_, err = extension.ImportOwners(context.Background(), view.WithState(catalog.CloneState(view.State())), roles, []extension.OwnerRecord{decoded})
		}
		if err == nil {
			t.Fatal("forged array owner authority accepted", cause)
		}
	}
}

// A scalar's genuine surviving codec graph does not retain unrelated array
// procedures or their argument types after the originating extension retires.
func TestLtreeScalarResidualReplay(t *testing.T) {
	for _, slot := range []int{0, 3, 6} {
		t.Run([]string{"lquery", "ltree", "ltxtquery"}[slot/3], func(t *testing.T) {
			e, op, origin, _ := lqueryProviderInstalled(t, false)
			recipe := origin.Installation
			declarations := slices.Clone(recipe.Declarations)
			if !recipe.Valid() || len(declarations) != extension.LtreeMemberCount {
				t.Fatal("incomplete genuine released recipe")
			}
			for _, address := range recipe.Members[slot : slot+3] {
				view := e.CatalogRead()
				sql, err := extension.MemberSQL(view, view.Get(address))
				if err != nil {
					t.Fatal(err)
				}
				enginetest.RoleExecute(t, e, op, "ALTER EXTENSION ltree DROP "+sql)
			}
			enginetest.RoleExecute(t, e, op, "DROP EXTENSION ltree")
			view := e.CatalogRead()
			scalar := view.MustType(recipe.Members[slot])
			arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(scalar.Array)}
			companion := view.MustType(arrayAddress)
			if companion.SQLType.Element() != scalar.SQLType || companion.Owner != scalar.Owner ||
				companion.Extension != (ids.ObjectAddress{}) || catalog.DescriptorOrigin(companion) != nil {
				t.Fatal("generated companion lost its real scalar ownership")
			}
			if view.Get(origin.Address()) != nil || scalar.Extension != (ids.ObjectAddress{}) {
				t.Fatal("retired origin or mutable membership survived")
			}
			for index, address := range recipe.Members {
				current := view.Get(address)
				if (current != nil) != (index >= slot && index < slot+3) {
					t.Fatal("unexpected surviving declaration", index)
				}
				if recipe.Declarations[index] != declarations[index] {
					t.Fatal("immutable declaration changed", index)
				}
				if current != nil && (!catalog.SameInstallation(current, recipe) || catalog.ExtensionMembership(current) != (ids.ObjectAddress{})) {
					t.Fatal("survivor lost original authority", index)
				}
			}
			if err := (stored.TypeProvider{}).Validate(view, scalar); err != nil {
				t.Fatal("genuine surviving scalar rejected", err)
			}
			// Rendering omitted declarations must not relax the real survivor's
			// required current companion and nominal argument authority.
			broken := catalog.CloneState(view.State())
			broken.RemoveDescriptor(arrayAddress)
			if err := (stored.TypeProvider{}).Validate(view.WithState(broken), scalar); err == nil {
				t.Fatal("missing required surviving array accepted")
			}
			broken = catalog.CloneState(view.State())
			broken.RemoveDescriptor(scalar.Address())
			output := view.MustProcedure(recipe.Members[slot+2])
			if _, err := extension.ProcedureSymbol(view.WithState(broken), output); err == nil {
				t.Fatal("missing required surviving nominal argument accepted")
			}
			var script bytes.Buffer
			if err := engine.Dump(context.Background(), e, &script); err != nil {
				t.Fatal("residual installation replay", err)
			}
			if e.CatalogRead().State() != view.State() || view.Get(scalar.Address()) != scalar || view.Get(output.Address()) != output {
				t.Fatal("rendering changed surviving catalog generations")
			}
			text := script.String()
			if strings.Count(text, `CREATE EXTENSION "ltree" `) != 1 || strings.Count(text, "DROP EXTENSION \"ltree\";\n") != 1 {
				t.Fatal("residual graph did not retire through its genuine origin", text)
			}
			metadata, err := extension.InstallationOwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), recipe, nil)
			if err != nil {
				t.Fatal(err)
			}
			record, present, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
			// Generated companions are owned by the scalar serializer after
			// detachment, not invented explicit installation declarations.
			if err != nil || !present || record.Present || len(record.Members) != 3 {
				t.Fatal("explicit scalar/codec survivor inventory", record, err)
			}
			for _, member := range record.Members {
				if member.Declaration == nil || *member.Declaration < slot || *member.Declaration >= slot+3 {
					t.Fatal("foreign explicit declaration in survivor inventory", member)
				}
			}
			if !recipe.Valid() || !slices.Equal(recipe.Declarations, declarations) {
				t.Fatal("replay changed immutable recipe")
			}
		})
	}
}
