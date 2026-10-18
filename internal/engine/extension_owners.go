package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"unicode/utf8"
)

const extensionOwnersMarker = "stuntgres-extension-owners-v1"

type extensionMemberOwner struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Signature string `json:"signature"`
	Owner     string `json:"owner"`
}
type extensionOwnerRecord struct {
	Name    string                 `json:"name"`
	Schema  string                 `json:"schema"`
	Version string                 `json:"version"`
	Owner   string                 `json:"owner"`
	Members []extensionMemberOwner `json:"members"`
}

func extensionOwnerMetadata(catalog CatalogRead, roles authorization.Read, ext *extensionDescriptor) (string, error) {
	owner, err := roles.Descriptor(ext.owner)
	if err != nil {
		return "", err
	}
	ns, ok := catalog.state.descriptors[ext.targetNamespace].(*namespaceDescriptor)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing extension target namespace")
	}
	record := extensionOwnerRecord{Name: ext.name, Schema: ns.name, Version: ext.version, Owner: owner.Name}
	for _, a := range ext.members {
		d := catalog.state.descriptors[a]
		var member extensionMemberOwner
		var principal authorization.PrincipalRef
		switch d := d.(type) {
		case *procedureDescriptor:
			member = extensionMemberOwner{Kind: "procedure", Name: d.name, Signature: string(d.signature)}
			if d.queryCodec != nil && d.name == "bqarr_out" {
				member.Signature = "query_int"
			}
			principal = d.owner
		case *tsDictionaryDescriptor:
			member = extensionMemberOwner{Kind: "dictionary", Name: d.name}
			principal = d.owner
		case *typeDescriptor:
			member = extensionMemberOwner{Kind: "type", Name: d.Name()}
			principal = d.owner
		case *tsTemplateDescriptor:
			continue
		default:
			return "", fmt.Errorf("stuntgres: missing extension owner member")
		}
		role, err := roles.Descriptor(principal)
		if err != nil {
			return "", err
		}
		member.Owner = role.Name
		record.Members = append(record.Members, member)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return "-- " + extensionOwnersMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}

// Decode JSON keys explicitly: DisallowUnknownFields alone accepts duplicates
// and nulls, neither of which is valid ownership authority.
func extensionOwnerFields(data []byte, keys ...string) (map[string]json.RawMessage, error) {
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
		allowed := false
		for _, name := range keys {
			allowed = allowed || name == key
		}
		if !allowed {
			return invalid()
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
		fields[key] = value
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(fields) != len(keys) {
		return invalid()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return invalid()
	}
	return fields, nil
}
func decodeExtensionOwners(comment string) (extensionOwnerRecord, bool, error) {
	var record extensionOwnerRecord
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-extension-owners-") {
		return record, false, nil
	}
	invalid := func() (extensionOwnerRecord, bool, error) {
		return record, true, fmt.Errorf("stuntgres: invalid extension owner metadata")
	}
	marker, payload, found := strings.Cut(text, " ")
	if !found || marker != extensionOwnersMarker || payload == "" {
		return invalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || !utf8.Valid(data) {
		return invalid()
	}
	fields, err := extensionOwnerFields(data, "name", "schema", "version", "owner", "members")
	if err != nil {
		return invalid()
	}
	for key, target := range map[string]*string{"name": &record.Name, "schema": &record.Schema, "version": &record.Version, "owner": &record.Owner} {
		if err := json.Unmarshal(fields[key], target); err != nil || *target == "" {
			return invalid()
		}
	}
	expectedMembers := 0
	switch {
	case record.Name == "unaccent" && record.Version == "1.1":
		expectedMembers = 5
	case record.Name == "intarray" && record.Version == "1.5":
		expectedMembers = 3
	case record.Name == "fuzzystrmatch" && record.Version == "1.2":
		expectedMembers = len(fuzzystrmatchMembers)
	case record.Name == "intagg" && record.Version == "1.1":
		expectedMembers = len(intaggMembers)
	case record.Name == "pgcrypto" && record.Version == "1.4":
		expectedMembers = len(pgcryptoMembers)
	case record.Name == "uuid-ossp" && record.Version == "1.1":
		expectedMembers = 1
	default:
		return invalid()
	}
	var members []json.RawMessage
	if err := json.Unmarshal(fields["members"], &members); err != nil || len(members) != expectedMembers {
		return invalid()
	}
	seen := map[string]bool{}
	for _, data := range members {
		fields, err := extensionOwnerFields(data, "kind", "name", "signature", "owner")
		if err != nil {
			return invalid()
		}
		var member extensionMemberOwner
		for key, target := range map[string]*string{"kind": &member.Kind, "name": &member.Name, "signature": &member.Signature, "owner": &member.Owner} {
			if err := json.Unmarshal(fields[key], target); err != nil {
				return invalid()
			}
		}
		identity := member.Kind + ":" + member.Name + ":" + member.Signature
		if member.Name == "" || member.Owner == "" || seen[identity] {
			return invalid()
		}
		seen[identity] = true
		switch member.Kind {
		case "procedure":
			valid := false
			switch record.Name {
			case "unaccent":
				valid = member.Name == "unaccent" && (member.Signature == string(unaccentTextSignature) || member.Signature == string(unaccentDictionarySignature)) || member.Name == "unaccent_init" && member.Signature == string(unaccentInitSignature) || member.Name == "unaccent_lexize" && member.Signature == string(unaccentLexizeSignature)
			case "intarray":
				valid = member.Name == "bqarr_in" && member.Signature == "2275" || member.Name == "bqarr_out" && member.Signature == "query_int"
			case "intagg":
				for _, selected := range intaggMembers {
					if member.Name == selected.name && member.Signature == string(selected.signature) {
						valid = true
						break
					}
				}
			case "pgcrypto":
				_, valid = pgcryptoLookup(member.Name, procedureSignature(member.Signature))
			case "uuid-ossp":
				valid = member.Name == "uuid_generate_v4" && member.Signature == ""
			}
			if record.Name == "fuzzystrmatch" {
				valid = fuzzystrmatchResult(member.Name, procedureSignature(member.Signature)) != 0
			}
			if !valid {
				return invalid()
			}
		case "dictionary":
			if record.Name != "unaccent" || member.Name != "unaccent" || member.Signature != "" {
				return invalid()
			}
		case "type":
			if record.Name != "intarray" || member.Name != "query_int" || member.Signature != "" {
				return invalid()
			}
		default:
			return invalid()
		}
		record.Members = append(record.Members, member)
	}
	return record, true, nil
}

// Apply only to Restore's isolated catalog. Named target identities were
// captured before reading; final replacement fences their remapping again.
func (e *Engine) importExtensionOwners(ctx context.Context, op *Operation, records []extensionOwnerRecord) error {
	if len(records) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.importTargets == nil {
		return fmt.Errorf("stuntgres: invalid extension owner import authority")
	}
	seen := map[ids.ObjectAddress]bool{}
	candidate := cloneCatalog(e.catalog.state)
	roles := e.roleRead(CatalogRead{e, candidate}, e.roles)
	ownersChanged := false
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		a := candidate.names[ids.NameKey{ClassID: ids.ExtensionClassID, Name: record.Name}]
		ext, ok := candidate.descriptors[a].(*extensionDescriptor)
		if !ok || seen[a] || ext.version != record.Version {
			return fmt.Errorf("stuntgres: invalid extension owner import target")
		}
		seen[a] = true
		ns, ok := candidate.descriptors[ext.targetNamespace].(*namespaceDescriptor)
		if !ok || ns.name != record.Schema {
			return fmt.Errorf("stuntgres: mismatching extension owner namespace")
		}
		owner, ok := roles.Lookup(record.Owner)
		if !ok || owner.Ref != ext.owner {
			return fmt.Errorf("stuntgres: mismatching extension installation owner")
		}
		for _, member := range record.Members {
			owner, ok := roles.Lookup(member.Owner)
			if !ok {
				return fmt.Errorf("stuntgres: extension member owner %q does not exist", member.Owner)
			}
			class := ids.ProcedureClassID
			if member.Kind == "dictionary" {
				class = ids.TSDictionaryClassID
			}
			if member.Kind == "type" {
				class = ids.TypeClassID
			}
			signature := member.Signature
			if record.Name == "intarray" && member.Kind == "procedure" && member.Name == "bqarr_out" {
				base, ok := candidate.descriptors[ext.members[0]].(*typeDescriptor)
				if !ok || !base.typ.IsQueryInt() {
					return fmt.Errorf("stuntgres: missing intarray owner type")
				}
				signature = string(queryIntSignature(base.typ.ID()))
			}
			a := candidate.names[ids.NameKey{ClassID: class, Namespace: ext.targetNamespace, Name: member.Name, Signature: signature}]
			d := candidate.descriptors[a]
			if d == nil || extensionMembership(d) != ext.address {
				return fmt.Errorf("stuntgres: invalid extension ownership member")
			}
			switch d := d.(type) {
			case *typeDescriptor:
				if d.owner != owner.Ref {
					n := *d
					n.owner = owner.Ref
					candidate.descriptors[a] = &n
					arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.array)}
					array, ok := candidate.descriptors[arrayAddress].(*typeDescriptor)
					if !ok || !array.typ.IsArray() || array.typ.Element() != d.typ {
						return fmt.Errorf("stuntgres: invalid owned intarray array")
					}
					mapped := *array
					mapped.owner = owner.Ref
					candidate.descriptors[arrayAddress] = &mapped
					ownersChanged = true
				}
			case *procedureDescriptor:
				if d.owner != owner.Ref {
					n := *d
					n.owner = owner.Ref
					candidate.descriptors[a] = &n
					ownersChanged = true
				}
			case *tsDictionaryDescriptor:
				if d.owner != owner.Ref {
					n := *d
					n.owner = owner.Ref
					candidate.descriptors[a] = &n
					ownersChanged = true
				}
			default:
				return fmt.Errorf("stuntgres: ownerless extension metadata member")
			}
		}
	}
	if !ownersChanged {
		return nil
	}
	// Ownership changes replace provider generations even when all SQL types are
	// unchanged. Refresh stored CHECK/view/policy/default captures through the
	// ordinary catalog expression remapper, not its type-only rename fast path.
	if err := e.reownCatalogExpressions(candidate, nil, false); err != nil {
		return err
	}
	e.catalog.state = candidate
	return nil
}
