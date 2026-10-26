package partition

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestConsumerBoundaryCapturedGiSTIdentity(t *testing.T) {
	parent := &catalog.Relation{RelationKind: catalog.PartitionedRelation, Partition: &catalog.PartitionDefinition{}}
	assertBoundary := func(t *testing.T, read catalog.Read, statement parser.Statement, admit bool) {
		t.Helper()
		err := ConsumerBoundary(read, statement, parent, nil)
		if admit {
			if err != nil {
				t.Fatalf("boundary performed ordinary binder validation: %v", err)
			}
			return
		}
		want := sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: "0A000", Message: "partitioned indexes are unsupported",
		}
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || *diagnostic != want {
			t.Fatalf("boundary refusal = %#v, want %#v", err, want)
		}
	}
	for _, tc := range []struct {
		name   string
		method *catalog.AccessMethod
		admit  bool
	}{
		{"canonical", &catalog.AccessMethod{ObjectName: "gist", ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 783}, Native: catalog.GistAccessMethod}, true},
		{"alias", &catalog.AccessMethod{ObjectName: "gist_alias", ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 90001}, Native: catalog.GistAccessMethod}, true},
		{"spgist", &catalog.AccessMethod{ObjectName: "spgist", ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 4000}, Native: catalog.SpgistAccessMethod}, true},
		{"spgist_alias", &catalog.AccessMethod{ObjectName: "spgist_alias", ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 90002}, Native: catalog.SpgistAccessMethod}, true},
		{"name_and_oid_spoof", &catalog.AccessMethod{ObjectName: "gist", ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 783}, Native: catalog.BtreeAccessMethod}, false},
		{"brin", &catalog.AccessMethod{ObjectName: "brin", ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 3580}, Native: catalog.BrinAccessMethod}, false},
		{"missing", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.NewState(1)
			name := "gist"
			if tc.method != nil {
				state.SetDescriptor(tc.method.Address(), tc.method)
				state.BindName(catalog.DescriptorNameKey(tc.method), tc.method.Address())
				name = tc.method.Name()
			}
			read := catalog.NewRead(nil, state)
			assertBoundary(t, read, &parser.CreateIndex{Method: name}, tc.admit)
			// UNIQUE still reaches the ordinary method binder, not this boundary.
			assertBoundary(t, read, &parser.CreateIndex{Method: name, Unique: true}, true)
			assertBoundary(t, read, &parser.Reindex{}, false)
		})
	}
	for _, tc := range []struct {
		name        string
		captured    catalog.NativeAccessMethod
		replacement catalog.NativeAccessMethod
	}{
		{"retained_gist_later_btree", catalog.GistAccessMethod, catalog.BtreeAccessMethod},
		{"retained_btree_later_gist", catalog.BtreeAccessMethod, catalog.GistAccessMethod},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := &catalog.AccessMethod{
				ObjectName: "captured_alias",
				ObjectAddress: ids.ObjectAddress{
					ClassID: ids.AccessMethodClassID, ObjectID: 90001,
				},
				ObjectRevision: 1,
				Native:         tc.captured,
			}
			state := catalog.NewState(1)
			state.SetDescriptor(method.Address(), method)
			state.BindName(catalog.DescriptorNameKey(method), method.Address())
			captured := catalog.NewRead(nil, state)
			admitCaptured := tc.captured == catalog.GistAccessMethod
			statement := &parser.CreateIndex{Method: method.Name()}
			assertBoundary(t, captured, statement, admitCaptured)

			// Rename a copied descriptor in a new state, never the held state.
			renamedState := catalog.CloneState(state)
			renamed := *method
			renamed.ObjectName = "renamed_alias"
			renamed.ObjectRevision++
			renamedState.UnbindName(catalog.DescriptorNameKey(method))
			renamedState.SetDescriptor(renamed.Address(), &renamed)
			renamedState.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
			renamedRead := catalog.NewRead(nil, renamedState)
			assertBoundary(t, captured, statement, admitCaptured)
			assertBoundary(t, renamedRead, statement, false)
			assertBoundary(t, captured, &parser.CreateIndex{Method: renamed.Name()}, false)
			assertBoundary(t, renamedRead, &parser.CreateIndex{Method: renamed.Name()}, admitCaptured)

			// Reuse the old spelling with a distinct identity and opposite authority.
			replacedState := catalog.CloneState(renamedState)
			replacement := &catalog.AccessMethod{
				ObjectName: method.Name(),
				ObjectAddress: ids.ObjectAddress{
					ClassID: ids.AccessMethodClassID, ObjectID: 90002,
				},
				ObjectRevision: 1,
				Native:         tc.replacement,
			}
			replacedState.SetDescriptor(replacement.Address(), replacement)
			replacedState.BindName(catalog.DescriptorNameKey(replacement), replacement.Address())
			later := catalog.NewRead(nil, replacedState)
			assertBoundary(t, captured, statement, admitCaptured)
			assertBoundary(t, later, statement, !admitCaptured)
			assertBoundary(t, renamedRead, statement, false)
			assertBoundary(t, later, &parser.CreateIndex{Method: renamed.Name()}, admitCaptured)

			// Even competing catalogs leave UNIQUE and REINDEX in their old phases.
			for _, read := range []catalog.Read{captured, renamedRead, later} {
				assertBoundary(t, read, &parser.CreateIndex{Method: method.Name(), Unique: true}, true)
				assertBoundary(t, read, &parser.Reindex{}, false)
			}
		})
	}
}

func TestConsumerBoundaryLeavesGiSTOptionValidationToBinder(t *testing.T) {
	state := catalog.NewState(1)
	catalog.InstallBuiltinAccessMethods(state)
	parent := &catalog.Relation{RelationKind: catalog.PartitionedRelation, Partition: &catalog.PartitionDefinition{}}
	statement := &parser.CreateIndex{
		Method: "gist",
		Options: []parser.IndexOption{{
			Name:  parser.Identifier{Text: "unknown"},
			Value: parser.Value{Kind: parser.IntegerValue, Integer: 1},
		}},
	}
	if err := ConsumerBoundary(catalog.NewRead(nil, state), statement, parent, nil); err != nil {
		t.Fatalf("admission performed ordinary binder validation: %v", err)
	}
}
