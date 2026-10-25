package extension

import (
	"encoding/json"
	"fmt"

	"stuntgres/internal/engine/authorization"
)

// MemberACL belongs to a validated installation slot, never a loose routine or
// a source PrincipalRef. Presence preserves explicit-empty versus absent ACLs.
type MemberACL struct {
	Explicit bool `json:"explicit"`
	Entries []MemberGrant `json:"entries"`
}
type MemberGrant struct {
	Grantor string `json:"grantor"`
	Grantee string `json:"grantee"`
	Public bool `json:"public"`
	Execute bool `json:"execute"`
	GrantOption bool `json:"grant_option"`
}

func captureMemberACL(roles authorization.Read, acl authorization.ACLValue) (*MemberACL, error) {
	if err := authorization.ValidateACL(roles, acl, authorization.ExecutePrivilege); err != nil { return nil, err }
	out := &MemberACL{Explicit: acl.Explicit, Entries: make([]MemberGrant, len(acl.Entries))}
	for i, entry := range acl.Entries {
		grantor, err := roles.Descriptor(entry.Grantor)
		if err != nil { return nil, err }
		grant := MemberGrant{Grantor: grantor.Name, Public: entry.Grantee.Public, Execute: entry.Privileges != 0, GrantOption: entry.GrantOptions != 0}
		if !grant.Public {
			grantee, err := roles.Descriptor(entry.Grantee.Role)
			if err != nil { return nil, err }
			grant.Grantee = grantee.Name
		}
		out.Entries[i] = grant
	}
	if err := validateMemberACL(out); err != nil { return nil, err }
	return out, nil
}

func validateMemberACL(acl *MemberACL) error {
	if acl == nil { return nil }
	invalid := func() error { return fmt.Errorf("stuntgres: invalid extension member ACL") }
	if !acl.Explicit && len(acl.Entries) != 0 { return invalid() }
	type grantKey struct { grantor, grantee string; public bool }
	seen := make(map[grantKey]bool, len(acl.Entries))
	for _, grant := range acl.Entries {
		key := grantKey{grant.Grantor, grant.Grantee, grant.Public}
		if grant.Grantor == "" || grant.Public && (grant.Grantee != "" || grant.GrantOption) || !grant.Public && grant.Grantee == "" || grant.GrantOption && !grant.Execute || seen[key] { return invalid() }
		seen[key] = true
	}
	return nil
}

func decodeMemberACL(data []byte) (*MemberACL, error) {
	fields, err := extensionOwnerFields(data, "explicit", "entries")
	if err != nil { return nil, err }
	out := new(MemberACL)
	if err := json.Unmarshal(fields["explicit"], &out.Explicit); err != nil { return nil, err }
	var entries []json.RawMessage
	if err := json.Unmarshal(fields["entries"], &entries); err != nil { return nil, err }
	out.Entries = make([]MemberGrant, len(entries))
	for i, entry := range entries {
		fields, err := extensionOwnerFields(entry, "grantor", "grantee", "public", "execute", "grant_option")
		if err != nil { return nil, err }
		grant := &out.Entries[i]
		for key, target := range map[string]*string{"grantor": &grant.Grantor, "grantee": &grant.Grantee} {
			if err := json.Unmarshal(fields[key], target); err != nil { return nil, err }
		}
		for key, target := range map[string]*bool{"public": &grant.Public, "execute": &grant.Execute, "grant_option": &grant.GrantOption} {
			if err := json.Unmarshal(fields[key], target); err != nil { return nil, err }
		}
	}
	if err := validateMemberACL(out); err != nil { return nil, err }
	return out, nil
}

func restoreMemberACL(roles authorization.Read, encoded *MemberACL) (authorization.ACLValue, error) {
	var out authorization.ACLValue
	if err := validateMemberACL(encoded); err != nil { return out, err }
	out.Explicit = encoded.Explicit
	if len(encoded.Entries) != 0 { out.Entries = make([]authorization.ACLEntry, len(encoded.Entries)) }
	for i, grant := range encoded.Entries {
		grantor, ok := roles.Lookup(grant.Grantor)
		if !ok { return out, fmt.Errorf("stuntgres: extension ACL grantor %q does not exist", grant.Grantor) }
		entry := authorization.ACLEntry{Grantor: grantor.Ref, Grantee: authorization.ACLGrantee{Public: grant.Public}}
		if !grant.Public {
			grantee, ok := roles.Lookup(grant.Grantee)
			if !ok { return out, fmt.Errorf("stuntgres: extension ACL grantee %q does not exist", grant.Grantee) }
			entry.Grantee.Role = grantee.Ref
		}
		if grant.Execute { entry.Privileges = authorization.ExecutePrivilege }
		if grant.GrantOption { entry.GrantOptions = authorization.ExecutePrivilege }
		out.Entries[i] = entry
	}
	return out, authorization.ValidateACL(roles, out, authorization.ExecutePrivilege)
}
