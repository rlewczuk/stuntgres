package engine

import (
	"context"
	"iter"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type namedDefinitionReader struct{ name string }

func (namedDefinitionReader) Descriptor(ids.ObjectAddress) (catalog.Descriptor, bool) {
	return nil, false
}
func (namedDefinitionReader) Lookup(ids.NameKey) ids.ObjectAddress { return ids.ObjectAddress{} }
func (namedDefinitionReader) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(func(ids.ObjectAddress, catalog.Descriptor) bool) {}
}
func (namedDefinitionReader) ProceduresNamed(ids.ObjectAddress, string) iter.Seq[*catalog.Procedure] {
	return func(func(*catalog.Procedure) bool) {}
}
func (namedDefinitionReader) HasDynamicProcedureName(string) bool      { return false }
func (namedDefinitionReader) HasProviderProcedureOverride(string) bool { return false }

type foreignAuthority struct{}

func (foreignAuthority) AnalyzeAuthority()         {}
func (foreignAuthority) ReadCatalog() catalog.Read { return catalog.Read{} }

func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s did not panic", what)
		}
	}()
	f()
}

// A binding authority is the binder's stored authority read through the
// operation's current definition reader, as bindingAuthorization did by
// copying the view. Without a stored authority it is a fresh one. Raw stored
// authorities keep their own reader.
func TestBindingAuthorityOverlaysOperationDefinition(t *testing.T) {
	e := New()
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	host := e.AnalysisHost(op)
	current, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}

	fresh, err := host.BindingAuthority(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := authorizationOf(fresh); got.State.CurrentUser != current.State.CurrentUser || got.Catalog.Epoch() != current.Catalog.Epoch() || got.Definition != op.Definition {
		t.Fatalf("fresh binding authority = %+v, want the operation's current authority", got.State)
	}

	own, opReader := namedDefinitionReader{"own"}, namedDefinitionReader{"op"}
	base := current
	base.Definition = own
	raw := analyze.Authority(&base)
	op.Definition = nil
	if got, _ := host.BindingAuthority(raw); got != raw {
		t.Fatal("without an operation definition the stored authority is not returned as it is")
	}
	if got := host.BindingDefinition(raw); got != own {
		t.Fatalf("BindingDefinition without an operation definition = %v, want the stored reader", got)
	}
	if got := host.BindingDefinition(nil); got != nil {
		t.Fatalf("BindingDefinition(nil) = %v, want none", got)
	}

	op.Definition = opReader
	defer func() { op.Definition = nil }()
	overlay, err := host.BindingAuthority(raw)
	if err != nil || overlay == raw {
		t.Fatal("a set operation definition does not overlay the stored authority", err)
	}
	if again, _ := host.BindingAuthority(overlay); again != overlay {
		t.Fatal("an overlaid authority is overlaid again")
	}
	if overlay.ReadCatalog().Epoch() != base.Catalog.Epoch() || overlay.ReadCatalog().Owner() != base.Catalog.Owner() {
		t.Fatal("the overlay changed the stored catalog")
	}
	if got := host.DefinitionOf(overlay); got != opReader {
		t.Fatalf("DefinitionOf(overlay) = %v, want the operation's reader", got)
	}
	if got := host.view(overlay).Definition; got != opReader {
		t.Fatalf("overlay view reader = %v, want the operation's reader", got)
	}
	if got := host.DefinitionOf(raw); got != own {
		t.Fatalf("DefinitionOf(raw) = %v, want the stored reader", got)
	}
	if got := host.view(raw).Definition; got != own {
		t.Fatalf("raw view reader = %v, want the stored reader", got)
	}
	if got := host.BindingDefinition(raw); got != opReader {
		t.Fatalf("BindingDefinition = %v, want the operation's reader", got)
	}
	if base.Definition != own {
		t.Fatal("binding changed the stored authority")
	}

	mustPanic(t, "view of a foreign authority", func() { host.view(foreignAuthority{}) })
	mustPanic(t, "authorizationOf an overlay", func() { authorizationOf(overlay) })
	if authorizationOf(nil) != nil || authorizationOf(raw) != &base {
		t.Fatal("authorizationOf does not recover the stored view")
	}
}
