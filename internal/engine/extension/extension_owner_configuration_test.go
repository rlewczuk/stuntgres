package extension

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestExtensionOwnerV3ConfigurationMembership(t *testing.T) {
	view, ext := identityFixture("1.2")
	roleState := authorization.Seed(view.Owner().Principals)
	owner := view.Owner().Principals.Principal(99)
	roleState, err := roleState.PutDescriptor(authorization.RoleDescriptor{Ref: owner, Name: "configuration_owner"})
	if err != nil {
		t.Fatal(err)
	}
	roles := roleState.Read(ext.Owner)
	catalog.InstallBuiltinTSParsers(view.State())
	defaultParser := view.MustTSParser(ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: 3722})
	configuration := &catalog.TSConfiguration{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 9400}, ObjectNamespace: ext.TargetNamespace, ObjectName: "member_configuration", ObjectRevision: 1, Parser: defaultParser.Address(), Owner: owner, Extension: ext.Address()}
	identityPut(view.State(), configuration)
	next := *ext
	next.Members = append(slices.Clone(ext.Members), configuration.Address())
	identityPut(view.State(), &next)
	ext = &next
	if err := (catalog.TSConfigurationProvider{}).Validate(view, configuration); err != nil {
		t.Fatal(err)
	}
	identity, err := OwnerIdentity(view, configuration)
	if err != nil || identity.Kind != "configuration" || identity.Schema != "installed" || identity.Name != configuration.Name() || identity.Signature != "" {
		t.Fatal("configuration symbolic identity", identity, err)
	}
	sql, err := MemberSQL(view, configuration)
	if err != nil || sql != `TEXT SEARCH CONFIGURATION "installed"."member_configuration"` {
		t.Fatal("real membership SQL alternative", sql, err)
	}
	text, err := OwnerMetadata(view, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := DecodeOwners(strings.TrimSpace(strings.TrimPrefix(text, "-- ")))
	if err != nil || !found {
		t.Fatal(found, err)
	}
	configurationIndex := -1
	for i, member := range record.Members {
		if member.Kind == "configuration" {
			configurationIndex = i
			if member.Extension != ext.Name() || member.Declaration != nil || member.Version != "" || member.Owner != "" || member.ACL != nil {
				t.Fatal("ordinary configuration acquired installed owner/ACL authority", member)
			}
		}
	}
	if configurationIndex < 0 {
		t.Fatal("configuration omitted from current membership")
	}
	candidate := catalog.CloneState(view.State())
	if changed, err := ImportOwners(context.Background(), view.WithState(candidate), roles, []OwnerRecord{record}); err != nil || changed {
		t.Fatal("unchanged ordinary member import", changed, err)
	}
	if candidate.Get(configuration.Address()) != configuration || configuration.Owner != owner {
		t.Fatal("ordinary serializer's owner was overwritten")
	}
	bad := record
	bad.Members = slices.Clone(record.Members)
	bad.Members[configurationIndex].Owner = "postgres"
	if _, _, err := DecodeOwners(encodeV3Record(t, bad)); err == nil {
		t.Fatal("duplicate configuration owner authority decoded")
	}
	if changed, err := ImportOwners(context.Background(), view.WithState(candidate), roles, []OwnerRecord{bad}); err == nil || changed || candidate.Get(configuration.Address()) != configuration {
		t.Fatal("invalid configuration authority mutated candidate", changed, err)
	}
	// Ordinary owner-bearing classes share the final parent graph fence with
	// ownerless parser members; replay admission itself still runs earlier.
	missingParent := *ext
	missingParent.Members = slices.Clone(ext.Members[:len(ext.Members)-1])
	identityPut(candidate, &missingParent)
	if changed, err := ImportOwners(context.Background(), view.WithState(candidate), roles, []OwnerRecord{record}); err == nil || changed || candidate.Get(configuration.Address()) != configuration {
		t.Fatal("ordinary import admitted stale parent graph", changed, err)
	}
	identityPut(candidate, ext)
	detached := *configuration
	detached.Extension = ids.ObjectAddress{}
	identityPut(candidate, &detached)
	if changed, err := ImportOwners(context.Background(), view.WithState(candidate), roles, []OwnerRecord{record}); err == nil || changed || candidate.Get(configuration.Address()) != &detached {
		t.Fatal("import manufactured missing reciprocal membership", changed, err)
	}
}
