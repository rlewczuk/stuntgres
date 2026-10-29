package extension

import (
	"encoding/json"
	"fmt"
	"stuntgres/internal/engine/authorization"
)

// MemberACL preserves only SQL grants, never source-domain principal handles.
type MemberACL struct {
	Explicit bool          `json:"explicit"`
	Entries  []MemberGrant `json:"entries"`
}
type MemberGrant struct {
	Grantor      string                   `json:"grantor"`
	Grantee      string                   `json:"grantee"`
	Public       bool                     `json:"public"`
	Privileges   authorization.Privileges `json:"privileges"`
	GrantOptions authorization.Privileges `json:"grant_options"`
}

func captureMemberACL(roles authorization.Read, acl authorization.ACLValue) (*MemberACL, error) {
	out := &MemberACL{Explicit: acl.Explicit, Entries: make([]MemberGrant, len(acl.Entries))}
	for i, entry := range acl.Entries {
		grantor, err := roles.Descriptor(entry.Grantor)
		if err != nil {
			return nil, err
		}
		grant := MemberGrant{Grantor: grantor.Name, Public: entry.Grantee.Public, Privileges: entry.Privileges, GrantOptions: entry.GrantOptions}
		if !grant.Public {
			grantee, err := roles.Descriptor(entry.Grantee.Role)
			if err != nil {
				return nil, err
			}
			grant.Grantee = grantee.Name
		}
		out.Entries[i] = grant
	}
	if err := validateMemberACL(out); err != nil {
		return nil, err
	}
	return out, nil
}
func validateMemberACL(acl *MemberACL) error {
	if acl == nil {
		return nil
	}
	invalid := func() error { return fmt.Errorf("stuntgres: invalid extension member ACL") }
	if !acl.Explicit && len(acl.Entries) != 0 {
		return invalid()
	}
	type key struct {
		grantor, grantee string
		public           bool
	}
	seen := map[key]bool{}
	for _, grant := range acl.Entries {
		k := key{grant.Grantor, grant.Grantee, grant.Public}
		if grant.Grantor == "" || grant.Public && (grant.Grantee != "" || grant.GrantOptions != 0) || !grant.Public && grant.Grantee == "" || grant.GrantOptions & ^grant.Privileges != 0 || seen[k] {
			return invalid()
		}
		seen[k] = true
	}
	return nil
}
func decodeMemberACL(data []byte) (*MemberACL, error) {
	fields, err := extensionOwnerFields(data, "explicit", "entries")
	if err != nil {
		return nil, err
	}
	out := new(MemberACL)
	if err := json.Unmarshal(fields["explicit"], &out.Explicit); err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(fields["entries"], &entries); err != nil {
		return nil, err
	}
	out.Entries = make([]MemberGrant, len(entries))
	for i, entry := range entries {
		fields, err := extensionOwnerFields(entry, "grantor", "grantee", "public", "privileges", "grant_options")
		if err != nil {
			return nil, err
		}
		grant := &out.Entries[i]
		for key, target := range map[string]*string{"grantor": &grant.Grantor, "grantee": &grant.Grantee} {
			if err := json.Unmarshal(fields[key], target); err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal(fields["public"], &grant.Public); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(fields["privileges"], &grant.Privileges); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(fields["grant_options"], &grant.GrantOptions); err != nil {
			return nil, err
		}
	}
	if err := validateMemberACL(out); err != nil {
		return nil, err
	}
	return out, nil
}
func restoreMemberACL(roles authorization.Read, encoded *MemberACL) (authorization.ACLValue, error) {
	var out authorization.ACLValue
	if err := validateMemberACL(encoded); err != nil {
		return out, err
	}
	out.Explicit = encoded.Explicit
	if len(encoded.Entries) != 0 {
		out.Entries = make([]authorization.ACLEntry, len(encoded.Entries))
	}
	for i, grant := range encoded.Entries {
		grantor, ok := roles.Lookup(grant.Grantor)
		if !ok {
			return out, fmt.Errorf("stuntgres: extension ACL grantor %q does not exist", grant.Grantor)
		}
		entry := authorization.ACLEntry{Grantor: grantor.Ref, Grantee: authorization.ACLGrantee{Public: grant.Public}, Privileges: grant.Privileges, GrantOptions: grant.GrantOptions}
		if !grant.Public {
			grantee, ok := roles.Lookup(grant.Grantee)
			if !ok {
				return out, fmt.Errorf("stuntgres: extension ACL grantee %q does not exist", grant.Grantee)
			}
			entry.Grantee.Role = grantee.Ref
		}
		out.Entries[i] = entry
	}
	return out, nil
}
