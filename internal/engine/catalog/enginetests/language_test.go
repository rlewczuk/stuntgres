package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

func TestLanguageCapabilityAuthority(t *testing.T) {
	base := engine.New().CatalogRead()
	foreign := engine.New().CatalogRead()
	language, err := catalog.LookupLanguage(base, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []ids.ObjectAddress{language.Call, language.Inline, language.Validator} {
		original, ok := base.Procedure(address)
		if !ok {
			t.Fatal("missing actual handler", address)
		}
		if err := catalog.ValidatePLHandler(base, original); err != nil {
			t.Fatal(err)
		}
		if err := catalog.ValidatePLHandler(foreign, original); err == nil {
			t.Fatal("foreign capability admitted")
		}
		for _, tc := range []struct {
			name    string
			corrupt func(*catalog.Procedure)
		}{
			{"kind", func(d *catalog.Procedure) { d.PLHandler = catalog.NoPLHandler }},
			{"payload", func(d *catalog.Procedure) { d.SQL = &proc.Definition{} }},
			{"principal", func(d *catalog.Procedure) { d.Owner = foreign.Owner().Principals.Principal(d.Owner.ID()) }},
			{"revision", func(d *catalog.Procedure) { d.ObjectRevision = 0 }},
			{"signature", func(d *catalog.Procedure) { d.Signature = "23" }},
			{"result", func(d *catalog.Procedure) { d.Result = 23 }},
		} {
			t.Run(original.Name()+"/"+tc.name, func(t *testing.T) {
				// A private state is crucial: rejection must not mutate publication.
				next := *original
				tc.corrupt(&next)
				candidate := catalog.NewCandidate(base)
				candidate.UpdateDescriptor(&next)
				private := base.WithState(candidate.State())
				if err := catalog.ValidatePLHandler(private, &next); err == nil {
					t.Fatal("forged capability admitted")
				}
				if base.Get(address) != original || catalog.ValidatePLHandler(base, original) != nil {
					t.Fatal("rejected candidate corrupted original")
				}
			})
		}
		candidate := catalog.NewCandidate(base)
		next := *original
		next.ObjectRevision = original.Revision() + 1
		if err := candidate.Replace(&next); err != nil {
			t.Fatal(err)
		}
		private := base.WithState(candidate.State())
		if err := catalog.ValidatePLHandler(private, original); err == nil {
			t.Fatal("stale handler admitted")
		}
		if err := catalog.ValidatePLHandler(private, &next); err != nil {
			t.Fatal("current immutable handler rejected", err)
		}
	}
}

func TestLanguageOptionalHandlerState(t *testing.T) {
	base := engine.New().CatalogRead()
	original, err := catalog.LookupLanguage(base, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	for _, mask := range []int{0, 1, 2, 3} {
		next := *original
		next.ObjectRevision = original.Revision() + 1
		if mask&1 == 0 {
			next.Inline = ids.ObjectAddress{}
		}
		if mask&2 == 0 {
			next.Validator = ids.ObjectAddress{}
		}
		candidate := catalog.NewCandidate(base)
		if err := candidate.Replace(&next); err != nil {
			t.Fatal(err)
		}
		private := base.WithState(candidate.State())
		if err := (catalog.LanguageProvider{}).Validate(private, &next); err != nil {
			t.Fatalf("optional mask %d rejected: %v", mask, err)
		}
		if base.Get(original.Address()) != original || original.Inline == (ids.ObjectAddress{}) || original.Validator == (ids.ObjectAddress{}) {
			t.Fatal("optional replacement mutated retained descriptor")
		}
	}
}

func TestLanguageDefinitionDependencies(t *testing.T) {
	base := engine.New().CatalogRead()
	language, err := catalog.LookupLanguage(base, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := base.Procedure(language.Call)
	routine := *handler
	routine.PLHandler = catalog.NoPLHandler
	routine.Language = language.Address()
	seen := map[ids.ObjectAddress]int{}
	catalog.VisitProcedureDeclarationDependencies(&routine, func(a ids.ObjectAddress) bool { seen[a]++; return true })
	if seen[language.Address()] != 1 {
		t.Fatalf("routine language dependency missing/duplicated: %v", seen)
	}
	if seen[ids.ObjectAddress{}] != 0 {
		t.Fatal("zero dependency leaked")
	}
	visited := false
	complete := catalog.VisitProcedureDeclarationDependencies(&routine, func(ids.ObjectAddress) bool { visited = true; return false })
	if !visited || complete {
		t.Fatal("dependency visitor ignored stop")
	}
}

func TestLanguageIdentityPublication(t *testing.T) {
	base := engine.New().CatalogRead()
	old, err := catalog.LookupLanguage(base, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	candidate := catalog.NewCandidate(base)
	next := *old
	next.ObjectName = "language_private_rename"
	next.ObjectRevision = old.Revision() + 1
	if err := candidate.Replace(&next); err != nil {
		t.Fatal(err)
	}
	private := base.WithState(candidate.State())
	if private.NameAddress(catalog.DescriptorNameKey(old)) != (ids.ObjectAddress{}) || private.NameAddress(catalog.DescriptorNameKey(&next)) != old.Address() {
		t.Fatal("private rename lost address or retained old alias")
	}
	if private.Get(old.Address()) != &next || next.Revision() <= old.Revision() || next.Owner != old.Owner || next.Call != old.Call || next.Inline != old.Inline || next.Validator != old.Validator {
		t.Fatal("rename changed immutable capability/owner")
	}
	if err := (catalog.LanguageProvider{}).Validate(private, &next); err != nil {
		t.Fatal("name incorrectly confers capability", err)
	}
	if err := (catalog.LanguageProvider{}).Validate(private, old); err == nil {
		t.Fatal("stale descriptor accepted")
	}
	if base.Get(old.Address()) != old || base.NameAddress(catalog.DescriptorNameKey(old)) != old.Address() || base.NameAddress(catalog.DescriptorNameKey(&next)) != (ids.ObjectAddress{}) {
		t.Fatal("unpublished candidate changed live snapshot")
	}
}

func TestLanguageReplayRemap(t *testing.T) {
	base := engine.New().CatalogRead()
	original, err := catalog.LookupLanguage(base, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	for _, optional := range []bool{false, true} {
		source := *original
		if !optional {
			source.Inline = ids.ObjectAddress{}
			source.Validator = ids.ObjectAddress{}
		}
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
		for _, a := range []ids.ObjectAddress{source.Address(), source.Call, source.Inline, source.Validator} {
			if a == (ids.ObjectAddress{}) {
				continue
			}
			b := a
			b.ObjectID += 100000
			c := a
			c.ObjectID += 200000
			mapping[a] = b
			mapping[b] = c
		}
		next := extension.RemapDescriptor(&source, mapping).(*catalog.Language)
		if next == &source || next.Address() != mapping[source.Address()] || next.Call != mapping[source.Call] {
			t.Fatal("remap missing or repeated")
		}
		if optional {
			if next.Inline != mapping[source.Inline] || next.Validator != mapping[source.Validator] {
				t.Fatal("optional refs not remapped exactly once")
			}
		} else if next.Inline != (ids.ObjectAddress{}) || next.Validator != (ids.ObjectAddress{}) {
			t.Fatal("absent handlers acquired references")
		}
		if source.Address() != original.Address() || source.Call != original.Call || base.Get(original.Address()) != original {
			t.Fatal("replay mutated source backing")
		}
	}
}
