package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestTSConfigurationPinnedDropAdmission(t *testing.T) {
	e := engine.New()
	original := e.CatalogRead()
	seed := original.MustTSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
	state := catalog.CloneState(original.State())
	dictionary := *original.MustTSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
	dictionary.ObjectAddress.ObjectID = 48001
	dictionary.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	dictionary.ObjectName = "config_drop_dict"
	state.SetDescriptor(dictionary.Address(), &dictionary)
	state.BindName(catalog.DescriptorNameKey(&dictionary), dictionary.Address())
	copyConfig := *seed
	copyConfig.ObjectAddress.ObjectID = 48002
	copyConfig.ObjectName = "config_drop_copy"
	copyConfig.ObjectNamespace = dictionary.Namespace()
	copyConfig.Mappings = catalog.CloneTSMappings(seed.Mappings)
	state.SetDescriptor(copyConfig.Address(), &copyConfig)
	state.BindName(catalog.DescriptorNameKey(&copyConfig), copyConfig.Address())
	base := original.WithState(state)
	if err := base.Owner().Provider(ids.TSDictionaryClassID).Validate(base, &dictionary); err != nil {
		t.Fatal("dictionary provider authority", err)
	}
	pinned := sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01",
		Message: "cannot drop text search configuration simple because it is required by the database system",
	}
	ordinary := sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01",
		Message: "cannot drop text search dictionary config_drop_dict because other objects depend on it",
		Detail:  "text search configuration config_drop_copy depends on text search dictionary config_drop_dict",
		Hint:    "Use DROP ... CASCADE to drop the dependent objects too.",
	}
	for _, tc := range []struct {
		name     string
		uses     int
		unpinned bool
		canceled bool
		mode     parser.DropMode
		want     *sqlstate.Error
	}{
		{name: "untouched", mode: parser.DropRestrict},
		{name: "repeated_pinned_restrict", uses: 2, mode: parser.DropRestrict, want: &pinned},
		{name: "repeated_pinned_cascade", uses: 2, mode: parser.DropCascade, want: &pinned},
		{name: "remaining_use", uses: 1, mode: parser.DropRestrict, want: &pinned},
		{name: "last_use_removed", mode: parser.DropRestrict},
		{name: "ordinary_restrict", uses: 2, unpinned: true, mode: parser.DropRestrict, want: &ordinary},
		{name: "canceled_before_pin", uses: 2, canceled: true, mode: parser.DropRestrict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mappingStage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(mappingStage)
			next := *seed
			if tc.unpinned {
				next = copyConfig
			}
			next.ObjectRevision++
			next.Mappings = catalog.CloneTSMappings(next.Mappings)
			for i := range next.Mappings {
				if next.Mappings[i].TokenID == 1 && tc.uses > 0 || next.Mappings[i].TokenID == 3 && tc.uses > 1 {
					next.Mappings[i].Dictionaries = []ids.ObjectAddress{dictionary.Address(), dictionary.Address()}
				}
			}
			if err := mappingStage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			// DROP consumes the complete definition input of the preceding
			// mapping command, not an in-progress mapping edit in its own stage.
			input := e.StageRead(mappingStage)
			if err := (catalog.TSConfigurationProvider{}).Validate(input, &next); err != nil {
				t.Fatal("current staged configuration authority", err)
			}
			if got := slices.Contains(input.MustTSConfiguration(next.Address()).Dependencies(), dictionary.Address()); got != (tc.uses > 0) {
				t.Fatal("current definition lost dictionary dependency", got, tc.uses)
			}
			stage := e.BeginCatalogStage(input)
			defer e.DiscardCatalogStage(stage)
			mappings := catalog.CloneTSMappings(next.Mappings)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			provider := core.StageDropProvider(ctx, stage, nil, nil, nil)
			err := provider.ExpressionDependencies([]ids.ObjectAddress{dictionary.Address()}, tc.mode, ids.ObjectAddress{})
			if tc.canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost precedence: %v", err)
				}
			} else if tc.want != nil {
				var got *sqlstate.Error
				if !errors.As(err, &got) || *got != *tc.want {
					t.Fatalf("drop admission diagnostic: got %#v (%v), want %#v", got, err, tc.want)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			retireDictionary := tc.want == nil && !tc.canceled
			wantRemovals := 0
			if retireDictionary {
				wantRemovals = 1
			}
			current := e.StageRead(stage)
			if stage.RemovalCount() != wantRemovals || (current.Get(dictionary.Address()) == nil) != retireDictionary {
				t.Fatal("dependency admission staged incorrect dictionary retirement", stage.RemovalCount(), err)
			}
			if current.Get(next.Address()) != &next || !reflect.DeepEqual(next.Mappings, mappings) {
				t.Fatal("dependency preflight mutated private configuration")
			}
			if e.CatalogRead().State() != original.State() || original.Get(seed.Address()) != seed {
				t.Fatal("dependency preflight published a catalog change")
			}
		})
	}
}
