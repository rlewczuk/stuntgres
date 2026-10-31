package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestTSConfigurationFoldOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_fold (COPY=pg_catalog.simple)")
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "config_fold"})
	query := "SELECT to_tsvector('public.config_fold'::regconfig,'The Fat Rats') AS normalized"
	ast, d := enginetest.PreparedDescription(t, e, s, query)
	if d.Folds == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
		t.Fatal("Describe evaluated conversion")
	}
	preparedFoldExecute(t, e, s, ast, &d)
	entry := preparedFoldEntry(t, d, sqltype.TSVector, "'fat':2 'rats':3 'the':1")
	before := snapshotFoldDescription(d)
	operand := dictionaryFoldDependency(t, d, address)
	analysis := d.Analysis
	dictionary := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
	dictionaryOperand := dictionaryFoldDependency(t, d, dictionary)
	if dictionaryOperand.Revision != e.CatalogRead().Get(dictionary).Revision() {
		t.Fatal("mapped dictionary operand revision missing")
	}
	for _, dependency := range before.Accepted.Dependencies {
		if dependency.Ref.Address == address || dependency.Ref.Address == dictionary {
			t.Fatal("configuration operand became executable fold dependency")
		}
	}
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.config_fold DROP MAPPING FOR asciiword")
	preparedFoldExecute(t, e, s, ast, &d)
	after := d.Folds.Current()
	current := e.CatalogRead().Get(address)
	refreshed := dictionaryFoldDependency(t, d, address)
	if current.Revision() == operand.Revision || refreshed.Revision != current.Revision() || refreshed.Ref != operand.Ref || d.Analysis == analysis {
		t.Fatal("retention bypassed current analysis")
	}
	if after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, sqltype.TSVector, "'fat':2 'rats':3 'the':1")) {
		t.Fatal("mapping edit replaced accepted immutable vector backing")
	}
	freshAST, fresh := enginetest.PreparedDescription(t, e, s, query)
	preparedFoldExecute(t, e, s, freshAST, &fresh)
	preparedFoldEntry(t, fresh, sqltype.TSVector, "")
	runtimeAST, runtime := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.config_fold'::regconfig,$1::text) AS normalized")
	r, err := dictionaryFoldRuntimeExecute(e, s, runtimeAST, &runtime)
	if err != nil {
		t.Fatal(err)
	}
	values := make([]sqltype.Value, 1)
	present, err := r.Rows.Next(context.Background(), values)
	if err != nil || !present || values[0].IsNull() || values[0].Type().ID() != sqltype.TSVector {
		r.Rows.Close()
		t.Fatal("parameter conversion lost typed vector", present, err, values)
	}
	text, err := values[0].Type().Output(context.Background(), values[0])
	if err != nil || text != "" {
		r.Rows.Close()
		t.Fatal("parameter conversion retained obsolete mapping", text, err)
	}
	present, err = r.Rows.Next(context.Background(), values)
	r.Rows.Close()
	if present || err != nil {
		t.Fatal("parameter conversion trailing rows", present, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	local := cloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	cancel()
	err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	_ = scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || local.Folds.Current().Generation != before.Accepted.Generation || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("rejected private vector fold changed accepted owner", err)
	}
	preparedFoldEntry(t, before, sqltype.TSVector, "'fat':2 'rats':3 'the':1")
}

func TestTSConfigurationPreparedRenameMove(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE SCHEMA config_capture_destination")
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_capture (COPY=pg_catalog.simple)")
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "config_capture"})
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.config_capture'::regconfig,$1::text) AS normalized")
	r, err := dictionaryFoldRuntimeExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	if d.Regtypes == nil || len(d.Regtypes.Constants) != 1 {
		t.Fatal("missing original configuration capture")
	}
	captures := regoperatorPreparedSnapshot(d.Regtypes.Constants)
	original := dictionaryFoldDependency(t, d, address)
	namespace := e.CatalogRead().State().NamespaceGeneration()
	accepted := d.Folds.Current()
	if len(accepted.Entries) != 0 || accepted.Generation != 0 {
		t.Fatal("runtime-text conversion published a vector fold")
	}
	for _, mutation := range []struct {
		alter string
		reuse string
	}{
		{"ALTER TEXT SEARCH CONFIGURATION public.config_capture RENAME TO config_capture_renamed", "CREATE TEXT SEARCH CONFIGURATION public.config_capture (PARSER=pg_catalog.default)"},
		{"ALTER TEXT SEARCH CONFIGURATION public.config_capture_renamed SET SCHEMA config_capture_destination", "CREATE TEXT SEARCH CONFIGURATION public.config_capture_renamed (PARSER=pg_catalog.default)"},
	} {
		enginetest.SessionSQL(t, s, mutation.alter)
		enginetest.SessionSQL(t, s, mutation.reuse)
		if e.CatalogRead().State().NamespaceGeneration() != namespace {
			t.Fatal("configuration rename/move/reuse advanced namespace generation")
		}
		r, err = dictionaryFoldRuntimeExecute(e, s, ast, &d)
		if err != nil {
			t.Fatal("renamed/moved capture failed", err)
		}
		r.Rows.Close()
		if d.Regtypes == nil || !reflect.DeepEqual(d.Regtypes.Constants, captures) {
			t.Fatal("object presentation change replaced original typed value/provenance")
		}
		current := e.CatalogRead().Get(address)
		refreshed := dictionaryFoldDependency(t, d, address)
		if current == nil || current.Revision() == original.Revision || refreshed.Ref != original.Ref || refreshed.Revision != current.Revision() {
			t.Fatal("preserved datum bypassed current original-object analysis")
		}
		if d.Folds.Current().Generation != accepted.Generation || len(d.Folds.Current().Entries) != 0 {
			t.Fatal("object presentation change published runtime conversion folds")
		}
	}
	_, fresh := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.config_capture'::regconfig,$1::text) AS normalized")
	replacement := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "config_capture"})
	oid, err := fresh.Regtypes.Constants[0].Value.RegTSOID()
	if err != nil || replacement == address || ids.OID(oid) != replacement.ObjectID {
		t.Fatal("fresh capture did not own the independently replaced spelling", oid, err)
	}
}

func TestTSConfigurationPreparedPublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_publication (COPY=pg_catalog.simple)")
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.config_publication'::regconfig,$1::text) AS normalized")
	cell := d.Regtypes
	captures := regoperatorPreparedSnapshot(cell.Constants)
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.config_publication RENAME TO config_publication_old")
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_publication (PARSER=pg_catalog.default)")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	observation, paths := e.RegtypeObservationLocked(auth)
	projection, err := cell.ForReanalysis(observation, paths)
	e.Mu.Unlock()
	op.Done()
	if err != nil || projection == nil {
		t.Fatal("missing private candidate", projection, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	analysis, folds := d.Analysis, d.Folds.Current()
	err = d.AcceptAnalysis(ctx, sequence.Analysis{}, projection, nil)
	if !errors.Is(err, context.Canceled) || d.Regtypes != cell || d.Analysis != analysis || !reflect.DeepEqual(cell.Constants, captures) || d.Folds.Current().Generation != folds.Generation {
		t.Fatal("rejected candidate partially published identifier analysis", err)
	}
	// DISCARD keeps its existing full reanalysis fence even when the ordinary
	// configuration resolution environment otherwise remains equal.
	enginetest.SessionSQL(t, s, "DISCARD PLANS")
	r, err := dictionaryFoldRuntimeExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	oldOID, _ := captures[0].Value.RegTSOID()
	newOID, err := d.Regtypes.Constants[0].Value.RegTSOID()
	if err != nil || oldOID == newOID || reflect.DeepEqual(cell.Constants, captures) {
		t.Fatal("DISCARD reused a retired configuration input", newOID, err)
	}
}

func TestTSConfigurationPreparedOwnerFences(t *testing.T) {
	for _, fence := range []string{"owner", "catalog_owner", "catalog_epoch", "session", "journal", "domain", "epoch", "missing_analysis"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_fence (COPY=pg_catalog.simple)")
			ast, d := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.config_fence'::regconfig,$1::text) AS normalized")
			original, err := d.Regtypes.Constants[0].Value.RegTSOID()
			if err != nil {
				t.Fatal(err)
			}
			enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.config_fence RENAME TO config_fence_old")
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_fence (PARSER=pg_catalog.default)")
			// These are rejected provenance candidates on the real description,
			// not authority to import an artifact from another owner.
			switch fence {
			case "owner":
				d.PlanOwner = ids.SessionToken{}
			case "catalog_owner":
				d.CatalogOwner = nil
			case "catalog_epoch":
				d.CatalogEpoch++
			case "session":
				d.Analysis.Session = ids.SessionToken{}
			case "journal":
				d.Analysis.Private = new(session.Journal)
			case "domain":
				d.Analysis.Domain = new(ids.Domain)
			case "epoch":
				d.Analysis.Epoch++
			case "missing_analysis":
				d.Analysis = nil
			}
			r, err := dictionaryFoldRuntimeExecute(e, s, ast, &d)
			if err != nil {
				t.Fatal(err)
			}
			r.Rows.Close()
			current, err := d.Regtypes.Constants[0].Value.RegTSOID()
			if err != nil || current == original {
				t.Fatal("enclosing owner fence imported old configuration input", current, err)
			}
		})
	}
}
