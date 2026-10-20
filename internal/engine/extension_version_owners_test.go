package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"testing"
)

func extensionVersionOwnerImports(t *testing.T) {
	for _, version := range []string{"1.1", "1.2"} {
		t.Run("VersionImport"+version, func(t *testing.T) {
			e, _, ext := extensionVersionUnitInstalled(t, version)
			metadata, err := extension.OwnerMetadata(e.CatalogRead(), e.Roles(), ext)
			if err != nil {
				t.Fatal(err)
			}
			record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
			want := 9
			if version == "1.2" {
				want = 10
			}
			if err != nil || !found || len(record.Members) != want {
				t.Fatal("version owner manifest", record, err)
			}
			stage := e.restoreStage()
			op, done := roleOperation(t, stage, "postgres")
			defer done()
			if err := extensionUnitExecute(t, stage, op, extensionVersionUnitCreate(version)); err != nil {
				t.Fatal(err)
			}
			before := stage.catalog.state
			for _, kind := range []string{"graph_only", "unknown", "member_owner", "extension_owner", "duplicate", "signature", "namespace"} {
				bad := record
				bad.Members = slices.Clone(record.Members)
				records := []extension.OwnerRecord{bad}
				switch kind {
				case "graph_only":
					records[0].Version = "1.0"
				case "unknown":
					records[0].Version = "missing"
				case "member_owner":
					records[0].Members[0].Owner = "missing_owner"
				case "extension_owner":
					records[0].Owner = "missing_owner"
				case "duplicate":
					records = append(records, bad)
				case "signature":
					records[0].Members[0].Signature = "invalid"
				case "namespace":
					records[0].Schema = "foreign_schema"
				}
				if err := stage.importExtensionOwners(context.Background(), op, records); err == nil {
					t.Fatal("invalid version owners admitted", kind)
				}
				if stage.catalog.state != before {
					t.Fatal("failed version owner import mutated destination", kind)
				}
			}
			if err := stage.importExtensionOwners(context.Background(), op, []extension.OwnerRecord{record}); err != nil {
				t.Fatal(err)
			}
			a := stage.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"})
			if stage.catalog.state.MustExtension(a).Version != version || extension.StockFuzzystrmatch.ValidateExtension(stage.CatalogRead(), stage.catalog.state.MustExtension(a)) != nil {
				t.Fatal("import lost genuine version manifest")
			}
			for _, field := range []string{"name", "schema", "version", "owner", "members"} {
				data, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				fields[field] = nil
				data, err = json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)); err == nil {
					t.Fatal("NULL owner key admitted", field)
				}
			}
		})
	}
}
