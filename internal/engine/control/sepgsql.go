package control

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"unicode/utf8"
)

const SepgsqlRequirementMarker = "stuntgres-sepgsql-simulation-v1"

type SepgsqlRequirement struct {
	ID             string                      `json:"id"`
	Version        string                      `json:"version"`
	Database       string                      `json:"database"`
	RetiredColumns []SepgsqlRetiredColumnLabel `json:"retired_columns,omitempty"`
}

// SepgsqlRetiredColumnLabel names a genuine destination tombstone, never a
// source OID or an SQL-addressable column.
type SepgsqlRetiredColumnLabel struct {
	Schema    string `json:"schema"`
	Relation  string `json:"relation"`
	Attribute int32  `json:"attribute"`
	Label     string `json:"label"`
}

func (r SepgsqlRequirement) Comment() (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return "-- " + SepgsqlRequirementMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}
func DecodeSepgsqlRequirement(comment string) (*SepgsqlRequirement, bool, error) {
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-sepgsql-") {
		return nil, false, nil
	}
	invalid := func() (*SepgsqlRequirement, bool, error) {
		return nil, true, fmt.Errorf("stuntgres: invalid sepgsql simulation requirement")
	}
	marker, payload, ok := strings.Cut(text, " ")
	if !ok || marker != SepgsqlRequirementMarker {
		return invalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil {
		return invalid()
	}
	fields, err := initialPrivilegeFields(data, "id", "version", "database")
	if err != nil {
		fields, err = initialPrivilegeFields(data, "id", "version", "database", "retired_columns")
	}
	if err != nil || !utf8.Valid(data) {
		return invalid()
	}
	var requirement SepgsqlRequirement
	if json.Unmarshal(data, &requirement) != nil || requirement.ID == "" || requirement.Version == "" || requirement.Database == "" || strings.ContainsRune(requirement.ID+requirement.Version+requirement.Database, 0) {
		return invalid()
	}
	if raw := fields["retired_columns"]; raw != nil {
		var records []json.RawMessage
		if json.Unmarshal(raw, &records) != nil || records == nil {
			return invalid()
		}
		seen := make(map[SepgsqlRetiredColumnLabel]bool, len(records))
		for i, data := range records {
			if _, err := initialPrivilegeFields(data, "schema", "relation", "attribute", "label"); err != nil {
				return invalid()
			}
			record := requirement.RetiredColumns[i]
			if record.Schema == "" || record.Relation == "" || record.Attribute <= 0 || record.Label == "" ||
				strings.ContainsRune(record.Schema+record.Relation+record.Label, 0) {
				return invalid()
			}
			record.Label = ""
			if seen[record] {
				return invalid()
			}
			seen[record] = true
		}
	}
	return &requirement, true, nil
}
func ValidateSepgsqlRequirement(requirement *SepgsqlRequirement, policy *sepgsql.Runtime) error {
	if requirement == nil || policy == nil || requirement.ID != policy.ID() || requirement.Version != policy.Version() {
		return fmt.Errorf("stuntgres: incompatible sepgsql simulation registration")
	}
	for _, record := range requirement.RetiredColumns {
		if !policy.Valid(record.Label) {
			return fmt.Errorf("stuntgres: invalid retired-column security context")
		}
	}
	return nil
}

func CaptureSepgsqlRequirement(ctx context.Context, view catalog.Read) (SepgsqlRequirement, error) {
	policy := view.Owner().Sepgsql
	out := SepgsqlRequirement{ID: policy.ID(), Version: policy.Version(), Database: view.MustDatabase(view.Owner().DatabaseAddress()).Name()}
	if err := catalog.ValidateSecurityLabels(view); err != nil {
		return out, err
	}
	for key, record := range view.State().SecurityLabels() {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		address := key.Object
		address.SubID = 0
		relation, ok := view.Relation(address)
		if !ok || key.Object.SubID <= 0 || temporaryDumpObject(view.State(), relation) {
			continue
		}
		if _, dropped := relation.ColumnMeta.Dropped[key.Object.SubID]; dropped {
			out.RetiredColumns = append(out.RetiredColumns, SepgsqlRetiredColumnLabel{
				Schema: view.MustNamespace(relation.Namespace()).Name(), Relation: relation.Name(), Attribute: key.Object.SubID, Label: record.Label,
			})
		}
	}
	slices.SortFunc(out.RetiredColumns, func(a, b SepgsqlRetiredColumnLabel) int {
		if c := strings.Compare(a.Schema, b.Schema); c != 0 {
			return c
		}
		if c := strings.Compare(a.Relation, b.Relation); c != 0 {
			return c
		}
		return cmp.Compare(a.Attribute, b.Attribute)
	})
	return out, ctx.Err()
}
func emitDumpSepgsql(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	view := s.OriginalCatalog
	policy := view.Owner().Sepgsql
	if policy == nil {
		return nil
	}
	if err := catalog.ValidateSecurityLabels(view); err != nil {
		return err
	}
	if err := w.forceOwner("postgres"); err != nil {
		return err
	}
	type statement struct{ identity, sql string }
	var statements []statement
	for key, record := range view.State().SecurityLabels() {
		if err := ctx.Err(); err != nil {
			return err
		}
		address := key.Object
		address.SubID = 0
		// Like pg_dump's dumpDatabase, serialize only the selected database's
		// shared label. Peer associations remain destination-owned on replay.
		if address.ClassID == ids.DatabaseClassID && address != view.Owner().DatabaseAddress() {
			continue
		}
		d := view.Get(address)
		if temporaryDumpObject(view.State(), d) {
			continue
		}
		if relation, ok := d.(*catalog.Relation); ok && key.Object.SubID > 0 {
			if _, dropped := relation.ColumnMeta.Dropped[key.Object.SubID]; dropped {
				continue // Imported by the typed requirement after history reconstruction.
			}
		}
		kind := catalog.SepgsqlClass(d, key.Object.SubID)
		target := catalog.QuoteIdentifier(d.Name())
		if d.Namespace().ObjectID != 0 {
			target = catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + target
		}
		switch kind {
		case "db_database":
			kind = "DATABASE"
		case "db_schema":
			kind = "SCHEMA"
		case "db_table":
			kind = "TABLE"
		case "db_sequence":
			kind = "SEQUENCE"
		case "db_view":
			kind = "VIEW"
		case "db_column":
			kind = "COLUMN"
			target += "." + catalog.QuoteIdentifier(catalog.SepgsqlAttributeName(d.(*catalog.Relation), key.Object.SubID))
		case "db_procedure":
			procedure := d.(*catalog.Procedure)
			kind = "FUNCTION"
			if procedure.RoutineKind == catalog.ProcedureRoutine {
				kind = "PROCEDURE"
			}
			if procedure.RoutineKind == catalog.AggregateRoutine {
				kind = "AGGREGATE"
			}
			var args []string
			for part := range strings.SplitSeq(string(procedure.Signature), ",") {
				if part == "" {
					continue
				}
				id, err := strconv.ParseUint(part, 10, 32)
				if err != nil {
					return err
				}
				typ, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
				if !ok {
					return fmt.Errorf("stuntgres: missing security label routine input")
				}
				args = append(args, catalog.TypeDiagnosticNameAt(view, nil, typ, nil))
			}
			if procedure.RoutineKind == catalog.AggregateRoutine && procedure.Signature == "" {
				args = append(args, "*")
			}
			target += "(" + strings.Join(args, ", ") + ")"
		default:
			return fmt.Errorf("stuntgres: unsupported portable security label")
		}
		sql := "SECURITY LABEL FOR selinux ON " + kind + " " + target + " IS " + dump.CommentLiteral(record.Label) + ";\n"
		statements = append(statements, statement{kind + " " + target, sql})
	}
	slices.SortFunc(statements, func(a, b statement) int { return strings.Compare(a.identity, b.identity) })
	for _, statement := range statements {
		if err := w.emit(statement.sql); err != nil {
			return err
		}
	}
	return nil
}

// RemapSecurityLabels performs a complete private rekey, preserving signed
// attribute identity. No source record/map is mutated, including swap cycles.
func RemapSecurityLabels(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	if next.SecurityLabelCount() == 0 || len(mapping) == 0 {
		return nil
	}
	records := make(map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord, next.SecurityLabelCount())
	for key, record := range next.SecurityLabels() {
		object := key.Object
		object.SubID = 0
		if target, ok := mapping[object]; ok {
			if target.ClassID != object.ClassID {
				return fmt.Errorf("stuntgres: cross-class security label remap")
			}
			target.SubID = key.Object.SubID
			key.Object = target
		}
		object = key.Object
		object.SubID = 0
		if d := next.Get(object); d == nil || catalog.SepgsqlClass(d, key.Object.SubID) == "" || records[key] != nil {
			return fmt.Errorf("stuntgres: invalid security label replacement identity")
		}
		records[key] = record
	}
	next.ClearSecurityLabels()
	for key, record := range records {
		next.SetSecurityLabel(key, record)
	}
	return nil
}
func RetainTemporarySecurityLabels(current catalog.Read, next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor) error {
	for key, record := range current.State().SecurityLabels() {
		object := key.Object
		object.SubID = 0
		if retained := temporary[object]; retained != nil {
			if next.Get(object) != retained || next.SecurityLabel(key) != nil || catalog.SepgsqlClass(retained, key.Object.SubID) == "" {
				return fmt.Errorf("stuntgres: conflicting retained temporary security label")
			}
			next.SetSecurityLabel(key, record)
		}
	}
	return nil
}
