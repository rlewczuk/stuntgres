package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

// Uses only current private engine seams. The real root, its maintained entries
// and a captured hash candidate must exist before asserting the corrected class
// identity. No proposed catalog/metadata API is needed at this checkpoint.
func TestPGOpclassJSONBHashIdentity(t *testing.T) {
	e := New()
	for _, q := range []string{
		"CREATE TABLE opclass_jsonb_private(j JSONB)",
		`INSERT INTO opclass_jsonb_private VALUES('{"a":1}'),('{"a":1.0}'),('{"b":2}'),(NULL)`,
		"CREATE INDEX opclass_jsonb_private_h ON opclass_jsonb_private USING hash(j)",
	} {
		run(t, e, q)
	}
	index, old := hashRoot(t, e, "opclass_jsonb_private_h")
	if got := hashKeys(t, old); !reflect.DeepEqual(got, []string{`{"a": 1.0}`, `{"a": 1}`, `{"b": 2}`}) {
		t.Fatal("actual JSONB hash entries", got)
	}
	query, source, release := hashImplicitQuery(t, e, `SELECT j FROM opclass_jsonb_private WHERE j='{"a":1}'::jsonb`)
	stage := hashFilterStage(t, query, source, nil)
	if stage.HashCandidates == nil || stage.HashCandidates.Root != old || len(stage.HashCandidates.Occurrences) != 2 {
		stage.Close()
		release()
		t.Fatal("no actual JSONB hash probe")
	}
	if err := stage.Drain(context.Background()); err != nil || stage.Count() != 2 {
		stage.Close()
		release()
		t.Fatal("JSONB hash matches", stage.Count(), err)
	}
	stage.Close()
	release()
	run(t, e, `UPDATE opclass_jsonb_private SET j='{"c":3}' WHERE j='{"b":2}'::jsonb`)
	_, updated := hashRoot(t, e, "opclass_jsonb_private_h")
	if updated == old {
		t.Fatal("update did not publish real hash root")
	}
	metadata, _ := indexMaintenanceMetadata(t, e, e.CatalogRead(), "opclass_jsonb_private_h")
	if got := index.SQLIndex.Keys[0].Class.OID(); got != 10089 {
		t.Errorf("JSONB hash class OID got %d want released hash identity 10089 (not btree 10088)", got)
	}
	if !reflect.DeepEqual(metadata.IndClass, []ids.OID{10089}) {
		t.Errorf("actual index metadata class got %v want [10089]", metadata.IndClass)
	}
}
