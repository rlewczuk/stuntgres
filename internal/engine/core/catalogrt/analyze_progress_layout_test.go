package catalogrt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
)

func TestAnalyzeProgressBuiltinLayout(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	relation := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressAnalyzeOID})
	runtime := catalogrt.Runtime{Presentation: stored.Presentation{Deparse: true, Pretty: true, Path: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}}
	before, err := runtime.BuiltinViewDefinition(t.Context(), read, relation, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		width  int64
		prefix string
	}{
		{0, " SELECT s.pid,\n    s.datid,\n    d.datname,\n    s.relid,"},
		{40, " SELECT s.pid, s.datid, d.datname,\n    s.relid,"},
		{-1, " SELECT s.pid, s.datid, d.datname, s.relid,"},
	} {
		got, err := runtime.BuiltinViewDefinition(t.Context(), read, relation, &tc.width)
		if err != nil || !strings.HasPrefix(got, tc.prefix) {
			t.Fatalf("wrap=%d: %q, %v", tc.width, got, err)
		}
		for _, unchanged := range []string{
			"WHEN 4 THEN 'computing extended statistics'::text\n",
			"s.param8::oid AS current_child_table_relid",
			"s.param9::double precision / 1000000::double precision AS delay_time",
			"LEFT JOIN pg_database d ON s.datid = d.oid",
		} {
			if !strings.Contains(got, unchanged) {
				t.Fatalf("wrap=%d split a structural target: %q", tc.width, got)
			}
		}
	}
	after, err := runtime.BuiltinViewDefinition(t.Context(), read, relation, nil)
	if err != nil || before != after || read.MustRelation(relation.Address()) != relation {
		t.Fatal("layout mutated canonical view owner", err)
	}
	// Unannotated builtin forms keep their existing explicit-format inventory;
	// target boundaries are never inferred by parsing or rewriting rendered SQL.
	copyView := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressCopyOID})
	width := int64(40)
	if got, err := runtime.BuiltinViewDefinition(t.Context(), read, copyView, &width); err != nil || got != "" {
		t.Fatal("invented unowned wrapped layout", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := runtime.BuiltinViewDefinition(ctx, read, relation, &width); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
}
