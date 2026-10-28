package typedef

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const domainReplayMarker = "stuntgres-domain-null-v1"

type DomainReplayRecord struct {
	Schema string             `json:"schema"`
	Table  string             `json:"table"`
	Cells  []DomainReplayCell `json:"cells"`
}

type DomainReplayCell struct {
	Column string            `json:"column"`
	Value  *DomainReplayNode `json:"value"`
}

// Pointer fields distinguish absent fields from inappropriate empty fields.
// NULL is not part of this wire format: null nodes are explicit tagged objects.
type DomainReplayNode struct {
	Kind           string                   `json:"kind"`
	Type           *[]string                `json:"type,omitempty"`
	Scalar         *string                  `json:"scalar,omitempty"`
	Base           *DomainReplayNode        `json:"base,omitempty"`
	Members        *[]DomainReplayMember    `json:"members,omitempty"`
	Dimensions     *[]DomainReplayDimension `json:"dimensions,omitempty"`
	Elements       *[]DomainReplayNode      `json:"elements,omitempty"`
	Seg            *sqltype.SegDatum        `json:"seg,omitempty"`
	StatisticsTree *string                  `json:"statistics_tree,omitempty"`
}

type DomainReplayMember struct {
	Name  string            `json:"name"`
	Value *DomainReplayNode `json:"value"`
}

type DomainReplayDimension struct {
	Lower  int32 `json:"lower"`
	Length int32 `json:"length"`
}

type PositionedDomainReplay struct {
	Record DomainReplayRecord
	End    int
}

func ContainsStoredDomainNull(v sqltype.Value) bool {
	d := v.Type()
	if d == nil || !sqltype.ContainsDomainConstraints(d) {
		return false
	}
	if v.IsNull() {
		return d.IsDomain() && d.HasDomainConstraints()
	}
	if d.IsDomain() {
		base, err := v.BaseValue()
		return err == nil && ContainsStoredDomainNull(base)
	}
	if d.IsArray() {
		for i := range v.ArrayLen() {
			if ContainsStoredDomainNull(v.ArrayElement(i)) {
				return true
			}
		}
	}
	if d.IsComposite() {
		for i := range v.CompositeLen() {
			if ContainsStoredDomainNull(v.CompositeField(i)) {
				return true
			}
		}
	}
	return false
}

func DomainReplayIdentity(view catalog.Read, d *sqltype.Descriptor) ([]string, error) {
	descriptor, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.ID())})
	if !ok || descriptor.SQLType != d || !d.IsDomain() {
		return nil, fmt.Errorf("stuntgres: invalid native domain identity")
	}
	namespace, ok := view.Descriptor(descriptor.Namespace())
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing native domain namespace")
	}
	return []string{namespace.Name(), descriptor.Name()}, nil
}

func EncodeDomainReplay(record DomainReplayRecord) (string, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return "-- " + domainReplayMarker + " " + base64.StdEncoding.EncodeToString(raw) + "\n", nil
}

// The token pass rejects duplicate keys at EVERY depth, JSON nulls and trailing
// values; the typed pass rejects unknown fields and nonintegral/bounded dimensions.
func validateDomainReplayJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("native domain replay does not accept JSON null")
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || name != strings.ToLower(name) || seen[name] {
				return fmt.Errorf("duplicate or noncanonical native domain replay field")
			}
			seen[name] = true
			if err := validateDomainReplayJSON(decoder); err != nil {
				return err
			}
		}
		if seen["lower"] != seen["length"] {
			return fmt.Errorf("incomplete native domain replay dimension")
		}
		if seen["lower_bits"] || seen["upper_bits"] || seen["l_sigd"] || seen["u_sigd"] || seen["l_ext"] || seen["u_ext"] {
			for _, field := range []string{"lower_bits", "upper_bits", "l_sigd", "u_sigd", "l_ext", "u_ext"} {
				if !seen[field] {
					return fmt.Errorf("incomplete native seg replay payload")
				}
			}
		}
	case '[':
		for decoder.More() {
			if err := validateDomainReplayJSON(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid native domain replay JSON")
	}
	_, err = decoder.Token()
	return err
}

func DecodeDomainReplay(comment string) (DomainReplayRecord, bool, error) {
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-domain-null-") {
		return DomainReplayRecord{}, false, nil
	}
	parts := strings.Fields(text)
	fail := func(err error) (DomainReplayRecord, bool, error) {
		return DomainReplayRecord{}, true, fmt.Errorf("stuntgres: invalid native domain replay: %w", err)
	}
	if len(parts) != 2 || parts[0] != domainReplayMarker {
		return fail(fmt.Errorf("unsupported marker or payload"))
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return fail(err)
	}
	if !utf8.Valid(raw) {
		return fail(fmt.Errorf("invalid UTF8 payload"))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err = validateDomainReplayJSON(decoder); err != nil {
		return fail(err)
	}
	if _, err = decoder.Token(); err != io.EOF {
		return fail(fmt.Errorf("trailing JSON data"))
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record DomainReplayRecord
	if err = decoder.Decode(&record); err != nil {
		return fail(err)
	}
	if record.Schema == "" || record.Table == "" || len(record.Cells) == 0 {
		return fail(fmt.Errorf("missing target or cells"))
	}
	return record, true, nil
}

func PairDomainReplay(script string, statements []parser.Statement, records []PositionedDomainReplay) (map[*parser.Insert]DomainReplayRecord, error) {
	if len(records) == 0 {
		return nil, nil
	}
	pairs := make(map[*parser.Insert]DomainReplayRecord, len(records))
	statementIndex := 0
	for _, entry := range records {
		for statementIndex < len(statements) {
			insert, ok := statements[statementIndex].(*parser.Insert)
			if ok && insert.Pos.Offset >= entry.End {
				break
			}
			statementIndex++
		}
		if statementIndex == len(statements) {
			return nil, fmt.Errorf("stuntgres: unpaired native domain replay")
		}
		statement := statements[statementIndex]
		insert, ok := statement.(*parser.Insert)
		if !ok || entry.End < 0 || entry.End > len(script) || insert.Pos.Offset < entry.End || strings.TrimSpace(script[entry.End:insert.Pos.Offset]) != "" {
			return nil, fmt.Errorf("stuntgres: native domain replay must immediately precede its INSERT")
		}
		if _, duplicate := pairs[insert]; duplicate {
			return nil, fmt.Errorf("stuntgres: duplicate native domain replay")
		}
		if len(insert.Name.Parts) != 2 || insert.Name.Parts[0].Text != entry.Record.Schema || insert.Name.Parts[1].Text != entry.Record.Table || insert.Alias.Text != "" || len(insert.Rows) != 1 || insert.DefaultValues || insert.Source != nil || len(insert.With) != 0 || insert.Returning != nil || insert.Conflict != nil {
			return nil, fmt.Errorf("stuntgres: invalid native domain replay INSERT target")
		}
		pairs[insert] = entry.Record
	}
	return pairs, nil
}

func (n *DomainReplayNode) Shape(kind string) bool {
	if n == nil || n.Kind != kind {
		return false
	}
	if kind == "statistics_tree" {
		return n.StatisticsTree != nil && n.Type == nil && n.Scalar == nil && n.Base == nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil && n.Seg == nil
	}
	if n.StatisticsTree != nil {
		return false
	}
	if kind == "seg" {
		return n.Seg != nil && n.Type == nil && n.Scalar == nil && n.Base == nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	}
	if n.Seg != nil {
		return false
	}
	switch kind {
	case "null":
		return n.Type == nil && n.Scalar == nil && n.Base == nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	case "domain_null":
		return n.Type != nil && n.Scalar == nil && n.Base == nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	case "domain":
		return n.Type != nil && n.Scalar == nil && n.Base != nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	case "scalar":
		return n.Type == nil && n.Scalar != nil && n.Base == nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	case "array":
		return n.Type == nil && n.Scalar == nil && n.Base == nil && n.Members == nil && n.Dimensions != nil && n.Elements != nil
	case "composite":
		return n.Type == nil && n.Scalar == nil && n.Base == nil && n.Members != nil && n.Dimensions == nil && n.Elements == nil
	}
	return false
}
