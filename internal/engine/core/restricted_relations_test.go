package core_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestRestrictedRelationKindIdentity(t *testing.T) {
	for _, ns := range []ids.OID{ids.PublicNamespaceOID, 11} {
		for _, oid := range []ids.OID{16383, 16384} {
			for _, kind := range []catalog.RelationKind{catalog.ViewRelation, catalog.TableRelation, catalog.CatalogRelation} {
				for _, catalogView := range []bool{false, true} {
					d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns}, ObjectName: `a"b`, RelationKind: kind, CatalogView: catalogView}
					err := stored.AdmitView(d, settings.RestrictView)
					if !(kind == catalog.ViewRelation || kind == catalog.CatalogRelation && catalogView) || oid < 16384 {
						if err != nil {
							t.Fatal("unrestricted identity", ns, oid, kind, err)
						}
						continue
					}
					var diagnostic *sqlstate.Error
					want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: `access to non-system view "a"b" is restricted`}
					if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
						t.Fatal(ns, oid, kind, err)
					}
					if err := stored.AdmitView(d, settings.RestrictForeignTable); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	// Canonical catalog views are SQL views but have no stored.View payload.
	// Admit their current identity without recursing into a nonexistent payload.
	e := engine.New()
	state := catalog.CloneState(e.Catalog.State)
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384}, ObjectName: "provider_view", RelationKind: catalog.CatalogRelation, CatalogView: true}
	state.SetDescriptor(d.Address(), d)
	read := e.Catalog.Read(state)
	if _, err := stored.ValidateViewReadGraph(read, []exec.Leaf{{Relation: d, AnalysisOnly: true}}, settings.RestrictView); err != nil {
		t.Fatal("analysis-only occurrence was executed", err)
	}
	_, graphErr := stored.ValidateViewReadGraph(read, []exec.Leaf{{Relation: d}}, settings.RestrictView)
	var graphDiagnostic *sqlstate.Error
	if !errors.As(graphErr, &graphDiagnostic) || graphDiagnostic.Code != "55000" || graphDiagnostic.Message != `access to non-system view "provider_view" is restricted` {
		t.Fatal("canonical catalog view bypassed graph admission", graphErr)
	}
	if _, err := stored.ValidateViewReadGraph(read, []exec.Leaf{{Relation: d}}, 0); err != nil {
		t.Fatal("catalog dispatch was treated as stored SQL", err)
	}
	// The handler policy wins even before inspecting a missing handler. A
	// restricted inquiry cannot enter application code or resolve a fallback.
	_, err := fdw.HandlerFor(catalog.Read{}, nil, settings.RestrictForeignTable)
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "access to non-system foreign table is restricted"}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal(err)
	}
}

func TestRestrictedRelationKindEffectiveSettings(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	const name = "restrict_nonsystem_relation_kind"
	set := func(s *session.SettingsStore, flags settings.RestrictedRelationKinds) {
		s.Mu.Lock()
		s.Typed[name] = settings.Value{Text: "view", RestrictedRelations: flags}
		s.Mu.Unlock()
	}
	set(op.EffectiveSettings(), settings.RestrictView)
	op.Restricted = &session.RestrictedInvocation{Owner: op.Session.Authorization.State.CurrentUser}
	image := op.EffectiveSettings()
	set(op.Session.Settings, settings.RestrictForeignTable)
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if auth.RestrictedRelations() != settings.RestrictView {
		t.Fatal("live setting replaced detached invocation policy")
	}
	owner := auth.AsUser(authorization.PrincipalRef{}).(core.AuthorizationView)
	if owner.RestrictedRelations() != settings.RestrictView {
		t.Fatal("view owner changed policy source")
	}
	set(image, settings.RestrictView|settings.RestrictForeignTable)
	if auth.RestrictedRelations() != settings.RestrictView|settings.RestrictForeignTable || owner.RestrictedRelations() != auth.RestrictedRelations() {
		t.Fatal("effective policy was cached or owner-specific")
	}
	op.Restricted = nil
	live, err := core.OperationAuthorization(op)
	if err != nil || live.RestrictedRelations() != settings.RestrictForeignTable {
		t.Fatal("next operation did not read live settings", err)
	}
}
