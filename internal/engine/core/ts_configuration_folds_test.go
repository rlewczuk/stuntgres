package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTSConfigurationPreparedIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_identity (COPY=pg_catalog.simple)")
	key := ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "config_identity"}
	address := e.CatalogRead().NameAddress(key)
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.config_identity'::regconfig,$1::text) AS normalized")
	r, err := dictionaryFoldRuntimeExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	r.Rows.Close()
	if d.Regtypes == nil || len(d.Regtypes.Constants) != 1 {
		t.Fatal("runtime-parameter statement has no owned identifier capture")
	}
	cell := d.Regtypes
	captures := regoperatorPreparedSnapshot(cell.Constants)
	oid, err := captures[0].Value.RegTSOID()
	if err != nil || ids.OID(oid) != address.ObjectID || captures[0].Operator != sqltype.Regconfig {
		t.Fatal("literal capture lost its typed configuration identity", oid, err, captures)
	}
	operand := dictionaryFoldDependency(t, d, address)
	if operand.Revision != e.CatalogRead().Get(address).Revision() || len(captures[0].Operands) == 0 || captures[0].Operands[0].Ref != operand.Ref {
		t.Fatal("literal capture and analysis do not share original descriptor authority")
	}
	accepted := d.Folds.Current()
	if len(accepted.Entries) != 0 || accepted.Generation != 0 {
		t.Fatal("runtime-parameter identity was supplied by a folded conversion")
	}
	enginetest.SessionSQL(t, s, "DROP TEXT SEARCH CONFIGURATION public.config_identity")
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_identity (PARSER=pg_catalog.default)")
	replacement := e.CatalogRead().NameAddress(key)
	if replacement == address || e.CatalogRead().Get(address) != nil {
		t.Fatal("DROP/recreate did not replace the actual configuration identity")
	}
	r, err = dictionaryFoldRuntimeExecute(e, s, ast, &d)
	if r.Rows != nil {
		r.Rows.Close()
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != fmt.Sprintf("cache lookup failed for text search configuration %d", oid) {
		t.Error("held runtime statement rebound its frozen configuration identifier", err)
	}
	if d.Regtypes != cell || !reflect.DeepEqual(cell.Constants, captures) {
		t.Error("stale-identity execution replaced frozen literal capture authority")
	}
	if d.Folds.Current().Generation != accepted.Generation || len(d.Folds.Current().Entries) != 0 {
		t.Error("runtime identity rejection changed unrelated fold ownership")
	}
	for _, dependency := range d.Analysis.Dependencies {
		if dependency.Ref.Address == address {
			t.Fatal("absent original survived as a fabricated current dependency")
		}
	}
	analysis := d.Analysis
	r, err = dictionaryFoldRuntimeExecute(e, s, ast, &d)
	if r.Rows != nil {
		r.Rows.Close()
	}
	if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != fmt.Sprintf("cache lookup failed for text search configuration %d", oid) {
		t.Fatal("repeated stale execution retargeted original", err)
	}
	if d.Analysis != analysis || !regoperatorPreparedCellValid(t, e, s, cell) || !reflect.DeepEqual(cell.Constants, captures) {
		t.Fatal("stable absent observation reanalyzed or replaced original capture")
	}
	_, fresh := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.config_identity'::regconfig,$1::text) AS normalized")
	freshOID, err := fresh.Regtypes.Constants[0].Value.RegTSOID()
	if err != nil || ids.OID(freshOID) != replacement.ObjectID {
		t.Fatal("fresh analysis did not capture the new descriptor", freshOID, err)
	}
}

func TestTSConfigurationPreparedObservations(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_observation (COPY=pg_catalog.simple)")
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT to_tsvector('public.config_observation'::regconfig,$1::text) AS normalized")
	cell := d.Regtypes
	captures := regoperatorPreparedSnapshot(cell.Constants)
	address := captures[0].Operands[0].Ref.Address
	namespace := e.CatalogRead().State().NamespaceGeneration()
	execute := func() {
		t.Helper()
		r, err := dictionaryFoldRuntimeExecute(e, s, ast, &d)
		if err != nil {
			t.Fatal(err)
		}
		r.Rows.Close()
	}
	execute()
	for _, sql := range []string{
		"ALTER TEXT SEARCH CONFIGURATION public.config_observation DROP MAPPING FOR asciiword",
		"ALTER TEXT SEARCH CONFIGURATION public.config_observation ADD MAPPING FOR asciiword WITH pg_catalog.simple",
	} {
		enginetest.SessionSQL(t, s, sql)
		if e.CatalogRead().State().NamespaceGeneration() != namespace {
			t.Fatal("configuration mapping advanced namespace generation")
		}
		execute()
		if d.Regtypes != cell || !reflect.DeepEqual(cell.Constants, captures) {
			t.Fatal("current observation replaced original capture")
		}
		if dependency := dictionaryFoldDependency(t, d, address); dependency.Revision != e.CatalogRead().Get(address).Revision() {
			t.Fatal("current analysis did not refresh original OID")
		}
		analysis := d.Analysis
		execute()
		if d.Analysis != analysis || !regoperatorPreparedCellValid(t, e, s, cell) {
			t.Fatal("stable current observation unnecessarily reanalyzed")
		}
	}
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.config_observation RENAME TO config_observation_old")
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.config_observation (PARSER=pg_catalog.default)")
	if e.CatalogRead().State().NamespaceGeneration() != namespace {
		t.Fatal("configuration name mutation advanced namespace generation")
	}
	execute()
	if !reflect.DeepEqual(cell.Constants, captures) {
		t.Fatal("same-environment spelling reuse retargeted original")
	}
	enginetest.SessionSQL(t, s, "CREATE SCHEMA config_observation_invalidation")
	if e.CatalogRead().State().NamespaceGeneration() == namespace {
		t.Fatal("actual namespace publication did not advance generation")
	}
	execute()
	oid, err := d.Regtypes.Constants[0].Value.RegTSOID()
	if err != nil || ids.OID(oid) == address.ObjectID || reflect.DeepEqual(d.Regtypes.Constants, captures) {
		t.Fatal("namespace mutation did not recapture normal source identity", oid, err)
	}
	if d.Folds.Current().Generation != 0 || len(d.Folds.Current().Entries) != 0 {
		t.Fatal("identifier observations published runtime vector folds")
	}
	for _, mutation := range []string{
		"GRANT USAGE ON SCHEMA config_observation_invalidation TO PUBLIC",
		"ALTER SCHEMA config_observation_invalidation RENAME TO config_observation_invalidated",
		"DROP SCHEMA config_observation_invalidated",
	} {
		before := e.CatalogRead().State().NamespaceGeneration()
		enginetest.SessionSQL(t, s, mutation)
		if e.CatalogRead().State().NamespaceGeneration() == before {
			t.Fatal("actual namespace mutation failed to advance generation", mutation)
		}
		execute()
	}
}

func TestTSConfigurationPreparedNamespacePublication(t *testing.T) {
	e := engine.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	before := e.CatalogRead()
	namespace, ok := before.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if !ok {
		t.Fatal("missing real namespace")
	}
	candidate := *namespace
	e.Mu.Lock()
	candidate.ObjectRevision, err = e.Catalog.RevisionLocked()
	stage := e.BeginCatalogStage(before)
	if err == nil {
		err = stage.Replace(&candidate)
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: before.Epoch()})
	if !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() || e.CatalogRead().State().NamespaceGeneration() != before.State().NamespaceGeneration() {
		t.Fatal("canceled namespace publication changed capture environment", err)
	}
}
