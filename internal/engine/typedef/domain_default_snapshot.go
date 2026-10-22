package typedef

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const domainDefaultMarker = "stuntgres-domain-default-v1"

// The expression is ordinary SQL in the paired CREATE DOMAIN, not executable
// JSON or typdefault text. Usage records the genuine result identity so copied
// defaults can bind without introducing a new immediate-ancestor conversion.
type DomainDefaultSnapshot struct {
	Schema string              `json:"schema"`
	Domain string              `json:"domain"`
	Own    bool                `json:"own"`
	Absent bool                `json:"absent"`
	Usage  *DomainDefaultUsage `json:"usage,omitempty"`
}

type DomainDefaultUsage struct {
	Type      []string `json:"type"`
	Typmod    int32    `json:"typmod"`
	Collation []string `json:"collation,omitempty"`
}

type PositionedDomainDefault struct {
	Record DomainDefaultSnapshot
	End    int
}

func CaptureDomainDefault(view catalog.Read, d *catalog.Type) (DomainDefaultSnapshot, error) {
	ns, ok := view.Descriptor(d.Namespace())
	if !ok {
		return DomainDefaultSnapshot{}, fmt.Errorf("stuntgres: missing domain default namespace")
	}
	r := DomainDefaultSnapshot{Schema: ns.Name(), Domain: d.Name(), Own: d.DomainDefaultOwn, Absent: d.DomainDefault == nil}
	if d.DomainDefault == nil {
		return r, nil
	}
	u := d.DomainDefault.Expression.ResultUsage()
	t, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})
	if !ok || t.SQLType != u.Type || !u.Valid() {
		return r, fmt.Errorf("stuntgres: foreign domain default result usage")
	}
	tns, ok := view.Descriptor(t.Namespace())
	if !ok {
		return r, fmt.Errorf("stuntgres: missing domain default result namespace")
	}
	r.Usage = &DomainDefaultUsage{Type: []string{tns.Name(), t.Name()}, Typmod: u.Typmod}
	if u.Collation.ID != 0 {
		c, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)})
		if !ok {
			return r, fmt.Errorf("stuntgres: missing domain default collation")
		}
		cns, ok := view.Descriptor(c.Namespace())
		if !ok {
			return r, fmt.Errorf("stuntgres: missing domain default collation namespace")
		}
		r.Usage.Collation = []string{cns.Name(), c.Name()}
	}
	return r, nil
}

func EncodeDomainDefault(r DomainDefaultSnapshot) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return "-- " + domainDefaultMarker + " " + base64.StdEncoding.EncodeToString(raw) + "\n", nil
}

func (r DomainDefaultSnapshot) Validate() error {
	if r.Schema == "" || r.Domain == "" || r.Absent != (r.Usage == nil) || r.Absent && r.Own {
		return fmt.Errorf("stuntgres: invalid domain default snapshot mask")
	}
	if r.Usage != nil {
		if len(r.Usage.Type) != 2 || r.Usage.Type[0] == "" || r.Usage.Type[1] == "" || len(r.Usage.Collation) != 0 && (len(r.Usage.Collation) != 2 || r.Usage.Collation[0] == "" || r.Usage.Collation[1] == "") {
			return fmt.Errorf("stuntgres: invalid domain default snapshot identity")
		}
	}
	return nil
}

func DecodeDomainDefault(comment string) (DomainDefaultSnapshot, bool, error) {
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-domain-default-") {
		return DomainDefaultSnapshot{}, false, nil
	}
	fail := func(err error) (DomainDefaultSnapshot, bool, error) {
		return DomainDefaultSnapshot{}, true, fmt.Errorf("stuntgres: invalid domain default snapshot: %w", err)
	}
	tokens := strings.Fields(text)
	if len(tokens) != 2 || tokens[0] != domainDefaultMarker {
		return fail(fmt.Errorf("unsupported marker or payload"))
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(tokens[1])
	if err != nil {
		return fail(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := validateDomainReplayJSON(decoder); err != nil {
		return fail(err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fail(fmt.Errorf("trailing JSON data"))
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var r DomainDefaultSnapshot
	if err := decoder.Decode(&r); err != nil {
		return fail(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fail(err)
	}
	for _, name := range []string{"schema", "domain", "own", "absent"} {
		if _, ok := fields[name]; !ok {
			return fail(fmt.Errorf("missing snapshot field %s", name))
		}
	}
	if r.Usage != nil {
		var usage map[string]json.RawMessage
		if err := json.Unmarshal(fields["usage"], &usage); err != nil {
			return fail(err)
		}
		for _, name := range []string{"type", "typmod"} {
			if _, ok := usage[name]; !ok {
				return fail(fmt.Errorf("missing snapshot usage field %s", name))
			}
		}
	}
	if err := r.Validate(); err != nil {
		return fail(err)
	}
	return r, true, nil
}

func PairDomainDefaults(script string, statements []parser.Statement, records []PositionedDomainDefault) (map[*parser.CreateDomain]DomainDefaultSnapshot, error) {
	if len(records) == 0 {
		return nil, nil
	}
	pairs := make(map[*parser.CreateDomain]DomainDefaultSnapshot, len(records))
	at := 0
	for _, entry := range records {
		for at < len(statements) {
			if s, ok := statements[at].(*parser.CreateDomain); ok && s.Pos.Offset >= entry.End {
				break
			}
			at++
		}
		if at == len(statements) {
			return nil, fmt.Errorf("stuntgres: unpaired domain default snapshot")
		}
		s := statements[at].(*parser.CreateDomain)
		if entry.End < 0 || entry.End > len(script) || s.Pos.Offset < entry.End || strings.TrimSpace(script[entry.End:s.Pos.Offset]) != "" || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != entry.Record.Schema || s.Name.Parts[1].Text != entry.Record.Domain {
			return nil, fmt.Errorf("stuntgres: mismatched domain default snapshot target")
		}
		if _, duplicate := pairs[s]; duplicate {
			return nil, fmt.Errorf("stuntgres: duplicate domain default snapshot")
		}
		var raw parser.Expr
		for _, c := range s.Constraints {
			if c.Default != nil {
				if raw != nil {
					return nil, fmt.Errorf("stuntgres: duplicate paired domain default")
				}
				raw = c.Default
			}
		}
		if raw == nil {
			return nil, fmt.Errorf("stuntgres: missing paired domain default expression")
		}
		if entry.Record.Absent {
			literal, ok := raw.(parser.LiteralExpr)
			if !ok || literal.Value.Kind != parser.NullValue {
				return nil, fmt.Errorf("stuntgres: invalid absent domain default expression")
			}
		}
		if err := entry.Record.Validate(); err != nil {
			return nil, err
		}
		pairs[s] = entry.Record
	}
	return pairs, nil
}

func snapshotObjectName(parts []string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: parts[0]}, {Text: parts[1]}}}
}

// SnapshotUsage resolves qualified identities under the existing import host;
// numeric source OIDs are never transport authority.
func SnapshotUsage(h StageHost, auth ddl.Authority, r DomainDefaultSnapshot) (sqltype.Usage, error) {
	if err := r.Validate(); err != nil {
		return sqltype.Usage{}, err
	}
	if r.Usage == nil {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: absent domain default has no result usage")
	}
	t, err := h.NamedType(auth, snapshotObjectName(r.Usage.Type))
	if err != nil {
		return sqltype.Usage{}, err
	}
	u := t.SQLType.Usage()
	u.Typmod = r.Usage.Typmod
	if len(r.Usage.Collation) != 0 {
		c, err := h.FindCollation(auth, snapshotObjectName(r.Usage.Collation))
		if err != nil {
			return sqltype.Usage{}, err
		}
		u.Collation, err = c.Usage()
		if err != nil {
			return sqltype.Usage{}, err
		}
	} else {
		u.Collation = sqltype.CollationRef{}
	}
	if !u.Valid() {
		return sqltype.Usage{}, fmt.Errorf("stuntgres: invalid domain default snapshot result usage")
	}
	return u, nil
}
