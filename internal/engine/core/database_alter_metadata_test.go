package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

func TestDatabaseAlterMetadata(t *testing.T) {
	t.Run("numeric-option-tokens", func(t *testing.T) {
		for _, tc := range []struct {
			raw  string
			want bool
		}{{"0x1", true}, {"0b1", true}, {"0o1", true}, {"00_1", true}, {"+01", true}, {"-0", false}} {
			got, err := ddl.ParseAlterDatabaseOptions([]parser.DatabaseOption{{Name: "is_template", Value: tc.raw, Numeric: true}})
			if err != nil || !got.HasTemplate || got.Template != tc.want {
				t.Fatal("integer Boolean token", tc.raw, got, err)
			}
		}
		for _, tc := range []struct {
			raw  string
			want int32
		}{{"1_000", 1000}, {"0x7fff_ffff", 2147483647}, {"0b0", 0}, {"-0o1", -1}} {
			got, err := ddl.ParseAlterDatabaseOptions([]parser.DatabaseOption{{Name: "connection_limit", Value: tc.raw, Numeric: true}})
			if err != nil || !got.HasLimit || got.Limit != tc.want {
				t.Fatal("int32 option token", tc.raw, got, err)
			}
		}
		for _, raw := range []string{"1.0", "1e0", "0x80000000", "-0x80000001"} {
			_, err := ddl.ParseAlterDatabaseOptions([]parser.DatabaseOption{{Name: "connection_limit", Value: raw, Numeric: true}})
			if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "42601" {
				t.Fatal("non-int32 token", raw, err)
			}
		}
	})
	e, _, _, successor, _ := ownershipUnitRoles()
	original := e.CatalogRead().MustDatabase(e.Catalog.DatabaseAddress())
	if !original.AllowConnections || original.IsTemplate || original.ConnectionLimit != -1 || original.DefaultTablespace != catalog.DefaultTablespaceOID || original.CollationVersion != nil {
		t.Fatal("constructor metadata", original)
	}
	t.Run("shared-builtin-retention", func(t *testing.T) {
		stage, stageErr := e.RestoreStage()
		if stageErr != nil {
			t.Fatal(stageErr)
		}
		imported := catalog.CloneState(stage.Catalog.State)
		for _, builtin := range catalog.BuiltinTablespaces {
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}
			source := *imported.Get(address).(*catalog.Tablespace)
			imported.UnbindName(catalog.DescriptorNameKey(&source))
			source.ObjectName = "metadata_" + builtin.Name
			source.ObjectRevision++
			imported.SetDescriptor(address, &source)
			imported.BindName(catalog.DescriptorNameKey(&source), address)
		}
		stage.Catalog.PublishLocked(imported)
		// Source owner/ACL facts are stale but valid. Ordinary replacement must retain
		// current destination authority without discarding the candidate alias image.
		e.Mu.Lock()
		committed := catalog.CloneState(e.Catalog.State)
		expected := make(map[ids.ObjectAddress]*catalog.Tablespace)
		for _, builtin := range catalog.BuiltinTablespaces {
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}
			current := *committed.Get(address).(*catalog.Tablespace)
			current.Owner, current.ACL, current.ObjectRevision = successor, authorization.ACLValue{Explicit: true}, current.ObjectRevision+1
			committed.SetDescriptor(address, &current)
			expected[address] = &current
		}
		projection := e.Catalog.ProjectSharedLocked(committed, e.Cluster.Catalogs)
		e.Catalog.PublishLocked(committed)
		projection.InstallLocked()
		e.Mu.Unlock()
		if _, err := e.ReplaceCatalog(context.Background(), stage, stage.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		for _, builtin := range catalog.BuiltinTablespaces {
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}
			descriptor := e.CatalogRead().Get(address).(*catalog.Tablespace)
			if descriptor.Name() != "metadata_"+builtin.Name || descriptor.Owner != expected[address].Owner || !authorization.EqualACL(descriptor.ACL, expected[address].ACL) || e.CatalogRead().NameAddress(catalog.DescriptorNameKey(descriptor)) != address {
				t.Fatal("replacement lost candidate alias or current authority", descriptor)
			}
		}
		for _, address := range []ids.ObjectAddress{{ClassID: ids.TablespaceClassID, ObjectID: 900000}, {ClassID: ids.TablespaceClassID, ObjectID: catalog.DefaultTablespaceOID, SubID: 1}, {ClassID: ids.DatabaseClassID, ObjectID: 5, SubID: 1}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}} {
			rejected := catalog.CloneState(e.Catalog.State)
			descriptor := &catalog.Tablespace{ObjectAddress: address, ObjectRevision: 1, ObjectName: "invalid", Owner: successor}
			rejected.SetDescriptor(address, descriptor)
			rejected.BindName(catalog.DescriptorNameKey(descriptor), address)
			if err := (catalog.TablespaceProvider{}).Validate(e.Catalog.Read(rejected), descriptor); err == nil {
				t.Fatal("non-shared identity admitted", address)
			}
		}
	})
	for _, field := range []string{"allow", "template", "limit", "tablespace", "version", "revision", "name"} {
		changed := *original
		switch field {
		case "allow":
			changed.AllowConnections = false
		case "template":
			changed.IsTemplate = true
		case "limit":
			changed.ConnectionLimit = 2
		case "tablespace":
			changed.DefaultTablespace = catalog.GlobalTablespaceOID
		case "version":
			value := "1"
			changed.CollationVersion = &value
		case "revision":
			changed.ObjectRevision++
		case "name":
			changed.ObjectName = "renamed"
		}
		if catalog.DatabaseAuthorityCompatible(original, &changed) {
			t.Fatal("semantic drift accepted", field)
		}
	}
	horizon := *original
	horizon.FrozenXID++
	if !catalog.DatabaseAuthorityCompatible(original, &horizon) {
		t.Fatal("maintenance drift rejected")
	}
	e.Mu.Lock()
	changed := *original
	changed.IsTemplate, changed.AllowConnections, changed.ConnectionLimit = true, false, 3
	stage := e.BeginCatalogStage(e.Catalog.Current())
	err := stage.Replace(&changed)
	if err == nil {
		err = role.StageOwnership(stage, &changed, successor)
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		t.Fatal(err)
	}
	retained := stage.Get(original.Address()).(*catalog.Database)
	if retained.Owner != successor || !retained.IsTemplate || retained.AllowConnections || retained.ConnectionLimit != 3 || retained.DefaultTablespace != original.DefaultTablespace {
		t.Fatal("ownership lost metadata", retained)
	}
	if e.CatalogRead().MustDatabase(original.Address()) != original {
		t.Fatal("private metadata escaped")
	}
	old, newer := "1", "2"
	for _, tc := range []struct {
		recorded, actual *string
		changed, invalid bool
	}{{nil, nil, false, false}, {&old, &old, false, false}, {&old, &newer, true, false}, {nil, &old, false, true}, {&old, nil, false, true}} {
		version, changed, err := ddl.DatabaseCollationVersion(tc.recorded, tc.actual)
		if tc.invalid {
			if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "XX000" {
				t.Fatal("invalid transition", err)
			}
			continue
		}
		if err != nil || changed != tc.changed {
			t.Fatal("version decision", changed, err)
		}
		if changed && (version == tc.actual || *version != *tc.actual) {
			t.Fatal("changed version borrowed provider memory")
		}
	}
}
