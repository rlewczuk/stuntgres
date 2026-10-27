package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestTypeDescribeCapturedTypePath(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TEMP TABLE captured_type(v integer)")
	sessionSQL(t, s, "SET search_path=pg_temp,public,pg_catalog")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var types, conversions []ids.ObjectAddress
	if err == nil {
		types, err = core.LookupPath(auth, analyze.TypeLookup)
	}
	if err == nil {
		conversions, err = core.LookupPath(auth, analyze.ConversionLookup)
	}
	if err == nil {
		err = core.EngineCaptureVisibilitySnapshotLocked(e, scope)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	relations := core.InquiryVisibilityRelationPath(&scope.Inquiry)
	others := core.InquiryVisibilityOtherPath(&scope.Inquiry)
	if !reflect.DeepEqual(relations, types) || !reflect.DeepEqual(others, conversions) {
		t.Fatal("captured paths differ from authorized type/conversion lookup", relations, types, others, conversions)
	}
	if len(relations) == 0 || relations[0] != core.TemporaryNamespace(auth) {
		t.Fatal("type path lost explicitly leading TEMP", relations)
	}
	for i := range relations {
		for j := range others {
			if &relations[i] == &others[j] {
				t.Fatal("conversion filtering shares mutable type-path backing")
			}
		}
	}
	catalog := core.InquiryVisibilityCatalog(&scope.Inquiry)
	e.Mu.Lock()
	err = core.EngineCaptureVisibilitySnapshotLocked(e, scope)
	e.Mu.Unlock()
	if err != nil || core.InquiryVisibilityCatalog(&scope.Inquiry) != catalog || &core.InquiryVisibilityRelationPath(&scope.Inquiry)[0] != &relations[0] {
		t.Fatal("repeated demand replaced the captured frontier", err)
	}
}

func TestTypeDescribeCopySnapshotDemand(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, predicate string
		demand                       bool
	}{
		{"catalog_free", "v integer", "pg_typeof(v)::oid=23", false},
		{"predicate_only", "v integer", "format_type(pg_typeof(v),NULL)='integer'", true},
		{"catalog_free_recipe", "v integer DEFAULT 1", "format_type(pg_typeof(v),NULL)='integer'", true},
		{"recipe_only", "v integer CHECK(format_type(pg_typeof(v),NULL)='integer')", "v>0", true},
		{"combined", "v integer CHECK(format_type(pg_typeof(v),NULL)='integer')", "format_type(pg_typeof(v),NULL)='integer'", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE copy_capture("+tc.declaration+")")
			view := e.CatalogRead()
			relation := view.MustRelation(enginetest.CatalogRef(t, e, view, "copy_capture").Address)
			recipes := relation.ColumnMeta
			checks := relation.Constraints.Check
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			veto := errors.New("stop after COPY preflight")
			started := false
			syntax := enginetest.Statement(t, "COPY copy_capture(v) FROM STDIN WHERE "+tc.predicate).(*parser.Copy)
			_, err = core.ScopeCopyProtocol(scope, syntax, strings.NewReader(""), nil, core.CopyProtocolHooks{Start: func(core.CopyProtocolMetadata) error {
				started = true
				if got := core.InquiryVisibilityCatalog(&scope.Inquiry).State() != nil; got != tc.demand {
					t.Fatalf("capture after predicate and recipe preflight = %t, want %t", got, tc.demand)
				}
				return veto
			}})
			if !started || !errors.Is(err, veto) {
				t.Fatal("did not reach preflight ownership boundary", err)
			}
			if !reflect.DeepEqual(relation.ColumnMeta, recipes) || !reflect.DeepEqual(relation.Constraints.Check, checks) {
				t.Fatal("COPY demand capture changed retained catalog recipes")
			}
		})
	}
	t.Run("before_failed_preflight", func(t *testing.T) {
		_, _, op, scope := copyUnitStart(t, false)
		defer op.Done()
		defer scope.Finish(context.Canceled)
		syntax := enginetest.Statement(t, "COPY t(v) FROM STDIN WHERE format_type(pg_typeof(v),1/0)='integer'").(*parser.Copy)
		started := false
		_, err := core.ScopeCopyProtocol(scope, syntax, strings.NewReader(""), nil, core.CopyProtocolHooks{Start: func(core.CopyProtocolMetadata) error {
			started = true
			return nil
		}})
		if err == nil || started {
			t.Fatal("failed predicate preflight crossed COPY start boundary", err)
		}
		if core.InquiryVisibilityCatalog(&scope.Inquiry).State() == nil {
			t.Fatal("predicate-only demand was not captured before failing preflight")
		}
	})
}
