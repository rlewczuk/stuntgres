package extension

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func encodeV3Record(t *testing.T, record OwnerRecord) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)
}

func TestExtensionOwnerV3RepresentedIdentities(t *testing.T) {
	record := OwnerRecord{Name: "unaccent", Schema: "target", Version: "1.1", Owner: "postgres", Present: true, Members: make([]MemberOwner, 0)}
	kinds := []string{"procedure", "type", "relation", "operator", "operator_class", "operator_family", "parser", "template", "dictionary", "configuration", "collation", "conversion", "schema", "language", "access_method", "event_trigger", "foreign_wrapper", "foreign_server", "cast"}
	for _, kind := range kinds {
		member := MemberOwner{Kind: kind, Schema: "objects", Name: "object_" + kind, Extension: "unaccent"}
		switch kind {
		case "schema", "language", "access_method", "event_trigger", "foreign_wrapper", "foreign_server", "cast":
			member.Schema = ""
		}
		switch kind {
		case "procedure":
			member.Signature = "integer, pg_catalog.text"
		case "operator":
			member.Signature = "integer, integer"
		case "operator_class", "operator_family":
			member.Signature = "btree"
		case "cast":
			member.Name = "integer"
			member.Signature = "pg_catalog.text"
		}
		record.Members = append(record.Members, member)
	}
	decoded, found, err := DecodeOwners(encodeV3Record(t, record))
	if err != nil || !found || len(decoded.Members) != len(kinds) {
		t.Fatal(decoded, found, err)
	}
	for i, member := range decoded.Members {
		if member.Declaration != nil || member.Owner != "" || member.Version != "" || member.ACL != nil || member.Kind != kinds[i] {
			t.Fatal("ordinary member acquired installed authority", member)
		}
	}
	for _, version := range []string{"v1", "v2", "v4"} {
		text := strings.Replace(encodeV3Record(t, record), "owners-v3", "owners-"+version, 1)
		if _, found, err := DecodeOwners(text); !found || err == nil {
			t.Fatal("non-v3 codec accepted", text, found, err)
		}
	}
	record.Members[0].Owner = "postgres"
	if _, _, err := DecodeOwners(encodeV3Record(t, record)); err == nil {
		t.Fatal("ordinary serializer owner was duplicated in installation authority")
	}
}

func TestExtensionOwnerV3ReleaseDeclarations(t *testing.T) {
	zero := 0
	record := OwnerRecord{Name: "fuzzystrmatch", Schema: "public", Version: "1.2", Owner: "postgres", Present: true, Members: []MemberOwner{
		{Declaration: &zero, Version: "1.1", Kind: "procedure", Schema: "public", Name: "older", Signature: "pg_catalog.text, pg_catalog.text", Extension: "fuzzystrmatch", Owner: "postgres"},
		{Declaration: &zero, Version: "1.2", Kind: "procedure", Schema: "elsewhere", Name: "newer", Signature: "pg_catalog.text", Extension: "", Owner: "postgres"},
	}}
	if _, found, err := DecodeOwners(encodeV3Record(t, record)); !found || err != nil {
		t.Fatal("distinct release declarations conflated", found, err)
	}
	record.Members[1].Version = "1.1"
	if _, _, err := DecodeOwners(encodeV3Record(t, record)); err == nil {
		t.Fatal("duplicate immutable release declaration accepted")
	}
}

func TestExtensionOwnerImportLateFailureAtomic(t *testing.T) {
	view, ext := identityFixture("1.2")
	root := authorization.Seed(view.Owner().Principals)
	recipient := view.Owner().Principals.Principal(99)
	root, err := root.PutDescriptor(authorization.RoleDescriptor{Ref: recipient, Name: "recipient"})
	if err != nil {
		t.Fatal(err)
	}
	roles := root.Read(ext.Owner)
	text, err := OwnerMetadata(view, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := DecodeOwners(strings.TrimSpace(strings.TrimPrefix(text, "-- ")))
	if !found || err != nil {
		t.Fatal(found, err)
	}
	record.Members[0].Owner = "recipient"
	record.Members[len(record.Members)-1].Owner = "missing_owner"
	candidate := catalog.CloneState(view.State())
	if changed, err := ImportOwners(context.Background(), view.WithState(candidate), roles, []OwnerRecord{record}); changed || err == nil {
		t.Fatal("late missing role accepted", changed, err)
	}
	for address, descriptor := range view.Descriptors() {
		if candidate.Get(address) != descriptor {
			t.Fatal("failed import changed private candidate", address)
		}
	}
}

func TestExtensionOwnerReplayValidationPhase(t *testing.T) {
	view, ext := identityFixture("1.2")
	root := authorization.Seed(view.Owner().Principals)
	roles := root.Read(ext.Owner)
	text, err := OwnerMetadata(view, roles, ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := DecodeOwners(strings.TrimSpace(strings.TrimPrefix(text, "-- ")))
	if !found || err != nil {
		t.Fatal(found, err)
	}
	stage := catalog.CloneState(view.State())
	detached := *ext
	detached.Members = nil
	stage.SetDescriptor(ext.Address(), &detached)
	for _, address := range ext.Members {
		current := stage.Get(address)
		next, err := catalog.WithExtensionMembership(current, ids.ObjectAddress{}, current.Revision())
		if err != nil {
			t.Fatal(err)
		}
		stage.SetDescriptor(address, next)
	}
	current := view.WithState(stage)
	record.Members = append(record.Members, MemberOwner{Kind: "procedure", Schema: "public", Name: "ordinary_later", Signature: "pg_catalog.text", Extension: ext.Name()})
	snapshot := catalog.CloneState(stage)
	if err := ValidateOwnerReplay(context.Background(), current, roles, []OwnerRecord{record}); err != nil {
		t.Fatal("authority phase required final membership or future ordinary CREATE", err)
	}
	for _, change := range []string{"version", "identity", "signature", "missing"} {
		t.Run(change, func(t *testing.T) {
			bad := record
			bad.Members = append([]MemberOwner(nil), record.Members...)
			switch change {
			case "version":
				bad.Version = "0.0"
			case "identity":
				bad.Members[0].Name = "missing_installed"
			case "signature":
				bad.Members[0].Signature = "pg_catalog.oid"
			case "missing":
				bad.Members = bad.Members[1:]
			}
			err := ValidateOwnerReplay(context.Background(), current, roles, []OwnerRecord{bad})
			var sql interface{ SQLState() string }
			if err == nil || err.Error() != "stuntgres: invalid extension owner metadata" || errors.As(err, &sql) {
				t.Fatal("malformed authority did not return ordinary codec rejection", err)
			}
		})
	}
	if _, err := ImportOwners(context.Background(), current, roles, []OwnerRecord{record}); err == nil {
		t.Fatal("final import skipped current membership or ordinary target validation")
	}
	for address, original := range snapshot.Descriptors() {
		if stage.Get(address) != original {
			t.Fatal("validation-only pass changed a private descriptor", address)
		}
	}
}
