package engine

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
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

func extensionOwnerMetadata(view catalog.Read, roles authorization.Read, ext *catalog.Extension) (string, error) {
	owner, err := roles.Descriptor(ext.Owner)
	if err != nil {
		return "", err
	}
	ns, ok := view.Namespace(ext.TargetNamespace)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing extension target namespace")
	}
	record := extensionOwnerRecord{Name: ext.Name(), Schema: ns.Name(), Version: ext.Version, Owner: owner.Name}
	for _, a := range ext.Members {
		d := view.Get(a)
		var member extensionMemberOwner
		var principal authorization.PrincipalRef
		switch d := d.(type) {
		case *catalog.Procedure:
			member = extensionMemberOwner{Kind: "procedure", Name: d.Name(), Signature: string(d.Signature)}
			if intarrayProviderOf(d) != nil && d.Name() == "bqarr_out" {
				member.Signature = "query_int"
			}
			if cubeProviderOf(d) != nil && d.Name() == "cube_out" {
				member.Signature = "cube"
			}
			principal = d.Owner
		case *catalog.TSDictionary:
			member = extensionMemberOwner{Kind: "dictionary", Name: d.Name()}
			principal = d.Owner
		case *catalog.Type:
			member = extensionMemberOwner{Kind: "type", Name: d.Name()}
			principal = d.Owner
		case *catalog.TSTemplate:
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
	case record.Name == "cube" && record.Version == "1.5":
		expectedMembers = 3
	case record.Name == "fuzzystrmatch" && record.Version == "1.2":
		expectedMembers = len(fuzzystrmatchMembers)
	case record.Name == "fuzzystrmatch" && record.Version == "1.1":
		expectedMembers = len(fuzzystrmatchMembers) - 1
	case record.Name == "intagg" && record.Version == "1.1":
		expectedMembers = len(intaggMembers)
	case record.Name == "pgcrypto" && record.Version == "1.4":
		expectedMembers = len(functions.PgcryptoMembers)
	case record.Name == "uuid-ossp" && record.Version == "1.1":
		expectedMembers = len(functions.UUIDOsspMembers)
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
				valid = member.Name == "unaccent" && (member.Signature == string(catalog.UnaccentTextSignature) || member.Signature == string(catalog.UnaccentDictionarySignature)) || member.Name == "unaccent_init" && member.Signature == string(catalog.UnaccentInitSignature) || member.Name == "unaccent_lexize" && member.Signature == string(catalog.UnaccentLexizeSignature)
			case "intarray":
				valid = member.Name == "bqarr_in" && member.Signature == "2275" || member.Name == "bqarr_out" && member.Signature == "query_int"
			case "cube":
				valid = member.Name == "cube_in" && member.Signature == "2275" || member.Name == "cube_out" && member.Signature == "cube"
			case "intagg":
				for _, selected := range intaggMembers {
					if member.Name == selected.name && member.Signature == string(selected.signature) {
						valid = true
						break
					}
				}
			case "pgcrypto":
				_, valid = functions.PgcryptoLookup(member.Name, member.Signature)
			case "uuid-ossp":
				_, _, selected := functions.UUIDOsspLookup(member.Name)
				valid = selected && member.Signature == ""
			}
			if record.Name == "fuzzystrmatch" {
				valid = fuzzystrmatchResult(member.Name, catalog.ProcedureSignature(member.Signature)) != 0 && (record.Version != "1.1" || member.Name != "daitch_mokotoff")
			}
			if !valid {
				return invalid()
			}
		case "dictionary":
			if record.Name != "unaccent" || member.Name != "unaccent" || member.Signature != "" {
				return invalid()
			}
		case "type":
			if (record.Name != "intarray" || member.Name != "query_int") && (record.Name != "cube" || member.Name != "cube") || member.Signature != "" {
				return invalid()
			}
		default:
			return invalid()
		}
		record.Members = append(record.Members, member)
	}
	return record, true, nil
}
