package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type tablespaceDefaults struct { calls int }

func (p *tablespaceDefaults) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	p.calls++
	if d.Name == "default_tablespace" { return "provider_missing_space", true, nil }
	return "", false, nil
}

func TestDefaultTablespaceAdmissionCapture(t *testing.T) {
	e := engine.New()
	provider := &tablespaceDefaults{}
	s := enginetest.StartSession(t, e, provider)
	peer := enginetest.StartSession(t, e, nil)
	captured := provider.calls
	op, _, err := s.AcquireCommand(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	if op.DefaultTablespace != "provider_missing_space" || (core.DDLHost{}).DefaultTablespace() != "" {
		t.Fatal("admission or internal boot capture", op.DefaultTablespace)
	}
	other, err := peer.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	if other.DefaultTablespace != "" { t.Fatal("session settings leaked", other.DefaultTablespace) }
	other.Done()
	// The operation is an immutable admission image until the existing nested
	// invocation refresh seam is explicitly called with the effective store.
	s.Settings.Mu.Lock()
	if s.Settings.Values == nil { s.Settings.Values = make(map[string]string) }
	s.Settings.Values["default_tablespace"] = "pg_global"
	s.Settings.Typed["default_tablespace"] = settings.Value{Text: "pg_global"}
	if op.DefaultTablespace != "provider_missing_space" { t.Fatal("operation recaptured implicitly") }
	op.RefreshInvocationSettingsLocked(s.Settings)
	if op.DefaultTablespace != "pg_global" { t.Fatal("nested entry failed to refresh") }
	delete(s.Settings.Values, "default_tablespace")
	delete(s.Settings.Typed, "default_tablespace")
	op.RefreshInvocationSettingsLocked(s.Settings)
	s.Settings.Mu.Unlock()
	if op.DefaultTablespace != "provider_missing_space" || provider.calls != captured {
		t.Fatal("nested exit failed to restore captured default", op.DefaultTablespace, provider.calls, captured)
	}
}

func TestDefaultTablespaceProviderFallbackCreation(t *testing.T) {
	e := engine.New()
	provider := &tablespaceDefaults{}
	s := enginetest.StartSession(t, e, provider)
	captured := provider.calls
	op, err := s.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	if op.DefaultTablespace != "provider_missing_space" { t.Fatal("ordinary acquisition lost trusted default") }
	// Exercise the actual SQL creation owner directly, not its policy helper or
	// a SQL execution shim. No physical tablespace provider is installed.
	syntax := &parser.CreateTable{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ts_provider_destination"}}},
		Elements: []parser.TableElement{{Column: &parser.Column{Name: "v", TypeName: parser.TypeName{Name: "int4"}}}},
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { e.Mu.Unlock(); t.Fatal(err) }
	stage := e.BeginCatalogStage(auth.Catalog)
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	err = tablecmds.SchemaTableLocked(e, op, auth, stage, syntax, namespace, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	if err == nil {
		address, found := stage.NameBinding(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "ts_provider_destination"})
		if !found { e.Mu.Unlock(); t.Fatal("creation owner did not stage descriptor") }
		d := stage.Get(address).(*catalog.Relation)
		if d.RelationKind != catalog.TableRelation || d.Persistence != "" || d.TableMethod != 2 || d.RowType == 0 {
			e.Mu.Unlock(); t.Fatal("fallback changed logical destination", d)
		}
	}
	e.DiscardCatalogStage(stage)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	if provider.calls != captured { t.Fatal("creation reentered provider", provider.calls, captured) }
}
