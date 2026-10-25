package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

type capabilityUpdates struct{}

func (capabilityUpdates) ExecForeignUpdate(context.Context, catalog.ForeignModifyRequest, any, catalog.ForeignRowIdentity, catalog.ForeignRow) (catalog.ForeignRow, bool, error) {
	panic("metadata inspection executed a foreign update")
}
func (capabilityUpdates) ExecForeignDelete(context.Context, catalog.ForeignModifyRequest, any, catalog.ForeignRowIdentity, catalog.ForeignRow) (catalog.ForeignRow, bool, error) {
	panic("metadata inspection executed a foreign delete")
}

type capabilityUpdateWrapper struct {
	fdwUnitScanWrapper
	capabilityUpdates
}
type capabilityFullWrapper struct {
	fdwUnitSingle
	capabilityUpdates
}

func TestViewCapabilitiesForeignEvents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wrapper catalog.ForeignDataWrapper
		want    uint32
	}{
		{"read", fdwUnitScanWrapper{}, 0},
		{"insert", fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{}}, 8},
		{"update_delete", capabilityUpdateWrapper{}, 20},
		{"all", capabilityFullWrapper{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{}}}, 28},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "capability_handler"}, Wrapper: tc.wrapper})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			state := catalog.CloneState(e.CatalogRead().State())
			handler := state.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "capability_handler", Signature: string(catalog.ForeignHandlerSignature)})
			wrapper := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: 92001}, ObjectName: "capability_wrapper", ObjectRevision: 1, Handler: handler}
			server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 92002}, ObjectName: "capability_server", ObjectRevision: 1, Wrapper: wrapper.Address()}
			relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 92003}, ObjectName: "capability_foreign", ObjectRevision: 1, RelationKind: catalog.ForeignRelation, Attributes: []int32{1}, Foreign: &catalog.ForeignTableDefinition{Server: server.Address()}}
			for _, d := range []catalog.Descriptor{wrapper, server, relation} {
				state.SetDescriptor(d.Address(), d)
			}
			read := e.CatalogRead().WithState(state)
			for _, subset := range [][]int32{nil, {}, {1}} {
				got, err := view.Capabilities(context.Background(), read, relation, subset, catalogViewLeafEvents)
				if err != nil || got != tc.want {
					t.Fatalf("subset%v: %d, %v; want%d", subset, got, err, tc.want)
				}
			}
			missing := catalog.CloneState(state)
			missing.RemoveDescriptor(server.Address())
			if got, err := view.Capabilities(context.Background(), read.WithState(missing), relation, nil, catalogViewLeafEvents); got != 0 || !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatalf("missing server: %d, %v", got, err)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if got, err := view.Capabilities(canceled, read, relation, nil, catalogViewLeafEvents); got != 0 || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled: %d, %v", got, err)
			}
		})
	}
}

func TestBuiltinViewRoutineIdentity(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	identity, ok := catalog.BuiltinRoutineIdentityBySignature("pg_get_userbyid", "26")
	if !ok {
		t.Fatal("missing released identity")
	}
	original, ok := base.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: identity.ID})
	if !ok {
		t.Fatal("missing installed builtin")
	}
	relationAddress, ok := base.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: original.Namespace(), Name: "pg_views"})
	if !ok {
		t.Fatal("missing pg_views identity")
	}
	relation, ok := base.Relation(relationAddress)
	if !ok {
		t.Fatal("missing pg_views")
	}
	pg := original.Namespace()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	for _, tc := range []struct {
		name, currentName, want                 string
		move, visible, absent, missingNamespace bool
	}{
		{name: "renamed", currentName: "identity_userbyid", want: "identity_userbyid"},
		{name: "moved_hidden", currentName: "pg_get_userbyid", move: true, want: "public.pg_get_userbyid"},
		{name: "moved_visible", currentName: "pg_get_userbyid", move: true, visible: true, want: "pg_get_userbyid"},
		{name: "renamed_moved", currentName: "identity_userbyid", move: true, want: "public.identity_userbyid"},
		{name: "absent_display_only", absent: true, want: "pg_get_userbyid"},
		{name: "missing_namespace", move: true, currentName: "pg_get_userbyid", missingNamespace: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(base.State())
			state.UnbindName(catalog.DescriptorNameKey(original))
			if tc.absent {
				state.RemoveDescriptor(original.Address())
			} else {
				current := *original
				current.ObjectName = tc.currentName
				current.ObjectRevision++
				if tc.move {
					current.ObjectNamespace = public
				}
				state.SetDescriptor(current.Address(), &current)
				state.BindName(catalog.DescriptorNameKey(&current), current.Address())
				index := catalog.ProcedureIndexEdit{Index: state.ProcedureNames()}
				index.Replace(original, &current)
				state.SetProcedureNames(index.Index)
			}
			if tc.missingNamespace {
				state.RemoveDescriptor(public)
			}
			read := base.WithState(state)
			runtime := EngineCatalogRuntime{presentation: stored.Presentation{Path: []ids.ObjectAddress{pg}}}
			if tc.visible {
				runtime.presentation.Path = []ids.ObjectAddress{pg, public}
			}
			for _, quote := range []bool{false, true} {
				runtime.presentation.Output.QuoteAllIdentifiers = quote
				got, err := runtime.builtinViewDefinition(context.Background(), read, relation)
				if tc.missingNamespace {
					if got != "" || !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatalf("inconsistent namespace published %q, %v", got, err)
					}
					continue
				}
				want := tc.want
				argument := "c.relowner"
				if quote {
					want = `"` + strings.ReplaceAll(want, ".", `"."`) + `"`
					argument = `"c"."relowner"`
				}
				if err != nil || !strings.Contains(got, want+"("+argument+")") {
					t.Fatalf("quote=%t definition=%q err=%v; want call %s(%s)", quote, got, err, want, argument)
				}
				// A newer descriptor must not leak into a previously captured read.
				old, err := runtime.builtinViewDefinition(context.Background(), base, relation)
				oldCall := "pg_get_userbyid(c.relowner)"
				if quote {
					oldCall = `"pg_get_userbyid"("c"."relowner")`
				}
				if err != nil || !strings.Contains(old, oldCall) {
					t.Fatalf("old capture changed: %q, %v", old, err)
				}
				if tc.absent {
					if _, exists := read.Procedure(original.Address()); exists {
						t.Fatal("display installed an absent routine")
					}
				}
			}
		})
	}
}

func TestSelectExactRoutineCurrentBuiltinIdentity(t *testing.T) {
	e := New()
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
