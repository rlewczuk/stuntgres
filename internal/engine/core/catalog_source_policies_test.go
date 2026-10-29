package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// A real output formatter can reacquire the engine mutex during rendering. It
// delegates the datum's spelling to the captured ordinary relation formatter.
type policiesUnlockedClassOutput struct {
	t *testing.T
	e *core.Engine
	base sqltype.RegclassFormatter
	observed *bool
}

func (f policiesUnlockedClassOutput) FormatRegclass(oid uint32) (string, error) {
	f.t.Helper()
	if !f.e.Mu.TryLock() {
		f.t.Fatal("policy rendering invoked output while holding engine mutex")
	}
	f.e.Mu.Unlock()
	*f.observed = true
	return f.base.FormatRegclass(oid)
}

func TestCatalogSourcePGPoliciesCapture(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE policies_capture_reader",
		"CREATE TABLE policies_capture_owner(id INT)",
		"CREATE TABLE policies_capture_provider(id INT)",
		"CREATE SEQUENCE policies_capture_sequence START 41",
		"CREATE POLICY p ON policies_capture_owner USING(EXISTS(SELECT 1 FROM policies_capture_provider WHERE policies_capture_provider.id=policies_capture_owner.id)) WITH CHECK(nextval('policies_capture_sequence')>0)",
		"REVOKE USAGE ON SCHEMA public FROM PUBLIC",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	created := e.CatalogRead()
	address := enginetest.CatalogRef(t, e, created, "policies_capture_owner").Address
	payload := stored.PolicyOf(created.MustRelation(address).Security.Policies[0].Using)
	presentation := payload.Presentation()
	root := presentation.Query().Root
	var retained []sqltype.Value
	for _, usable := range []bool{false, true} {
		if usable {
			enginetest.SessionSQL(t, s, "RESET SESSION AUTHORIZATION")
			enginetest.SessionSQL(t, s, "GRANT USAGE ON SCHEMA public TO policies_capture_reader")
		}
		enginetest.SessionSQL(t, s, "SET SESSION AUTHORIZATION policies_capture_reader")
		func() {
			op := enginetest.TransactionOperation(t, s)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := scope.Finish(nil); err != nil {
					t.Error(err)
				}
			}()
			e.Mu.Lock()
			err = e.CaptureCommentSnapshotLocked(scope)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			auth := core.InquiryDeparseAuth(&scope.Inquiry)
			actor, ok := auth.Roles.Lookup("policies_capture_reader")
			if !ok || actor.Ref != auth.State.CurrentUser || auth.Catalog != scope.Inquiry.CommentCatalog {
				t.Fatal("inquiry capture lost actor/catalog pairing")
			}
			view := scope.Inquiry.CommentCatalog
			d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.PoliciesOID})
			ref := view.Owner().Domain.Ref(view.Epoch(), d.Address())
			observed := false
			scope.Output.Regclass = policiesUnlockedClassOutput{t: t, e: e, base: regobj.ClassOutput{View: view, Path: scope.Inquiry.DeparsePath}, observed: &observed}
			e.Mu.Lock()
			source, err := core.EngineOpenRelationSourceLocked(e, scope, ref, d.Owner, plan.RowMark{}, nil, nil)
			if err != nil || source == nil {
				t.Fatal("actual policies source capture", source, err)
			}
			defer source.Close()
			if source.Ref != ref || len(source.Anonymous) != 1 || !observed {
				t.Fatal("capture did not use captured output/owned rows", source, observed)
			}
			provider := "public.policies_capture_provider"
			if usable {
				provider = "policies_capture_provider"
			}
			want := "(EXISTS ( SELECT 1\n   FROM " + provider + "\n  WHERE (policies_capture_provider.id = policies_capture_owner.id)))"
			if got, err := source.Anonymous[0][6].Text(); err != nil || got != want {
				t.Fatal("caller qualification", got, err, want)
			}
			sequence := "public.policies_capture_sequence"
			if usable {
				sequence = "policies_capture_sequence"
			}
			if got, err := source.Anonymous[0][7].Text(); err != nil || got != "(nextval('"+sequence+"'::regclass) > 0)" {
				t.Fatal("captured relation output", got, err)
			}
			if stored.PolicyOf(view.MustRelation(address).Security.Policies[0].Using) != payload || payload.Presentation() != presentation || presentation.Query().Root != root {
				t.Fatal("capture replaced frozen definition/presentation")
			}
			if !usable {
				retained = append([]sqltype.Value(nil), source.Anonymous[0]...)
			}
			// Fail the actual capture frontier, not a canned producer. No row
			// wrapper may be published on mismatched catalog/principal authority.
			core.InquiryDeparseAuthRef(&scope.Inquiry).Roles = authorization.Read{}
			e.Mu.Lock()
			rejected, err := core.EngineOpenCatalogSourceLocked(e, scope, ref, d)
			if rejected != nil || err == nil {
				t.Fatal("failed authority published rows", rejected, err)
			}
		}()
	}
	if got, _ := retained[6].Text(); got != "(EXISTS ( SELECT 1\n   FROM public.policies_capture_provider\n  WHERE (policies_capture_provider.id = policies_capture_owner.id)))" {
		t.Fatal("new authority mutated detached values", got)
	}
}

func TestCatalogSourcePGPoliciesPresentation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE policies_settings(id INT)", "CREATE POLICY p ON policies_settings USING(id>0)", "SET quote_all_identifiers=on"} {
		enginetest.SessionSQL(t, s, q)
	}
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.Inquiry.CommentCatalog
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.PoliciesOID})
	ref := view.Owner().Domain.Ref(view.Epoch(), d.Address())
	e.Mu.Lock()
	source, err := core.EngineOpenRelationSourceLocked(e, scope, ref, d.Owner, plan.RowMark{}, nil, nil)
	if err != nil || source == nil {
		t.Fatal(source, err)
	}
	defer source.Close()
	if got, err := source.Anonymous[0][6].Text(); err != nil || got != "(\"id\" > 0)" || !source.Anonymous[0][7].IsNull() {
		t.Fatal("statement output or absent CHECK", got, err)
	}
	stale := ref
	stale.Epoch++
	e.Mu.Lock()
	rejected, err := core.EngineOpenCatalogSourceLocked(e, scope, stale, d)
	if rejected != nil || err == nil {
		t.Fatal("stale capture published rows", rejected, err)
	}
}
