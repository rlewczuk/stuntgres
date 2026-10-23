package extension

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

const OwnersMarker = "stuntgres-extension-owners-v1"

type MemberOwner struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Signature string `json:"signature"`
	Owner     string `json:"owner"`
}
type OwnerRecord struct {
	Name    string        `json:"name"`
	Schema  string        `json:"schema"`
	Version string        `json:"version"`
	Owner   string        `json:"owner"`
	Members []MemberOwner `json:"members"`
}

func OwnerMetadata(view catalog.Read, roles authorization.Read, ext *catalog.Extension) (string, error) {
	owner, err := roles.Descriptor(ext.Owner)
	if err != nil {
		return "", err
	}
	ns, ok := view.Namespace(ext.TargetNamespace)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing extension target namespace")
	}
	record := OwnerRecord{Name: ext.Name(), Schema: ns.Name(), Version: ext.Version, Owner: owner.Name}
	if ext.Name() == "earthdistance" {
		if err := StockEarthdistance.ValidateExtension(view, ext); err != nil {
			return "", err
		}
	}
	if ext.Name() == "cube" {
		if err := StockCube.ValidateExtension(view, ext); err != nil {
			return "", err
		}
	}
	for _, a := range ext.Members {
		d := view.Get(a)
		var member MemberOwner
		var principal authorization.PrincipalRef
		switch d := d.(type) {
		case *catalog.Procedure:
			member = MemberOwner{Kind: "procedure", Name: d.Name(), Signature: string(d.Signature)}
			if IntarrayProviderOf(d) != nil && d.Name() == "bqarr_out" {
				member.Signature = "query_int"
			}
			if CubeProviderOf(d) != nil {
				base, ok := view.Type(ext.Members[0])
				if !ok {
					return "", fmt.Errorf("stuntgres: missing cube ownership type")
				}
				parts := strings.Split(member.Signature, ",")
				for i, part := range parts {
					if part == string(CubeSignature(base.SQLType.ID())) {
						parts[i] = "cube"
					}
				}
				member.Signature = strings.Join(parts, ",")
			}
			if CitextProviderOf(d) != nil && d.Name() == "citextout" {
				member.Signature = "citext"
			}
			principal = d.Owner
		case *catalog.Operator:
			known := cubeSpatialOwnerName(d.Name(), true) || cubeCoordinateOwnerName(d.Name())
			for _, name := range cubeOperatorNames {
				known = known || d.Name() == name
			}
			if ext.Name() != "cube" || !known || d.Extension != ext.Address() {
				return "", fmt.Errorf("stuntgres: invalid extension operator owner")
			}
			member = MemberOwner{Kind: "operator", Name: d.Name(), Signature: "cube,cube"}
			if cubeCoordinateOwnerName(d.Name()) {
				member.Signature = "cube,23"
			}
			principal = d.Owner
		case *catalog.OperatorClass:
			if d.Method != 403 {
				return "", fmt.Errorf("stuntgres: unsupported extension class method")
			}
			member = MemberOwner{Kind: "operator_class", Name: d.Name(), Signature: "btree"}
			principal = d.Owner
		case *catalog.OperatorFamily:
			if d.Method != 403 {
				return "", fmt.Errorf("stuntgres: unsupported extension family method")
			}
			member = MemberOwner{Kind: "operator_family", Name: d.Name(), Signature: "btree"}
			principal = d.Owner
		case *catalog.TSDictionary:
			member = MemberOwner{Kind: "dictionary", Name: d.Name()}
			principal = d.Owner
		case *catalog.Type:
			roleName, err := MemberTypeRole(view, ext, d)
			if err != nil {
				return "", err
			}
			member = MemberOwner{Kind: "type", Name: roleName}
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
	return "-- " + OwnersMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
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
		expectedMembers = CubeMemberCount
	case record.Name == "citext" && record.Version == "1.8":
		expectedMembers = 3
	case record.Name == "earthdistance" && record.Version == "1.2":
		expectedMembers = 2
	case record.Name == "fuzzystrmatch" && record.Version == "1.2":
		expectedMembers = len(FuzzystrmatchMembers)
	case record.Name == "fuzzystrmatch" && record.Version == "1.1":
		expectedMembers = len(FuzzystrmatchMembers) - 1
	case record.Name == "intagg" && record.Version == "1.1":
		expectedMembers = len(IntaggMembers)
	case record.Name == "pgrowlocks" && record.Version == "1.2":
		expectedMembers = 1
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
		var member MemberOwner
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
				valid = cubeSpatialOwnerName(member.Name, false) && member.Signature == "cube,cube"
				for _, spec := range cubeRoutines {
					if member.Name == spec.name && member.Signature == spec.ownerSignature() {
						valid = true
						break
					}
				}
			case "citext":
				valid = member.Name == "citextin" && member.Signature == "2275" || member.Name == "citextout" && member.Signature == "citext"
			case "pgrowlocks":
				valid = member.Name == "pgrowlocks" && member.Signature == "25"
			case "intagg":
				for _, selected := range IntaggMembers {
					if member.Name == selected.Name && member.Signature == string(selected.Signature) {
						valid = true
						break
					}
				}
			case "pgcrypto":
				_, valid = functions.PgcryptoLookup(member.Name, member.Signature)
			case "uuid-ossp":
				memberSpec, selected := functions.UUIDOsspLookup(member.Name)
				valid = selected && member.Signature == memberSpec.Signature
			}
			if record.Name == "fuzzystrmatch" {
				valid = fuzzystrmatchResult(member.Name, catalog.ProcedureSignature(member.Signature)) != 0 && (record.Version != "1.1" || member.Name != "daitch_mokotoff")
			}
			if !valid {
				return invalid()
			}
		case "operator":
			valid := cubeSpatialOwnerName(member.Name, true)
			for _, name := range cubeOperatorNames {
				valid = valid || member.Name == name
			}
			valid = valid && member.Signature == "cube,cube" || cubeCoordinateOwnerName(member.Name) && member.Signature == "cube,23"
			if record.Name != "cube" || !valid {
				return invalid()
			}
		case "operator_class", "operator_family":
			if record.Name != "cube" || member.Name != "cube_ops" || member.Signature != "btree" {
				return invalid()
			}
		case "dictionary":
			if record.Name != "unaccent" || member.Name != "unaccent" || member.Signature != "" {
				return invalid()
			}
		case "type":
			if (record.Name != "intarray" || member.Name != "query_int") && (record.Name != "cube" || member.Name != "cube") && (record.Name != "citext" || member.Name != "citext") && (record.Name != "earthdistance" || member.Name != "earth" && member.Name != "earth[]") || member.Signature != "" {
				return invalid()
			}
		default:
			return invalid()
		}
		record.Members = append(record.Members, member)
	}
	if record.Name == "earthdistance" && record.Members[0].Owner != record.Members[1].Owner {
		return invalid()
	}
	return record, true, nil
}
