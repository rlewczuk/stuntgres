package core_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestStatisticsMCVSnapshotTreeCapability(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	tree, ok := read.LookupType(sqltype.PgNodeTree)
	if !ok {
		t.Fatal("missing builtin")
	}
	text := "{retained local structural output}"
	value, err := sqltype.StatisticsRetainedTreeValue(tree.Usage(), text)
	if err != nil {
		t.Fatal(err)
	}
	codec := core.StatisticsSnapshotCodec(t.Context(), e, read)
	encoded, err := codec.Encode(tree.Usage(), value)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := codec.Decode(tree.Usage(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	output, err := tree.Output(t.Context(), restored)
	if err != nil || output != text {
		t.Fatal(output, err)
	}
	reencoded, err := codec.Encode(tree.Usage(), restored)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		t.Fatal("retained bytes changed", err)
	}
	var node typedef.DomainReplayNode
	if err := json.Unmarshal(encoded, &node); err != nil {
		t.Fatal(err)
	}
	env := typedef.DomainReplayEnv{Context: t.Context(), Catalog: read}
	if _, _, err := typedef.RestoreDomainReplayNode(&env, tree.Usage(), &node); err == nil {
		t.Fatal("ordinary replay gained statistics capability")
	}
	env.StatisticsTree = sqltype.StatisticsRetainedTreeValue
	if _, provenance, err := typedef.RestoreDomainReplayNode(&env, tree.Usage(), &node); err != nil || provenance != 0 {
		t.Fatal("statistics datum granted row provenance", provenance, err)
	}
	other := engine.New()
	foreign, _ := other.CatalogRead().LookupType(sqltype.PgNodeTree)
	if _, _, err := typedef.RestoreDomainReplayNode(&env, foreign.Usage(), &node); err == nil {
		t.Fatal("foreign tree descriptor adopted")
	}
	integer, _ := read.LookupType(sqltype.Int4)
	if _, _, err := typedef.RestoreDomainReplayNode(&env, integer.Usage(), &node); err == nil {
		t.Fatal("tree capability widened to scalar")
	}
	if _, err := tree.Input(t.Context(), text, -1); err == nil {
		t.Fatal("SQL input widened")
	}
	// Ordinary capture never emits a statistics-only constructor.
	ordinary, err := table.CaptureDomainReplayNode(t.Context(), read, tree.Usage(), value)
	if err == nil && ordinary.Kind == "statistics_tree" {
		t.Fatal("ordinary capture gained statistics authority")
	}
	if _, err := codec.Decode(tree.Usage(), json.RawMessage(`{"kind":"statistics_tree","statistics_tree":"x","scalar":"x"}`)); err == nil {
		t.Fatal("mixed constructor accepted")
	}
	if _, err := codec.Decode(tree.Usage(), json.RawMessage(`{"kind":"statistics_tree","statistics_tree":"x","statistics_tree":"x"}`)); err == nil {
		t.Fatal("duplicate constructor accepted")
	}
	if read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.PgNodeTree)}).SQLType != tree {
		t.Fatal("builtin authority changed")
	}
}
