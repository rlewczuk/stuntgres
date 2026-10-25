package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestCandidateDefinitionProviderOverrides(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	oldKey := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "abs", Signature: "23"}
	original, ok := base.Procedure(base.NameAddress(oldKey))
	if !ok || original.Builtin == nil {
		t.Fatal("missing genuine released abs declaration")
	}
	for _, mode := range []string{"rename", "move", "rename_and_move"} {
		t.Run(mode, func(t *testing.T) {
			candidate := catalog.NewCandidate(base)
			next := *original
			next.ObjectRevision++
			if mode != "move" {
				next.ObjectName = "candidate_abs"
			}
			if mode != "rename" {
				next.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			}
			if err := candidate.Replace(&next); err != nil {
				t.Fatal(err)
			}
			reader := candidate.Definitions()
			newKey := catalog.DescriptorNameKey(&next)
			if reader.Lookup(oldKey) != (ids.ObjectAddress{}) || reader.Lookup(newKey) != original.Address() {
				t.Fatal("candidate lookup resurrected the vacated provider or lost its new spelling")
			}
			for procedure := range reader.ProceduresNamed(original.Namespace(), original.Name()) {
				if procedure.Address() == original.Address() {
					t.Fatal("vacated provider remained callable", procedure.Address())
				}
			}
			found := false
			for procedure := range reader.ProceduresNamed(next.Namespace(), next.Name()) {
				if found || procedure.Address() != original.Address() || procedure.Signature != original.Signature || procedure.Builtin != original.Builtin {
					t.Fatal("candidate callable lost immutable released declaration", procedure)
				}
				found = true
			}
			if !found || !reader.HasProviderProcedureOverride(original.Name()) || !reader.HasProviderProcedureOverride(next.Name()) || reader.HasProviderProcedureOverride("lower") {
				t.Fatal("candidate fallback suppression missed relabels or leaked to unrelated names")
			}
			if base.NameAddress(oldKey) != original.Address() || base.State().HasProviderProcedureOverride(original.Name()) || base.State().HasProviderProcedureOverride(next.Name()) {
				t.Fatal("private candidate changed published discovery")
			}

			// A later candidate must keep both published tombstones even when
			// the relabelled declaration itself is no longer in its live overlay.
			published := base.WithState(candidate.State())
			removed := catalog.NewCandidate(published)
			removed.RecordRemoval(base.Owner().Domain.Ref(published.Epoch(), next.Address()), &next)
			retired := removed.Definitions()
			if retired.Lookup(newKey) != (ids.ObjectAddress{}) || !retired.HasProviderProcedureOverride(original.Name()) || !retired.HasProviderProcedureOverride(next.Name()) {
				t.Fatal("removal reopened a published provider fallback")
			}
			for procedure := range retired.ProceduresNamed(next.Namespace(), next.Name()) {
				t.Fatal("removed provider remained callable", procedure.Address())
			}
		})
	}
	t.Run("acl_only", func(t *testing.T) {
		candidate := catalog.NewCandidate(base)
		next := *original
		next.ObjectRevision++
		next.ACL.Explicit = !original.ACL.Explicit
		if err := candidate.Replace(&next); err != nil {
			t.Fatal(err)
		}
		reader := candidate.Definitions()
		declaration, exists := reader.Descriptor(original.Address())
		current, valid := declaration.(*catalog.Procedure)
		if !exists || !valid || current.ACL.Explicit != next.ACL.Explicit || reader.Lookup(oldKey) != original.Address() {
			t.Fatal("candidate failed to expose the actual ACL replacement")
		}
		if reader.HasProviderProcedureOverride(original.Name()) || base.State().HasProviderProcedureOverride(original.Name()) || original.ACL.Explicit == next.ACL.Explicit {
			t.Fatal("ACL change was mistaken for a provider relabel or mutated its source")
		}
	})
}
