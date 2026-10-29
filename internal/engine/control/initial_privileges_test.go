package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func initialMetadataComment(data string) string {
	return InitialPrivilegesMarker + " " + base64.StdEncoding.EncodeToString([]byte(data))
}

func TestPGInitPrivsMetadata(t *testing.T) {
	raw := `{"complete":true,"records":[{"class":2328,"kind":"foreign_wrapper","schema":"","name":"native_fdw","signature":"","subid":0,"privtype":"e","entries":[{"grantor":"postgres","grantee":"","public":true,"privileges":256,"grant_options":0}]}]}`
	// Build the valid mask from its declaration, not a duplicated numeric guess.
	var valid InitialPrivilegesMetadata
	if err := json.Unmarshal([]byte(raw), &valid); err != nil {
		t.Fatal(err)
	}
	valid.Records[0].Entries[0].Privileges = authorization.UsagePrivilege
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	raw = string(encoded)
	m, recognized, err := DecodeInitialPrivileges(initialMetadataComment(raw))
	if err != nil || !recognized || !reflect.DeepEqual(m, &valid) {
		t.Fatalf("valid record: %v %v %#v", recognized, err, m)
	}
	comment, err := m.Comment()
	if err != nil {
		t.Fatal(err)
	}
	if _, recognized, err := DecodeInitialPrivileges(strings.TrimPrefix(strings.TrimSpace(comment), "-- ")); !recognized || err != nil {
		t.Fatalf("round trip: %v %v", recognized, err)
	}
	for name, data := range map[string]string{
		"duplicate root":       strings.Replace(raw, `"complete":true`, `"complete":true,"complete":true`, 1),
		"unknown root":         strings.Replace(raw, `"complete":true`, `"complete":true,"future":1`, 1),
		"null root":            `{"complete":true,"records":null}`,
		"missing marker":       `{"records":[]}`,
		"false marker":         `{"complete":false,"records":[]}`,
		"duplicate record key": strings.Replace(raw, `"subid":0`, `"subid":0,"subid":0`, 1),
		"null field":           strings.Replace(raw, `"signature":""`, `"signature":null`, 1),
		"negative column":      strings.Replace(raw, `"subid":0`, `"subid":-1`, 1),
		"foreign column":       strings.Replace(raw, `"subid":0`, `"subid":1`, 1),
		"wrong kind":           strings.Replace(raw, `"kind":"foreign_wrapper"`, `"kind":"relation"`, 1),
		"bad origin":           strings.Replace(raw, `"privtype":"e"`, `"privtype":"x"`, 1),
		"empty entries":        strings.Replace(raw, string(mustInitialJSON(t, valid.Records[0].Entries)), `[]`, 1),
		"duplicate grant key":  strings.Replace(raw, `"grantor":"postgres"`, `"grantor":"postgres","grantor":"postgres"`, 1),
		"public role":          strings.Replace(raw, `"grantee":""`, `"grantee":"postgres"`, 1),
		"missing role":         strings.Replace(raw, `"grantor":"postgres"`, `"grantor":""`, 1),
		"unknown mask":         strings.Replace(raw, `"privileges":`+string(mustInitialJSON(t, authorization.UsagePrivilege)), `"privileges":2147483648`, 1),
		"grant options":        strings.Replace(raw, `"grant_options":0`, `"grant_options":1`, 1),
		"trailing data":        raw + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, recognized, err := DecodeInitialPrivileges(initialMetadataComment(data)); !recognized || err == nil {
				t.Fatalf("malformed metadata accepted: %s", data)
			}
		})
	}
	for _, text := range []string{"stuntgres-initial-privileges-v2 e30=", InitialPrivilegesMarker + " !!!"} {
		if _, recognized, err := DecodeInitialPrivileges(text); !recognized || err == nil {
			t.Fatalf("bad marker accepted: %s", text)
		}
	}
	duplicate := valid
	duplicate.Records = append(append([]InitialPrivilegeMetadataRecord(nil), valid.Records...), valid.Records[0])
	if _, _, err := DecodeInitialPrivileges(initialMetadataComment(string(mustInitialJSON(t, duplicate)))); err == nil {
		t.Fatal("duplicate target accepted")
	}
	if _, recognized, err := DecodeInitialPrivileges("ordinary SQL comment"); recognized || err != nil {
		t.Fatal("ordinary comment consumed")
	}
}

func mustInitialJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPGInitPrivsRemap(t *testing.T) {
	source, wrapper, server, _ := fdwUnitSource()
	owner := source.Bootstrap
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.UsagePrivilege, GrantOptions: authorization.UsagePrivilege},
	}}
	a, b := wrapper.Address(), server.Address()
	source.State.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{a: {PrivType: 'e', ACL: acl}, b: {PrivType: 'i', ACL: acl}})
	source.State.SetInitialPrivilegeBaseline(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{a: source.State.InitialPrivilege(a)})
	next := catalog.CloneState(source.State)
	targetDomain := new(authorization.Domain)
	principal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		return targetDomain.Principal(ref.ID()), nil
	}
	if err := catalog.RemapPrincipals(next, catalog.PrincipalRemap{Principal: principal, SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return principal(ref)
	}}); err != nil {
		t.Fatal(err)
	}
	mapped := next.InitialPrivilege(a)
	if mapped.ACL.Entries[0].Grantor == owner || mapped.ACL.Entries[1].Grantee.Role == owner || !mapped.ACL.Entries[0].Grantee.Public || source.State.InitialPrivilege(a).ACL.Entries[0].Grantor != owner {
		t.Fatal("principal remap retained foreign authority or mutated source")
	}
	for address, record := range next.InitialPrivilegeBaseline() {
		if address != a || record.ACL.Entries[0].Grantor == owner || record.ACL.Entries[0].Grantor != mapped.ACL.Entries[0].Grantor {
			t.Fatal("comparison baseline retained foreign principal authority")
		}
	}
	newA, newB := a, b
	newA.ObjectID += 100
	newB.ObjectID += 200
	if err := RemapReplacementIdentities(source.CatalogView, next, nil, map[ids.ObjectAddress]ids.ObjectAddress{a: newA, b: newB}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if next.InitialPrivilege(a) != nil || next.InitialPrivilege(b) != nil || next.InitialPrivilege(newA) != mapped || next.InitialPrivilege(newB).PrivType != 'i' || source.State.InitialPrivilege(a) == nil {
		t.Fatal("historical address remap lost data or changed source")
	}
	for address := range next.InitialPrivilegeBaseline() {
		if address != newA {
			t.Fatal("comparison baseline did not follow object mapping", address)
		}
	}
	broken := catalog.CloneState(next)
	if err := remapInitialPrivilegeAddresses(broken, map[ids.ObjectAddress]ids.ObjectAddress{newA: newB}); err == nil {
		t.Fatal("cross-class mapping accepted")
	}
	if broken.InitialPrivilege(newA) != mapped {
		t.Fatal("failed remap changed historical map")
	}

	metadata, err := CaptureInitialPrivileges(context.Background(), source.CatalogView, source.OriginalRoles)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveInitialPrivileges(context.Background(), source.CatalogView, source.OriginalRoles, metadata)
	if err != nil || !reflect.DeepEqual(resolved, map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{a: source.State.InitialPrivilege(a), b: source.State.InitialPrivilege(b)}) {
		t.Fatalf("metadata resolver: %v %#v", err, resolved)
	}
	bad := *metadata
	bad.Records = append([]InitialPrivilegeMetadataRecord(nil), metadata.Records...)
	bad.Records[0].Name = "absent"
	if _, err := ResolveInitialPrivileges(context.Background(), source.CatalogView, source.OriginalRoles, &bad); err == nil {
		t.Fatal("absent object accepted")
	}
	bad.Records[0] = metadata.Records[0]
	bad.Records[0].Entries = []extension.MemberGrant{{Grantor: "absent", Public: true, Privileges: authorization.UsagePrivilege}}
	if _, err := ResolveInitialPrivileges(context.Background(), source.CatalogView, source.OriginalRoles, &bad); err == nil {
		t.Fatal("absent principal accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CaptureInitialPrivileges(ctx, source.CatalogView, source.OriginalRoles); err != context.Canceled {
		t.Fatalf("capture cancellation: %v", err)
	}
	if _, err := ResolveInitialPrivileges(ctx, source.CatalogView, source.OriginalRoles, metadata); err != context.Canceled {
		t.Fatalf("resolve cancellation: %v", err)
	}
}
