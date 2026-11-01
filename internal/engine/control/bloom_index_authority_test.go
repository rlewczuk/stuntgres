package control

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
)

func TestBloomDumpIndexAuthority(t *testing.T) {
	state := catalog.NewState(1)
	descriptor := &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 94001},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
		ObjectName:      "bloom_index",
		SQLIndex:        &catalog.IndexDefinition{},
	}
	state.SetDescriptor(descriptor.Address(), descriptor)
	view := catalog.NewRead(&catalog.Owner{}, state)
	script := &dumpScript{Capture: &Capture{
		CatalogView: view, OriginalCatalog: view,
		ConstraintReplay: &partition.ConstraintReplay{Occupied: make(map[ids.ObjectAddress]map[string]bool)},
	}}
	const definition = "CREATE INDEX bloom_index ON public.t USING bloom(i);\n"
	index := OwnedStatement{Owner: "Owned 'role", SQL: definition}
	const restored = "SET SESSION AUTHORIZATION 'Owned ''role';\n"
	want := "SET SESSION AUTHORIZATION DEFAULT;\n" + definition + restored
	for _, priorOwner := range []string{"prior_owner", index.Owner} {
		var out bytes.Buffer
		w := dumpWriter{ctx: context.Background(), dst: &out, currentOwner: priorOwner}
		if err := emitDumpIndex(script, &w, descriptor.Address().ObjectID, index); err != nil {
			t.Fatal(err)
		}
		if out.String() != want || w.currentOwner != index.Owner || !script.ConstraintReplay.Occupied[descriptor.Namespace()][descriptor.Name()] {
			t.Fatalf("index definition authority = %q owner=%q, want %q", out.String(), w.currentOwner, want)
		}
		const dependent = "CREATE VIEW public.v AS SELECT 1;\n"
		if err := emitDumpStatement(script, &w, 94002, dump.Statement{Owner: index.Owner, SQL: dependent}); err != nil {
			t.Fatal(err)
		}
		if out.String() != want+dependent || w.currentOwner != index.Owner {
			t.Fatal("index creator leaked authenticated authority to dependent", out.String(), w.currentOwner)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	w := dumpWriter{ctx: ctx, dst: &out, currentOwner: "prior_owner"}
	if err := emitDumpIndex(script, &w, descriptor.Address().ObjectID, index); !errors.Is(err, context.Canceled) || out.Len() != 0 || w.currentOwner != "prior_owner" {
		t.Fatal("canceled index declaration changed output/authority", err, out.String(), w.currentOwner)
	}
}

func TestBloomDumpIndexOptionsAuthority(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	descriptor := &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 94001},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
		ObjectName:      "bloom_index",
		Owner:           source.Bootstrap,
		SQLIndex:        &catalog.IndexDefinition{},
	}
	descriptor.StorageOptions.Base = []catalog.RelationOption{{Name: "length", Text: "7", Kind: catalog.RelationOptionInteger, Integer: 7}}
	source.State.SetDescriptor(descriptor.Address(), descriptor)
	script := &dumpScript{Capture: &Capture{OriginalCatalog: source.CatalogView, DomainRoles: source.DomainRoles}}
	var out bytes.Buffer
	w := dumpWriter{ctx: context.Background(), dst: &out, currentOwner: "postgres"}
	if err := emitDumpIndexOptions(context.Background(), script, &w); err != nil {
		t.Fatal(err)
	}
	const want = "SET SESSION AUTHORIZATION DEFAULT;\nALTER INDEX \"public\".\"bloom_index\" SET (length='7');\nSET SESSION AUTHORIZATION 'postgres';\n"
	if out.String() != want || w.currentOwner != "postgres" || descriptor.StorageOptions.Base[0].Integer != 7 {
		t.Fatal("index options lost authenticated creation/captured owner or lexical intent", out.String(), w.currentOwner)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out.Reset()
	if err := emitDumpIndexOptions(ctx, script, &w); !errors.Is(err, context.Canceled) || out.Len() != 0 || w.currentOwner != "postgres" {
		t.Fatal("canceled index options changed output/authority", err, out.String(), w.currentOwner)
	}
}
