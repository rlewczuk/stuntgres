package core_test

import (
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestAvailableExtensionsMetadataValues(t *testing.T) {
	e := engine.New()
	d, _ := availableUnitSource(e, 12085)
	empty, schema := "", "fixed_control_schema"
	metadata := catalog.ExtensionControl{Version: "1.1", Trusted: true, Superuser: true, Relocatable: true, Comment: &empty, Schema: &schema, Requires: []string{"first", "quote\"name", "comma,name"}}
	// Metadata-only projection input, not a new registry entry or provider.
	row, err := pgcatalog.ProjectAvailableExtension("unaccent", metadata, &catalog.Extension{Version: "1.1", TargetNamespace: ids.ObjectAddress{ObjectID: 999}}, d)
	if err != nil {
		t.Fatal(err)
	}
	gotSchema, _ := row[6].Text()
	gotComment, _ := row[8].Text()
	deps, err := row[7].NamesOutput()
	if err != nil || deps != `{first,"quote\"name","comma,name"}` || gotSchema != schema || row[8].IsNull() || gotComment != "" {
		t.Fatal("optional control values", row, deps, err)
	}
	for _, i := range []int{2, 3, 4, 5} {
		if value, err := row[i].Boolean(); err != nil || !value {
			t.Fatal("independent flags", i, value, err)
		}
	}
	metadata.Comment, metadata.Schema, metadata.Requires = nil, nil, nil
	row, err = pgcatalog.ProjectAvailableExtension("unaccent", metadata, nil, d)
	if err != nil || !row[6].IsNull() || !row[7].IsNull() || !row[8].IsNull() || row[7].Type() != d.Columns[7].Usage.Type {
		t.Fatal("typed absence", row, err)
	}
	metadata.Requires = []string{strings.Repeat("x", 64)}
	if row, err := pgcatalog.ProjectAvailableExtension("unaccent", metadata, nil, d); err == nil || row != nil {
		t.Fatal("unchecked NAME dependency", row, err)
	}
	// The registry holds the installer of the stock implementation; its control
	// metadata is read as a detached copy, so readers cannot mutate registered
	// storage.
	registry := &core.Engine{}
	if err := core.EngineRegisterExtension(registry, "unaccent", extension.StockUnaccent); err != nil {
		t.Fatal(err)
	}
	read := registry.Catalog.Extensions["unaccent"].Control()
	*read.Comment = "mutated"
	if stored := registry.Catalog.Extensions["unaccent"].Control(); registry.Catalog.Extensions["unaccent"] != extension.StockUnaccent || *stored.Comment != "text search dictionary that removes accents" {
		t.Fatal("registry storage shared", stored)
	}
	empty, schema = "", "fixed_control_schema"
	original := catalog.ExtensionControl{Version: "1.1", Comment: &empty, Schema: &schema, Requires: []string{"original"}}
	detached := original.Detached()
	empty, schema, original.Requires[0] = "mutated", "mutated", "mutated"
	if *detached.Comment != "" || *detached.Schema != "fixed_control_schema" || detached.Requires[0] != "original" {
		t.Fatal("caller storage retained", detached)
	}
}

func availableUnitSource(e *core.Engine, oid ids.OID) (*catalog.Relation, ids.ObjectRef) {
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	return e.Catalog.State.MustRelation(a), e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), a)
}
