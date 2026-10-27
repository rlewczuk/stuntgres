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

const OwnersMarker = "stuntgres-extension-owners-v2"

type MemberOwner struct {
	Slot      int        `json:"slot"`
	Schema    string     `json:"schema"`
	Kind      string     `json:"kind"`
	Name      string     `json:"name"`
	Signature string     `json:"signature"`
	Owner     string     `json:"owner"`
	ACL       *MemberACL `json:"acl,omitempty"`
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
	if ext.Name() == "hstore" {
		if err := StockHstore.ValidateExtension(view, ext); err != nil {
			return "", err
		}
	}
	for slot, a := range ext.Members {
		d := view.Get(a)
		var member MemberOwner
		var principal authorization.PrincipalRef
		switch d := d.(type) {
		case *catalog.Procedure:
			symbol, err := ProcedureSymbol(view, d)
			if err != nil {
				return "", err
			}
			member = MemberOwner{Kind: "procedure", Name: d.Name(), Signature: string(d.Signature)}
			if IntarrayProviderOf(d) != nil && symbol == "bqarr_out" {
				member.Signature = "query_int"
			}
			if SegProviderOf(d) != nil && symbol == "seg_out" {
				member.Signature = "seg"
			}
			if LtreeProviderOf(d) != nil && slot%3 == 2 {
				member.Signature = ltreeRecipes[slot/3].name
			}
			if HstoreProviderOf(d) != nil || CubeProviderOf(d) != nil || EarthdistanceProviderOf(d) != nil || CitextProviderOf(d) != nil || IsnProviderOf(d) != nil {
				var valid bool
				member.Signature, valid = ownerProcedureSignature(ext.Name(), ext.Version, slot)
				if !valid {
					return "", fmt.Errorf("stuntgres: invalid cube ownership signature")
				}
			}
			principal = d.Owner
			if d.ACL.Explicit || len(d.ACL.Entries) != 0 || AmcheckProviderOf(d) != nil {
				member.ACL, err = captureMemberACL(roles, d.ACL)
				if err != nil {
					return "", err
				}
			}
		case *catalog.Operator:
			name, signature, known := ownerOperatorIdentity(ext.Name(), slot)
			if !known || d.Name() != name || d.Extension != ext.Address() {
				return "", fmt.Errorf("stuntgres: invalid extension operator owner")
			}
			member = MemberOwner{Kind: "operator", Name: d.Name(), Signature: signature}
			principal = d.Owner
		case *catalog.OperatorClass:
			if d.Method != 403 && !(ext.Name() == "hstore" && d.Method == 405) {
				return "", fmt.Errorf("stuntgres: unsupported extension class method")
			}
			member = MemberOwner{Kind: "operator_class", Name: d.Name(), Signature: "btree"}
			if d.Method == 405 {
				member.Signature = "hash"
			}
			principal = d.Owner
		case *catalog.OperatorFamily:
			if d.Method != 403 && !(ext.Name() == "hstore" && d.Method == 405) {
				return "", fmt.Errorf("stuntgres: unsupported extension family method")
			}
			member = MemberOwner{Kind: "operator_family", Name: d.Name(), Signature: "btree"}
			if d.Method == 405 {
				member.Signature = "hash"
			}
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
		case *catalog.TSTemplate, *catalog.Cast:
			continue
		default:
			return "", fmt.Errorf("stuntgres: missing extension owner member")
		}
		member.Slot = slot
		namespace, ok := view.Namespace(d.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing member namespace")
		}
		member.Schema = namespace.Name()
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
		allowed := false
		for _, name := range keys {
			allowed = allowed || name == key
		}
		for _, name := range optional {
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
	case record.Name == "tablefunc" && record.Version == "1.0":
		expectedMembers = tablefuncMemberCount
	case record.Name == "unaccent" && record.Version == "1.1":
		expectedMembers = 5
	case record.Name == "hstore" && record.Version == "1.8":
		expectedMembers = HstoreMemberCount - 1 // The cast has no owner or ACL.
	case record.Name == "seg" && record.Version == "1.4":
		expectedMembers = 3
	case record.Name == "intarray" && record.Version == "1.5":
		expectedMembers = 3
	case record.Name == "ltree" && record.Version == "1.3":
		expectedMembers = LtreeMemberCount
	case record.Name == "cube" && record.Version == "1.5":
		expectedMembers = CubeMemberCount
	case record.Name == "citext" && record.Version == "1.8":
		expectedMembers = CitextMemberCount
	case record.Name == "isn" && record.Version == "1.3":
		expectedMembers = isnMemberCount
	case record.Name == "earthdistance" && record.Version == "1.2":
		expectedMembers = EarthMemberCount
	case record.Name == "lo" && record.Version == "1.2":
		expectedMembers = 2
	case record.Name == "fuzzystrmatch" && record.Version == "1.2":
		expectedMembers = len(FuzzystrmatchMembers)
	case record.Name == "fuzzystrmatch" && record.Version == "1.1":
		expectedMembers = len(FuzzystrmatchMembers) - 1
	case record.Name == "intagg" && record.Version == "1.1":
		expectedMembers = len(IntaggMembers)
	case record.Name == "amcheck" && record.Version == "1.5":
		expectedMembers = 1 + len(AmcheckMembers)
	case record.Name == "pageinspect" && record.Version == "1.13":
		expectedMembers = len(PageinspectMembers)
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
		fields, err := extensionOwnerOptionalFields(data, []string{"acl"}, "slot", "schema", "kind", "name", "signature", "owner")
		if err != nil {
			return invalid()
		}
		var member MemberOwner
		if err := json.Unmarshal(fields["slot"], &member.Slot); err != nil {
			return invalid()
		}
		for key, target := range map[string]*string{"schema": &member.Schema, "kind": &member.Kind, "name": &member.Name, "signature": &member.Signature, "owner": &member.Owner} {
			if err := json.Unmarshal(fields[key], target); err != nil {
				return invalid()
			}
		}
		if data, present := fields["acl"]; present {
			if member.Kind != "procedure" {
				return invalid()
			}
			member.ACL, err = decodeMemberACL(data)
			if err != nil {
				return invalid()
			}
		}
		if record.Name == "amcheck" && member.Kind == "procedure" && member.ACL == nil {
			return invalid()
		}
		identity := fmt.Sprint(member.Slot)
		slots := expectedMembers
		if record.Name == "unaccent" || record.Name == "hstore" {
			slots++
		}
		if member.Slot < 0 || member.Slot >= slots || member.Schema == "" || member.Name == "" || member.Owner == "" || seen[identity] {
			return invalid()
		}
		seen[identity] = true
		if record.Name == "tablefunc" {
			if !validTablefuncOwnerMember(member) {
				return invalid()
			}
			record.Members = append(record.Members, member)
			continue
		}
		switch member.Kind {
		case "procedure":
			// Mutable procedure labels are checked against the current installed
			// slot by ImportOwners, not used as installation identity.
			if record.Name == "lo" || record.Name == "earthdistance" && member.Slot < 2 || record.Name == "unaccent" && member.Slot > 3 || (record.Name == "seg" || record.Name == "hstore" || record.Name == "intarray" || record.Name == "ltree" || record.Name == "cube" || record.Name == "citext" || record.Name == "isn") && member.Slot == 0 {
				return invalid()
			}
			if signature, ok := ownerProcedureSignature(record.Name, record.Version, member.Slot); !ok || signature != member.Signature {
				return invalid()
			}
		case "operator":
			name, signature, valid := ownerOperatorIdentity(record.Name, member.Slot)
			if !valid || member.Name != name || member.Signature != signature {
				return invalid()
			}
		case "operator_class", "operator_family":
			slot := 16
			if member.Kind == "operator_class" {
				slot = 17
			}
			valid := record.Name == "cube" && member.Slot == slot && member.Name == "cube_ops" && member.Signature == "btree"
			if record.Name == "hstore" {
				class := member.Kind == "operator_class"
				valid = member.Name == "btree_hstore_ops" && member.Signature == "btree" && ((!class && member.Slot == HstoreBtreeFamilySlot) || (class && member.Slot == HstoreBtreeClassSlot)) || member.Name == "hash_hstore_ops" && member.Signature == "hash" && ((!class && member.Slot == HstoreHashFamilySlot) || (class && member.Slot == HstoreHashClassSlot))
			}
			if !valid {
				return invalid()
			}
		case "dictionary":
			// ImportOwners checks mutable labels against the current genuine slot.
			if record.Name != "unaccent" || member.Slot != 5 || member.Signature != "" {
				return invalid()
			}
		case "type":
			if (record.Name != "seg" || member.Name != "seg") && (record.Name != "hstore" || member.Name != "hstore") && (record.Name != "intarray" || member.Name != "query_int") && (record.Name != "ltree" || member.Name != "lquery" && member.Name != "ltree" && member.Name != "ltxtquery") && (record.Name != "cube" || member.Name != "cube") && (record.Name != "citext" || member.Name != "citext") && (record.Name != "isn" || member.Name != "ean13" && member.Name != "isbn" && member.Name != "isbn13" && member.Name != "ismn" && member.Name != "ismn13" && member.Name != "issn" && member.Name != "issn13" && member.Name != "upc") && (record.Name != "earthdistance" || member.Name != "earth" && member.Name != "earth[]") && (record.Name != "lo" || member.Name != "lo" && member.Name != "lo[]") || member.Signature != "" {
				return invalid()
			}
			if record.Name == "isn" {
				valid := false
				for _, recipe := range isnRecipes {
					if member.Slot == recipe.slot && member.Name == recipe.name {
						valid = true
					}
				}
				if !valid {
					return invalid()
				}
			}
			if record.Name == "ltree" && !((member.Slot == 0 && member.Name == "lquery") || (member.Slot == 3 && member.Name == "ltree") || (member.Slot == 6 && member.Name == "ltxtquery")) {
				return invalid()
			}
			if (record.Name == "seg" || record.Name == "citext" || record.Name == "hstore") && member.Slot != 0 {
				return invalid()
			}
			if record.Name == "lo" && member.Name != [...]string{"lo", "lo[]"}[member.Slot] {
				return invalid()
			}
		default:
			return invalid()
		}
		record.Members = append(record.Members, member)
	}
	if record.Name == "tablefunc" && !validTablefuncOwnerRoles(record) {
		return invalid()
	}
	if record.Name == "earthdistance" && !validEarthOwnerRoles(record) || record.Name == "lo" && record.Members[0].Owner != record.Members[1].Owner {
		return invalid()
	}
	return record, true, nil
}

// The codec admits mutable labels but never mutable executable declarations.
func ownerProcedureSignature(name, version string, slot int) (string, bool) {
	switch name {
	case "tablefunc":
		if version == "1.0" {
			if slot >= 0 && slot < tablefuncCrosstabStart {
				return string(tablefuncMembers[slot].signature), true
			}
			switch slot - tablefuncCrosstabStart {
			case 0, 7, 8, 9:
				return "25", true
			case 10:
				return "25,23", true
			}
		}
	case "earthdistance":
		if version != "1.2" || slot < 2 || slot >= EarthPointOperatorSlot {
			return "", false
		}
		switch slot {
		case EarthPointProcedureSlot:
			return "600,600", true
		case 2:
			return "", true
		case 5:
			return "701,701", true
		case 6, 7:
			return "earth", true
		default:
			return "701", true
		}
	case "pgcrypto":
		if slot < len(functions.PgcryptoMembers) {
			return functions.PgcryptoMembers[slot].Signature, true
		}
	case "uuid-ossp":
		if slot < len(functions.UUIDOsspMembers) {
			return functions.UUIDOsspMembers[slot].Signature, true
		}
	case "intagg":
		if slot < len(IntaggMembers) {
			return string(IntaggMembers[slot].Signature), true
		}
	case "fuzzystrmatch":
		if version == "1.1" {
			slot++
		}
		if slot < len(FuzzystrmatchMembers) {
			return string(FuzzystrmatchMembers[slot].Signature), true
		}
	case "amcheck":
		if slot == 0 {
			return string(VerifyHeapamSignature), true
		}
		if slot > 0 && slot <= len(AmcheckMembers) {
			return string(AmcheckMembers[slot-1].Signature), true
		}
	case "pageinspect":
		if version == "1.13" && slot >= 0 && slot < len(PageinspectMembers) {
			return string(PageinspectMembers[slot].Signature), true
		}
	case "pgrowlocks":
		return "25", slot == 0
	case "unaccent":
		signatures := [...]catalog.ProcedureSignature{catalog.UnaccentDictionarySignature, catalog.UnaccentTextSignature, catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}
		if slot < len(signatures) {
			return string(signatures[slot]), true
		}
	case "cube":
		if version != StockCube.control.Version {
			return "", false
		}
		if spec, ok := cubeRoutineForMember(slot); ok {
			return spec.ownerSignature(), true
		}
		if slot >= cubeSpatialProcedureStart && slot < cubeSpatialOperatorStart {
			return "cube,cube", true
		}
	case "isn":
		for _, recipe := range isnRecipes {
			if slot == recipe.slot+1 {
				return "2275", true
			}
			if slot == recipe.slot+2 {
				return recipe.name, true
			}
		}
	case "ltree":
		for _, recipe := range ltreeRecipes {
			if slot == recipe.slot+1 {
				return "2275", true
			}
			if slot == recipe.slot+2 {
				return recipe.name, true
			}
		}
	case "seg", "hstore", "intarray", "citext":
		if name == "hstore" {
			if recipe, ok := hstoreRoutineAt(slot); ok {
				return recipe.signature, true
			}
			return "", false
		}
		if name == "citext" && slot >= CitextComparisonProcedureStart && slot < CitextComparisonOperatorStart {
			return "citext,citext", true
		}
		if name == "citext" && slot >= CitextConversionProcedureStart && slot < CitextPatternProcedureStart {
			return []string{"1042", "16", "869"}[slot-CitextConversionProcedureStart], true
		}
		if name == "citext" && slot >= CitextPatternProcedureStart && slot < CitextPatternOperatorStart {
			return [2]string{"citext,citext", "citext,25"}[(slot-CitextPatternProcedureStart)%2], true
		}
		if name == "citext" && slot >= CitextRegexpProcedureStart && slot < CitextRegexpProcedureEnd {
			m := CitextRegexpMembers[slot-CitextRegexpProcedureStart]
			signature := "citext,citext"
			for i := 2; i < m.ArgCount; i++ {
				signature += ",25"
			}
			return signature, true
		}
		if name == "citext" && slot >= CitextStringProcedureStart && slot < CitextMemberCount {
			return CitextStringMembers[slot-CitextStringProcedureStart].OwnerSignature(), true
		}
		if slot == 1 {
			return "2275", true
		}
		if slot == 2 {
			if name == "intarray" {
				return "query_int", true
			}
			return name, true
		}
	}
	return "", false
}

func cubeOwnerOperatorIdentity(slot int) (string, string, bool) {
	if slot >= 10 && slot < 10+len(cubeOperatorNames) {
		return cubeOperatorNames[slot-10], "cube,cube", true
	}
	if slot >= cubeCoordinateOperatorStart && slot < cubeSpatialProcedureStart {
		return cubeCoordinateOperators[slot-cubeCoordinateOperatorStart].name, "cube,23", true
	}
	if slot >= cubeSpatialOperatorStart && slot < CubeMemberCount {
		return cubeSpatialMembers[slot-cubeSpatialOperatorStart].operator, "cube,cube", true
	}
	return "", "", false
}

func ownerOperatorIdentity(extension string, slot int) (string, string, bool) {
	switch extension {
	case "hstore":
		if slot >= HstoreComparisonOperatorStart && slot < HstoreBtreeFamilySlot {
			return hstoreComparisonOperators[slot-HstoreComparisonOperatorStart], "hstore,hstore", true
		}
		if slot == HstoreArrayOperatorSlot || slot == HstoreMatrixOperatorSlot {
			return []string{"%%", "%#"}[slot-HstoreArrayOperatorSlot], "0,hstore", true
		}
		if slot == HstoreFetchOperatorSlot {
			return "->", "hstore,25", true
		}
		if slot == HstoreSliceOperatorSlot {
			return "->", "hstore,1009", true
		}
		for _, recipe := range hstoreOperators {
			if recipe.slot == slot {
				signature := "hstore," + string(HstoreSignature(recipe.right))
				if recipe.rightHstore {
					signature = "hstore,hstore"
				}
				return recipe.name, signature, true
			}
		}
	case "cube":
		return cubeOwnerOperatorIdentity(slot)
	case "citext":
		if slot >= CitextComparisonOperatorStart && slot < CitextConversionProcedureStart {
			return citextOperatorNames[slot-CitextComparisonOperatorStart], "citext,citext", true
		}
		if slot >= CitextPatternOperatorStart && slot < CitextPatternOperatorEnd {
			index := slot - CitextPatternOperatorStart
			return citextPatternOperators[index%8], [2]string{"citext,citext", "citext,25"}[index/8], true
		}
	case "earthdistance":
		if slot == EarthPointOperatorSlot {
			return "<@>", "600,600", true
		}
	}
	return "", "", false
}
