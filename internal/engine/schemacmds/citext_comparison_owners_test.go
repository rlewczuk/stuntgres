package schemacmds_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres/internal/engine/extension"
)

func TestCitextComparisonProviderOwnerShape(t *testing.T) {
	e, ext, _ := citextUnitInstalled(t)
	view := e.CatalogRead()
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	original, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found {
		t.Fatal(found, err)
	}
	original = extensionOwnerRecipeOrder(original)
	for _, mutate := range []func(*extension.OwnerRecord){
		func(r *extension.OwnerRecord) { r.Members = r.Members[:3] },
		func(r *extension.OwnerRecord) { r.Members[3].Signature = "citext,25" },
		func(r *extension.OwnerRecord) { r.Members[9].Name = "<" },
		func(r *extension.OwnerRecord) {
			r.Members[3].Kind = "type"
			r.Members[3].Name = "citext"
			r.Members[3].Signature = ""
		},
		func(r *extension.OwnerRecord) { r.Members[3].Declaration = new(2) },
	} {
		bad := original
		bad.Members = append([]extension.MemberOwner(nil), original.Members...)
		mutate(&bad)
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if err := extensionOwnerAdmissionError(e, extension.OwnersMarker+" "+base64.StdEncoding.EncodeToString(data)); err != nil {
			continue
		}
		t.Fatal("malformed owner accepted", bad)
	}
	if len(original.Members) != extension.CitextMemberCount+1 || original.Members[3].Signature != "public.citext, public.citext" {
		t.Fatal("original inventory changed")
	}
}
