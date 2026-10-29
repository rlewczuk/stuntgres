package core_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestExtensionOwnershipControls(t *testing.T) {
	t.Run("detached_cohort_remap_and_global_delta", func(t *testing.T) {
		source, ext := ownershipUnitInstall(t, "fuzzystrmatch")
		op, done := enginetest.RoleOperation(t, source, "postgres")
		defer done()
		base := source.CatalogRead()
		p := base.MustProcedure(base.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "levenshtein", Signature: "25,25"}))
		symbol, err := extension.ProcedureSymbol(base, p)
		if err != nil {
			t.Fatal(err)
		}
		// The SQL is setup only. Assertions below inspect the actual typed owner,
		// source immutability and independent imported definition generation.
		identity := p.Name() + "(text,text)"
		for _, q := range []string{`ALTER EXTENSION fuzzystrmatch DROP FUNCTION ` + identity, `ALTER FUNCTION ` + identity + ` RENAME TO eo_detached`, `DROP EXTENSION fuzzystrmatch`, `ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql`} {
			enginetest.RoleExecute(t, source, op, q)
		}
		snapshot := source.CatalogRead()
		detached := snapshot.MustProcedure(p.Address())
		target := engine.New()
		if _, err := target.ReplaceCatalog(context.Background(), source, snapshot); err != nil {
			t.Fatal(err)
		}
		view := target.CatalogRead()
		a := view.NameAddress(catalog.DescriptorNameKey(detached))
		imported := view.MustProcedure(a)
		if imported == detached || imported.Implementation != detached.Implementation || imported.Signature != detached.Signature || catalog.ExtensionMembership(imported) != (ids.ObjectAddress{}) {
			t.Fatal("import reused foreign descriptor or lost origin/membership")
		}
		if got, err := extension.ProcedureSymbol(view, imported); err != nil || got != symbol {
			t.Fatal("logical cohort remap lost declaration", got, err)
		}
		if view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}) != (ids.ObjectAddress{}) {
			t.Fatal("remap resurrected former extension")
		}
		language := view.Get(view.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: "plpgsql"}))
		if language == nil || language.Namespace() != (ids.ObjectAddress{}) || catalog.ExtensionMembership(language) != (ids.ObjectAddress{}) {
			t.Fatal("global membership delta reseeded or gained namespace")
		}
		if snapshot.Get(detached.Address()) != detached || base.Get(p.Address()) != p || p.Extension != ext.Address() {
			t.Fatal("remap mutated a captured predecessor")
		}
	})
	t.Run("strict_metadata", func(t *testing.T) {
		e, ext := ownershipUnitInstall(t, "unaccent")
		view := e.CatalogRead()
		metadata, err := extension.OwnerMetadata(view, e.Roles(), ext)
		if err != nil {
			t.Fatal(err)
		}
		if extension.OwnersMarker != "stuntgres-extension-owners-v3" {
			t.Fatal("obsolete slot codec marker", extension.OwnersMarker)
		}
		prefix := extension.OwnersMarker + " "
		payload := strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")), prefix)
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]any
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{
			"stuntgres-extension-owners-v2 " + payload,
			prefix + "!!!!",
			prefix + base64.StdEncoding.EncodeToString([]byte(`{"owner":"postgres","owner":"postgres"}`)),
			prefix + base64.StdEncoding.EncodeToString([]byte(`{"unknown":true}`)),
			prefix + base64.StdEncoding.EncodeToString([]byte(`null`)),
		} {
			if _, found, err := extension.DecodeOwners(raw); !found || err == nil {
				t.Fatal("malformed/obsolete metadata accepted", raw, found, err)
			}
		}
		for _, key := range []string{"members", "owner", "name", "version"} {
			missing := make(map[string]any, len(record))
			for k, v := range record {
				if k != key {
					missing[k] = v
				}
			}
			encoded, err := json.Marshal(missing)
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := extension.DecodeOwners(prefix + base64.StdEncoding.EncodeToString(encoded)); !found || err == nil {
				t.Fatal("missing required metadata accepted", key, found, err)
			}
		}
		for _, key := range []string{"members", "owner", "name", "version"} {
			for _, value := range []any{nil, []any{}, "missing"} {
				copy := make(map[string]any, len(record))
				for k, v := range record {
					copy[k] = v
				}
				copy[key] = value
				encoded, err := json.Marshal(copy)
				if err != nil {
					t.Fatal(err)
				}
				decoded, found, err := extension.DecodeOwners(prefix + base64.StdEncoding.EncodeToString(encoded))
				if !found {
					t.Fatal("metadata marker ignored")
				}
				if err == nil {
					candidate := catalog.CloneState(view.State())
					if _, err = extension.ImportOwners(context.Background(), e.Catalog.Read(candidate), e.Roles(), []extension.OwnerRecord{decoded}); err == nil {
						t.Fatal("invalid metadata accepted", key, value)
					}
				}
				if e.CatalogRead().State() != view.State() {
					t.Fatal("failed metadata validation published")
				}
			}
		}
	})
}
