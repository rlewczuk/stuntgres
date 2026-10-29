package extension

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

const OwnersMarker = "stuntgres-extension-owners-v3"

// Declaration identifies an immutable release declaration, never a membership
// position. Ordinary SQL members have no declaration or provider authority.
type MemberOwner struct {
	Declaration *int       `json:"declaration,omitempty"`
	Version     string     `json:"version"`
	Schema      string     `json:"schema"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	Signature   string     `json:"signature"`
	Extension   string     `json:"extension"`
	Owner       string     `json:"owner"`
	ACL         *MemberACL `json:"acl,omitempty"`
}
type OwnerRecord struct {
	Name    string        `json:"name"`
	Schema  string        `json:"schema"`
	Version string        `json:"version"`
	Owner   string        `json:"owner"`
	Present bool          `json:"present"`
	Members []MemberOwner `json:"members"`
}

func OwnerMetadata(view catalog.Read, roles authorization.Read, ext *catalog.Extension) (string, error) {
	if ext == nil || !ext.Installation.Valid() {
		return "", InvalidProviderDescriptor()
	}
	if view.Owner() == nil {
		return "", InvalidProviderDescriptor()
	}
	if err := ValidateCurrentInstallation(view, ext, ext.Installation.Implementation); err != nil {
		return "", err
	}
	return InstallationOwnerMetadata(view, roles, ext.Installation, ext)
}

// InstallationOwnerMetadata also describes survivors whose originating extension
// no longer exists. The recipe is authority; the final extension name is not.
func InstallationOwnerMetadata(view catalog.Read, roles authorization.Read, installation *catalog.Installation, ext *catalog.Extension) (string, error) {
	if !installation.Valid() {
		return "", InvalidProviderDescriptor()
	}
	record := OwnerRecord{Name: installation.Provider, Version: installation.Version, Members: make([]MemberOwner, 0)}
	if ext != nil {
		ns, ok := view.Namespace(ext.TargetNamespace)
		if !ok {
			return "", InvalidProviderDescriptor()
		}
		owner, err := roles.Descriptor(ext.Owner)
		if err != nil {
			return "", err
		}
		record.Schema, record.Owner, record.Present = ns.Name(), owner.Name, true
	}
	for _, d := range view.Descriptors() {
		if namespace, ok := view.Namespace(d.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		origin := catalog.DescriptorOrigin(d)
		belongs := origin.Valid(d.Address()) && origin.Installation.Cohort == installation.Cohort
		ordinary := origin == nil && ext != nil && catalog.ExtensionMembership(d) == ext.Address()
		if !belongs && !ordinary {
			continue
		}
		member, err := OwnerIdentity(view, d)
		if err != nil {
			return "", err
		}
		if belongs {
			declaration := origin.Declaration
			member.Declaration = &declaration
			member.Version = origin.Installation.Version
		}
		membership := catalog.ExtensionMembership(d)
		if membership != (ids.ObjectAddress{}) {
			current, ok := view.Extension(membership)
			if !ok {
				return "", InvalidProviderDescriptor()
			}
			member.Extension = current.Name()
		}
		// Ordinary serializers remain the sole owner/ACL authority for SQL objects.
		if belongs {
			if err := validateInstalledDeclaration(view, d); err != nil {
				return "", err
			}
			principal, acl, _ := access.DescriptorACL(d)
			if principal != (authorization.PrincipalRef{}) {
				owner, err := roles.Descriptor(principal)
				if err != nil {
					return "", err
				}
				member.Owner = owner.Name
			}
			if access.ClassPrivileges(d) != 0 {
				if typ, ok := d.(*catalog.Type); ok && typ.ACLSyntheticDefault {
					// The stored baseline represents a SQL NULL ACL, not grants
					// tied to the historical owner in this captured descriptor.
					acl = authorization.ACLValue{}
				}
				member.ACL, err = captureMemberACL(roles, acl)
				if err != nil {
					return "", err
				}
			}
		}
		record.Members = append(record.Members, member)
	}
	slices.SortFunc(record.Members, func(a, b MemberOwner) int { return strings.Compare(ownerIdentityKey(a), ownerIdentityKey(b)) })
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return "-- " + OwnersMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}

// Decode JSON keys explicitly: encoding/json alone accepts duplicate keys and
// nulls, neither of which can grant restore authority.
func extensionOwnerFields(data []byte, keys ...string) (map[string]json.RawMessage, error) {
	return extensionOwnerOptionalFields(data, nil, keys...)
}
func extensionOwnerOptionalFields(data []byte, optional []string, keys ...string) (map[string]json.RawMessage, error) {
	invalid := func() (map[string]json.RawMessage, error) {
		return nil, fmt.Errorf("stuntgres: invalid extension owner metadata")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	fields := make(map[string]json.RawMessage, len(keys))
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || fields[key] != nil {
			return invalid()
		}
		if !slices.Contains(keys, key) && !slices.Contains(optional, key) {
			return invalid()
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
		fields[key] = value
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return invalid()
	}
	for _, key := range keys {
		if fields[key] == nil {
			return invalid()
		}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return invalid()
	}
	return fields, nil
}
func DecodeOwners(comment string) (OwnerRecord, bool, error) {
	var record OwnerRecord
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-extension-owners-") {
		return record, false, nil
	}
	invalid := func() (OwnerRecord, bool, error) {
		return record, true, fmt.Errorf("stuntgres: invalid extension owner metadata")
	}
	marker, payload, found := strings.Cut(text, " ")
	if !found || marker != OwnersMarker || payload == "" {
		return invalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || !utf8.Valid(data) {
		return invalid()
	}
	fields, err := extensionOwnerFields(data, "name", "schema", "version", "owner", "present", "members")
	if err != nil {
		return invalid()
	}
	for key, target := range map[string]*string{"name": &record.Name, "schema": &record.Schema, "version": &record.Version, "owner": &record.Owner} {
		if json.Unmarshal(fields[key], target) != nil {
			return invalid()
		}
	}
	if record.Name == "" || record.Version == "" || json.Unmarshal(fields["present"], &record.Present) != nil || record.Present && (record.Owner == "" || record.Schema == "") || !record.Present && (record.Owner != "" || record.Schema != "") {
		return invalid()
	}
	var members []json.RawMessage
	if json.Unmarshal(fields["members"], &members) != nil || members == nil {
		return invalid()
	}
	seen := map[string]bool{}
	declarations := map[string]bool{}
	for _, data := range members {
		// A missing declaration means ordinary SQL. Null is never accepted.
		fields, err := extensionOwnerOptionalFields(data, []string{"declaration", "acl"}, "version", "schema", "kind", "name", "signature", "extension", "owner")
		if err != nil {
			return invalid()
		}
		var member MemberOwner
		for key, target := range map[string]*string{"version": &member.Version, "schema": &member.Schema, "kind": &member.Kind, "name": &member.Name, "signature": &member.Signature, "extension": &member.Extension, "owner": &member.Owner} {
			if json.Unmarshal(fields[key], target) != nil {
				return invalid()
			}
		}
		if value, ok := fields["declaration"]; ok {
			var declaration int
			if json.Unmarshal(value, &declaration) != nil || declaration < 0 || member.Version == "" {
				return invalid()
			}
			key := fmt.Sprint(member.Version, ":", declaration)
			if declarations[key] {
				return invalid()
			}
			declarations[key] = true
			member.Declaration = &declaration
		}
		if value, ok := fields["acl"]; ok {
			member.ACL, err = decodeMemberACL(value)
			if err != nil {
				return invalid()
			}
		}
		if !validOwnerIdentity(member) || member.Declaration == nil && (member.Version != "" || member.Owner != "" || member.ACL != nil || !record.Present || member.Extension != record.Name) {
			return invalid()
		}
		key := ownerIdentityKey(member)
		if seen[key] {
			return invalid()
		}
		seen[key] = true
		record.Members = append(record.Members, member)
	}
	return record, true, nil
}
