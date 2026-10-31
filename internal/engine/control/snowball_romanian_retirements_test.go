package control

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestSnowballRomanianSeedRetirements(t *testing.T) {
	const dropConfiguration = "DROP TEXT SEARCH CONFIGURATION \"pg_catalog\".\"romanian\";\n"
	const dropDictionary = "DROP TEXT SEARCH DICTIONARY \"pg_catalog\".\"romanian_stem\";\n"
	const detach = "ALTER TEXT SEARCH CONFIGURATION \"pg_catalog\".\"romanian\" DROP MAPPING IF EXISTS FOR \"asciiword\", \"word\", \"hword_part\", \"hword_asciipart\", \"asciihword\", \"hword\";\n"
	for _, tc := range []struct {
		name                             string
		dictionary, configuration, reuse bool
		want                             string
	}{
		{"intact", false, false, false, ""},
		{"configuration", false, true, false, dropConfiguration},
		{"dictionary", true, false, false, detach + dropDictionary},
		{"both", true, true, false, dropConfiguration + dropDictionary},
		{"reused-canonical-names", true, true, true, dropConfiguration + dropDictionary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := snowballUnitSource(t)
			var defaults []catalog.Descriptor
			var dictionary *catalog.TSDictionary
			var configuration *catalog.TSConfiguration
			for _, d := range source.State.Descriptors() {
				switch d := d.(type) {
				case *catalog.TSDictionary:
					if d.Seeded() && d.SeedName() != "simple" {
						defaults = append(defaults, d)
					}
					if d.SeedName() == "romanian_stem" {
						dictionary = d
					}
				case *catalog.TSConfiguration:
					if d.Seeded() && d.SeedName() != "simple" {
						defaults = append(defaults, d)
					}
					if d.SeedName() == "romanian" {
						configuration = d
					}
				}
			}
			if dictionary == nil || configuration == nil {
				t.Fatal("missing genuine installed seeds")
			}
			remove := func(d catalog.Descriptor) {
				source.State.UnbindName(catalog.DescriptorNameKey(d))
				source.State.RemoveDescriptor(d.Address())
			}
			if tc.configuration {
				remove(configuration)
			}
			if tc.dictionary {
				remove(dictionary)
				if !tc.configuration {
					changed := *configuration
					changed.ObjectRevision++
					changed.Mappings = slices.Clone(configuration.Mappings)
					for i := range changed.Mappings {
						changed.Mappings[i].Dictionaries = []ids.ObjectAddress{{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}}
					}
					source.State.SetDescriptor(changed.Address(), &changed)
				}
			}
			if tc.reuse {
				for _, d := range defaults {
					switch d := d.(type) {
					case *catalog.TSDictionary:
						if d.SeedName() == "portuguese_stem" {
							changed := *d
							changed.ObjectName = "romanian_stem"
							source.State.UnbindName(catalog.DescriptorNameKey(d))
							source.State.SetDescriptor(changed.Address(), &changed)
							source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
						}
					case *catalog.TSConfiguration:
						if d.SeedName() == "portuguese" {
							changed := *d
							changed.ObjectName = "romanian"
							source.State.UnbindName(catalog.DescriptorNameKey(d))
							source.State.SetDescriptor(changed.Address(), &changed)
							source.State.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
						}
					}
				}
			}
			script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView}}
			// These are actual installer-produced immutable descriptors, retained
			// before mutation, as owner.seed retains them in the composed engine.
			snapshot := func(reverse bool) iter.Seq[catalog.Descriptor] {
				return func(yield func(catalog.Descriptor) bool) {
					for i := range defaults {
						j := i
						if reverse {
							j = len(defaults) - 1 - i
						}
						if !yield(defaults[j]) {
							return
						}
					}
				}
			}
			for _, reverse := range []bool{false, true} {
				got, err := script.snowballSeedRetirements(t.Context(), snapshot(reverse))
				if err != nil || strings.Join(got, "") != tc.want {
					t.Fatal("exact bootstrap retirement", got, tc.want, err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if got, err := script.snowballSeedRetirements(ctx, snapshot(false)); got != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled retirement published prefix", got, err)
			}
		})
	}
}
