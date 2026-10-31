package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

func TestSelectExactRoutineCurrentBuiltinIdentity(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	identity, ok := catalog.BuiltinRoutineIdentityBySignature("pg_get_userbyid", "26")
	if !ok {
		t.Fatal("missing released identity")
	}
	original, ok := base.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID})
	if !ok {
		t.Fatal("missing installed builtin")
	}
	pg := original.Namespace()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	for _, tc := range []struct {
		name, currentName, lookup string
		move, visible, absent     bool
		want                      int
	}{
		{"unchanged", original.Name(), original.Name(), false, false, false, 1},
		{"renamed_old_alias", "identity_userbyid", original.Name(), false, false, false, 0},
		{"renamed_current", "identity_userbyid", "identity_userbyid", false, false, false, 1},
		{"pure_move_hidden", original.Name(), original.Name(), true, false, false, 0},
		{"pure_move_visible_after_pg_catalog", original.Name(), original.Name(), true, true, false, 1},
		{"absent_release_identity", original.Name(), original.Name(), false, false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(base.State())
			state.UnbindName(catalog.DescriptorNameKey(original))
			current := *original
			current.ObjectName = tc.currentName
			if tc.move {
				current.ObjectNamespace = public
			}
			if tc.absent {
				state.RemoveDescriptor(original.Address())
			} else {
				state.SetDescriptor(current.Address(), &current)
				state.BindName(catalog.DescriptorNameKey(&current), current.Address())
				index := catalog.ProcedureIndexEdit{Index: state.ProcedureNames()}
				index.Replace(original, &current)
				state.SetProcedureNames(index.Index)
			}
			path := []ids.ObjectAddress{pg}
			if tc.visible {
				path = append(path, public)
			}
			selected, count := regobj.SelectExactRoutine(base.WithState(state), analyze.NamespaceSelection{Path: path}, tc.lookup, original.Signature)
			if count != tc.want {
				t.Fatalf("count=%d want %d; selected=%+v", count, tc.want, selected)
			}
			if count == 1 {
				namespace := current.Namespace().ObjectID
				if tc.absent {
					namespace = identity.Namespace
				}
				if selected.ID != identity.ID || selected.Namespace != namespace || selected.Name != tc.lookup {
					t.Fatalf("wrong current identity: %+v", selected)
				}
			}
		})
	}
}
