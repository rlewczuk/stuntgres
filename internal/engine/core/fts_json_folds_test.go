package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
	"testing"
)

func TestFTSJSONPreparedOwnership(t *testing.T) {
	for _, tc := range []struct{ constant, runtime string }{
		{`to_tsvector('public.json_fold'::regconfig,'"The"'::json)`, `to_tsvector('public.json_fold'::regconfig,$1::json)`},
		{`jsonb_to_tsvector('public.json_fold'::regconfig,'"The"'::jsonb,'"string"'::jsonb)`, `jsonb_to_tsvector('public.json_fold'::regconfig,$1::jsonb,'"string"'::jsonb)`},
	} {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.json_fold_dict (TEMPLATE=simple,STOPWORDS=english)")
		enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.json_fold (COPY=pg_catalog.simple)")
		enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.json_fold ALTER MAPPING FOR asciiword WITH public.json_fold_dict")
		address := thesaurusUnitAddress(e, ids.TSConfigurationClassID, "json_fold")
		dictionary := thesaurusUnitAddress(e, ids.TSDictionaryClassID, "json_fold_dict")
		configuration := e.CatalogRead().MustTSConfiguration(address)
		query := "SELECT " + tc.constant + " AS normalized"
		ast, d := sequencePreparedDescription(t, e, s, query)
		if len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
			t.Fatal("Describe performed conversion")
		}
		preparedFoldExecute(t, e, s, ast, &d)
		entry := preparedFoldEntry(t, d, sqltype.TSVector, "")
		before := snapshotFoldDescription(d)
		analysis := d.Analysis
		for _, a := range []ids.ObjectAddress{address, configuration.Parser, dictionary} {
			if dep := dictionaryFoldDependency(t, d, a); dep.Revision != e.CatalogRead().Get(a).Revision() {
				t.Fatal(dep)
			}
			for _, dep := range before.Accepted.Dependencies {
				if dep.Ref.Address == a {
					t.Fatal("operand became executable fold dependency", dep)
				}
			}
		}
		enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.json_fold_dict (STOPWORDS)")
		preparedFoldExecute(t, e, s, ast, &d)
		after := d.Folds.Current()
		if d.Analysis == analysis || dictionaryFoldDependency(t, d, dictionary).Revision != e.CatalogRead().Get(dictionary).Revision() {
			t.Fatal("missing operand reanalysis")
		}
		if after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, sqltype.TSVector, "")) {
			t.Fatal("accepted vector replaced")
		}
		freshAST, fresh := sequencePreparedDescription(t, e, s, query)
		preparedFoldExecute(t, e, s, freshAST, &fresh)
		preparedFoldEntry(t, fresh, sqltype.TSVector, "'the':1")
		runtimeAST, runtime := sequencePreparedDescription(t, e, s, "SELECT "+tc.runtime+" AS normalized")
		convertOwnedExecute(t, e, s, runtimeAST, &runtime, `"The"`, "'the':1")
		enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.json_fold DROP MAPPING FOR asciiword")
		preparedFoldExecute(t, e, s, ast, &d)
		if current := d.Folds.Current(); current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("mapping edit replaced accepted vector")
		}
		convertOwnedExecute(t, e, s, runtimeAST, &runtime, `"The"`, "")
		if len(runtime.Folds.Current().Entries) != 0 {
			t.Fatal("parameter conversion folded")
		}
		convertRejectPublication(t, e, s, ast, d, before.Accepted)
	}
	for _, call := range []string{`to_tsvector('"The Rats"'::json)`, `jsonb_to_tsvector('"The Rats"'::jsonb,'"string"'::jsonb)`} {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		ast, d := sequencePreparedDescription(t, e, s, "SELECT "+call)
		for _, tc := range []struct{ setting, want string }{{"simple", "'rats':2 'the':1"}, {"english", "'rat':2"}, {"simple", "'rats':2 'the':1"}} {
			enginetest.SessionSQL(t, s, "SET default_text_search_config='"+tc.setting+"'")
			convertOwnedExecute(t, e, s, ast, &d, "", tc.want)
			for _, entry := range d.Folds.Current().Entries {
				if entry.Result.Type.ID() == sqltype.TSVector {
					t.Fatal("stable conversion folded")
				}
			}
			if d.Regtypes != nil && len(d.Regtypes.Constants) != 0 {
				t.Fatal("synthetic default constant")
			}
		}
	}
}

func TestFTSJSONCancellation(t *testing.T) {
	e, s := thesaurusCoreSetup(t)
	savedAST, saved := sequencePreparedDescription(t, e, s, `SELECT to_tsvector('simple'::regconfig,'["saved"]'::json)`)
	preparedFoldExecute(t, e, s, savedAST, &saved)
	savedEntry := preparedFoldEntry(t, saved, sqltype.TSVector, "'saved':1")
	accepted := snapshotFoldDescription(saved).Accepted
	if thesaurusReceipts(s).Len() != 0 {
		t.Fatal("unrelated fold initialized thesaurus")
	}
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	view := e.CatalogRead()
	jsonType, _ := view.LookupType(sqltype.JSON)
	configType, _ := view.LookupType(sqltype.Regconfig)
	vectorType, _ := view.LookupType(sqltype.TSVector)
	address := thesaurusUnitAddress(e, ids.TSConfigurationClassID, "thes_unit_cfg")
	config, err := sqltype.RegTSValue(configType, uint32(address.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonType.Input(t.Context(), `["one","two"]`, -1)
	if err != nil {
		t.Fatal(err)
	}
	// A lexical work admission after traversal has reached the first item arms
	// cancellation/refusal for subsequent real work, not a sleep or a large race.
	for _, cancelMode := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		refusal := errors.New("JSON conversion refused")
		admissions := 0
		ctx = workadmission.Activate(workadmission.With(ctx, func(_ context.Context, class workadmission.Class) error {
			if !e.Mu.TryLock() {
				t.Fatal("conversion admission holds engine lock")
			}
			e.Mu.Unlock()
			if class == workadmission.WorkTSVectorConvert {
				admissions++
				// [ then string then comma lookahead then lexical first item; the
				// next traversal token is after its normalization has completed.
				if admissions == 5 {
					if cancelMode {
						cancel()
						return context.Canceled
					}
					return refusal
				}
			}
			return nil
		}))
		value, err := core.HostOf(scope).ConvertJSONTextSearch(ctx, true, config, document, sqltype.Value{}, false, vectorType)
		cancel()
		want := refusal
		if cancelMode {
			want = context.Canceled
		}
		if err != want || value != (sqltype.Value{}) || admissions < 5 {
			t.Fatal("partial conversion escaped", value, err, admissions)
		}
		if thesaurusReceipts(s).Len() != 0 {
			t.Fatal("rejected conversion published partial receipts")
		}
		current := saved.Folds.Current()
		if current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !samePublishedFold(savedEntry, preparedFoldEntry(t, saved, sqltype.TSVector, "'saved':1")) {
			t.Fatal("failed conversion mutated accepted fold")
		}
	}
	value, err := core.HostOf(scope).ConvertJSONTextSearch(context.Background(), true, config, document, sqltype.Value{}, false, vectorType)
	// The shipped thesaurus maps each item independently (one:*1, two:*2);
	// its "one two":*12 phrase must not cross the JSON item boundary.
	thesaurusUnitVector(t, value, err, "'1':1 '2':3")
	if thesaurusReceipts(s).Len() == 0 {
		t.Fatal("successful conversion did not accept actual dictionary acquisition")
	}
}
