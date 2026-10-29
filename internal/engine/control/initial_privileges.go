package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

const InitialPrivilegesMarker = "stuntgres-initial-privileges-v1"

type InitialPrivilegeIdentity struct {
	Class     ids.OID `json:"class"`
	Kind      string  `json:"kind"`
	Schema    string  `json:"schema"`
	Name      string  `json:"name"`
	Signature string  `json:"signature"`
}

type InitialPrivilegeMetadataRecord struct {
	InitialPrivilegeIdentity
	SubID    int32                   `json:"subid"`
	PrivType string                  `json:"privtype"`
	Entries  []extension.MemberGrant `json:"entries"`
}

// Complete is replacement authority, including the absence of bootstrap rows.
// No source-domain handles or allocated object OIDs cross this boundary.
type InitialPrivilegesMetadata struct {
	Complete bool                             `json:"complete"`
	Records  []InitialPrivilegeMetadataRecord `json:"records"`
}

func initialPrivilegesError() error {
	return fmt.Errorf("stuntgres: invalid initial privileges metadata")
}

func initialPrivilegeMetadataMask(r InitialPrivilegeMetadataRecord) authorization.Privileges {
	if r.SubID < 0 || r.Name == "" {
		return 0
	}
	var mask authorization.Privileges
	switch r.Class {
	case ids.RelationClassID:
		if r.Kind != "relation" || r.Schema == "" || r.Signature != "" {
			return 0
		}
		mask = authorization.TableGrantPrivileges | authorization.UsagePrivilege
	case ids.ProcedureClassID:
		if r.Kind != "procedure" || r.Schema == "" {
			return 0
		}
		mask = authorization.ExecutePrivilege
	case ids.TypeClassID:
		if r.Kind != "type" || r.Schema == "" || r.Signature != "" {
			return 0
		}
		mask = authorization.UsagePrivilege
	case ids.NamespaceClassID:
		if r.Kind != "schema" || r.Schema != "" || r.Signature != "" {
			return 0
		}
		mask = authorization.UsagePrivilege | authorization.CreatePrivilege
	case ids.LanguageClassID:
		if r.Kind != "language" || r.Schema != "" || r.Signature != "" {
			return 0
		}
		mask = authorization.UsagePrivilege
	case ids.ForeignWrapperClassID, ids.ForeignServerClassID:
		kind := "foreign_wrapper"
		if r.Class == ids.ForeignServerClassID {
			kind = "foreign_server"
		}
		if r.Kind != kind || r.Schema != "" || r.Signature != "" {
			return 0
		}
		mask = authorization.UsagePrivilege
	case ids.LargeObjectClassID:
		if r.Kind != "largeobject" || r.Schema != "" || r.Signature != "" {
			return 0
		}
		oid, err := strconv.ParseUint(r.Name, 10, 32)
		if err != nil || oid == 0 || strconv.FormatUint(oid, 10) != r.Name {
			return 0
		}
		mask = authorization.SelectPrivilege | authorization.UpdatePrivilege
	default:
		return 0
	}
	if r.SubID != 0 {
		if r.Class != ids.RelationClassID {
			return 0
		}
		mask = authorization.ColumnGrantPrivileges
	}
	return mask
}

func initialPrivilegeIdentity(view catalog.Read, d catalog.Descriptor) (InitialPrivilegeIdentity, error) {
	if d.Address().ClassID == ids.LargeObjectClassID {
		return InitialPrivilegeIdentity{Class: d.Address().ClassID, Kind: "largeobject", Name: strconv.FormatUint(uint64(d.Address().ObjectID), 10)}, nil
	}
	m, err := extension.OwnerIdentity(view, d)
	return InitialPrivilegeIdentity{Class: d.Address().ClassID, Kind: m.Kind, Schema: m.Schema, Name: m.Name, Signature: m.Signature}, err
}

// CaptureInitialPrivileges runs under the same coherent read as the SQL dump.
// Validate all current history, returning nil only for exact dump-visible seed
// equality. Resolve every emitted principal before writer I/O.
func CaptureInitialPrivileges(ctx context.Context, view catalog.Read, roles authorization.Read) (*InitialPrivilegesMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := catalog.ValidateInitialPrivileges(roles, view); err != nil {
		return nil, err
	}
	if InitialPrivilegesMatchBaseline(view) {
		return nil, ctx.Err()
	}
	out := &InitialPrivilegesMetadata{Complete: true, Records: make([]InitialPrivilegeMetadataRecord, 0, view.State().InitialPrivilegeCount())}
	for address, record := range view.State().InitialPrivileges() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		object := address
		object.SubID = 0
		d := view.Get(object)
		if d == nil {
			return nil, initialPrivilegesError()
		}
		if temporaryDumpObject(view.State(), d) {
			continue
		}
		identity, err := initialPrivilegeIdentity(view, d)
		if err != nil {
			return nil, err
		}
		r := InitialPrivilegeMetadataRecord{InitialPrivilegeIdentity: identity, SubID: address.SubID, PrivType: string(record.PrivType), Entries: make([]extension.MemberGrant, len(record.ACL.Entries))}
		for i, entry := range record.ACL.Entries {
			grantor, err := roles.Descriptor(entry.Grantor)
			if err != nil {
				return nil, err
			}
			g := extension.MemberGrant{Grantor: grantor.Name, Public: entry.Grantee.Public, Privileges: entry.Privileges, GrantOptions: entry.GrantOptions}
			if !g.Public {
				grantee, err := roles.Descriptor(entry.Grantee.Role)
				if err != nil {
					return nil, err
				}
				g.Grantee = grantee.Name
			}
			r.Entries[i] = g
		}
		out.Records = append(out.Records, r)
	}
	// Sort rows, never ACL entries: ACL array order is historical data.
	slices.SortFunc(out.Records, func(a, b InitialPrivilegeMetadataRecord) int {
		if a.Class < b.Class {
			return -1
		}
		if a.Class > b.Class {
			return 1
		}
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if c := strings.Compare(a.Schema, b.Schema); c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		if c := strings.Compare(a.Signature, b.Signature); c != 0 {
			return c
		}
		if a.SubID < b.SubID {
			return -1
		}
		if a.SubID > b.SubID {
			return 1
		}
		return 0
	})
	return out, ctx.Err()
}

func (m *InitialPrivilegesMetadata) Comment() (string, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return "-- " + InitialPrivilegesMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}

// Decode every object explicitly; encoding/json alone accepts duplicate keys,
// nulls and missing fields, none of which may confer replacement authority.
func initialPrivilegeFields(data []byte, keys ...string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, initialPrivilegesError()
	}
	fields := make(map[string]json.RawMessage, len(keys))
	for d.More() {
		t, err := d.Token()
		key, ok := t.(string)
		if err != nil || !ok || !slices.Contains(keys, key) || fields[key] != nil {
			return nil, initialPrivilegesError()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, initialPrivilegesError()
		}
		fields[key] = value
	}
	t, err = d.Token()
	if err != nil || t != json.Delim('}') || len(fields) != len(keys) {
		return nil, initialPrivilegesError()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, initialPrivilegesError()
	}
	return fields, nil
}

func DecodeInitialPrivileges(comment string) (*InitialPrivilegesMetadata, bool, error) {
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-initial-privileges-") {
		return nil, false, nil
	}
	invalid := func() (*InitialPrivilegesMetadata, bool, error) { return nil, true, initialPrivilegesError() }
	marker, payload, ok := strings.Cut(text, " ")
	if !ok || marker != InitialPrivilegesMarker {
		return invalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || !utf8.Valid(data) {
		return invalid()
	}
	fields, err := initialPrivilegeFields(data, "complete", "records")
	if err != nil {
		return invalid()
	}
	var out InitialPrivilegesMetadata
	if json.Unmarshal(fields["complete"], &out.Complete) != nil || !out.Complete {
		return invalid()
	}
	var records []json.RawMessage
	if json.Unmarshal(fields["records"], &records) != nil || records == nil {
		return invalid()
	}
	out.Records = make([]InitialPrivilegeMetadataRecord, len(records))
	type target struct {
		InitialPrivilegeIdentity
		SubID int32
	}
	seen := make(map[target]bool, len(records))
	for i, data := range records {
		f, err := initialPrivilegeFields(data, "class", "kind", "schema", "name", "signature", "subid", "privtype", "entries")
		if err != nil || json.Unmarshal(data, &out.Records[i]) != nil {
			return invalid()
		}
		r := &out.Records[i]
		key := target{r.InitialPrivilegeIdentity, r.SubID}
		mask := initialPrivilegeMetadataMask(*r)
		if mask == 0 || r.PrivType != "i" && r.PrivType != "e" || seen[key] {
			return invalid()
		}
		seen[key] = true
		var entries []json.RawMessage
		if json.Unmarshal(f["entries"], &entries) != nil || len(entries) == 0 {
			return invalid()
		}
		type grantKey struct {
			Grantor, Grantee string
			Public           bool
		}
		grants := make(map[grantKey]bool, len(entries))
		for j, data := range entries {
			if _, err := initialPrivilegeFields(data, "grantor", "grantee", "public", "privileges", "grant_options"); err != nil {
				return invalid()
			}
			g := r.Entries[j]
			key := grantKey{g.Grantor, g.Grantee, g.Public}
			if g.Grantor == "" || g.Privileges == 0 || g.Privileges & ^mask != 0 || g.GrantOptions & ^g.Privileges != 0 || g.Public && (g.Grantee != "" || g.GrantOptions != 0) || !g.Public && g.Grantee == "" || grants[key] {
				return invalid()
			}
			grants[key] = true
		}
	}
	return &out, true, nil
}

// ResolveInitialPrivileges resolves only against the final isolated replay
// state. The caller feeds the returned complete map through catalog validation
// and the normal final principal/object replacement fences.
func ResolveInitialPrivileges(ctx context.Context, view catalog.Read, roles authorization.Read, metadata *InitialPrivilegesMetadata) (map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord, error) {
	if metadata == nil || !metadata.Complete {
		return nil, initialPrivilegesError()
	}
	identities := make(map[InitialPrivilegeIdentity]ids.ObjectAddress)
	ambiguous := make(map[InitialPrivilegeIdentity]bool)
	for address, d := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if temporaryDumpObject(view.State(), d) {
			continue
		}
		identity, err := initialPrivilegeIdentity(view, d)
		if err != nil {
			continue
		} // Descriptors without ACLs cannot be targets.
		if _, exists := identities[identity]; exists {
			ambiguous[identity] = true
		}
		identities[identity] = address
	}
	next := make(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord, len(metadata.Records))
	for _, r := range metadata.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		address, ok := identities[r.InitialPrivilegeIdentity]
		if !ok || ambiguous[r.InitialPrivilegeIdentity] {
			return nil, initialPrivilegesError()
		}
		address.SubID = r.SubID
		mask, err := catalog.InitialPrivilegeMask(view, address)
		if err != nil {
			return nil, err
		}
		if next[address] != nil || !initialPrivilegeTarget(view.State(), address) || mask == 0 || r.PrivType != "i" && r.PrivType != "e" || len(r.Entries) == 0 {
			return nil, initialPrivilegesError()
		}
		acl := authorization.ACLValue{Explicit: true, Entries: make([]authorization.ACLEntry, len(r.Entries))}
		for i, g := range r.Entries {
			if g.Privileges & ^mask != 0 || g.GrantOptions & ^g.Privileges != 0 {
				return nil, initialPrivilegesError()
			}
			grantor, ok := roles.Lookup(g.Grantor)
			if !ok {
				return nil, fmt.Errorf("stuntgres: initial privilege grantor %q does not exist", g.Grantor)
			}
			e := authorization.ACLEntry{Grantor: grantor.Ref, Grantee: authorization.ACLGrantee{Public: g.Public}, Privileges: g.Privileges, GrantOptions: g.GrantOptions}
			if !g.Public {
				grantee, ok := roles.Lookup(g.Grantee)
				if !ok {
					return nil, fmt.Errorf("stuntgres: initial privilege grantee %q does not exist", g.Grantee)
				}
				e.Grantee.Role = grantee.Ref
			}
			acl.Entries[i] = e
		}
		next[address] = &catalog.InitialPrivilegeRecord{PrivType: r.PrivType[0], ACL: acl}
	}
	return next, ctx.Err()
}
