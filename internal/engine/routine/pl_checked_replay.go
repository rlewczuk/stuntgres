package routine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"stuntgres/internal/parser"
)

const plCheckedMarker = "stuntgres-pl-checked-v1"

// PLCheckedReplay retains admission, not executable code or validation authority.
// Header excludes the body: replay must genuinely compile even modified source.
// Owner is independently paired with the immediately following real transfer.
type PLCheckedReplay struct {
	Header string `json:"header"`
	Owner  string `json:"owner"`
}

type PositionedPLChecked struct {
	Record PLCheckedReplay
	End    int
}

func invalidPLChecked() error { return fmt.Errorf("stuntgres: invalid checked PL replay metadata") }

func (r PLCheckedReplay) declaration() (*parser.CreateFunction, error) {
	if r.Owner == "" || !strings.HasSuffix(r.Header, " ") {
		return nil, invalidPLChecked()
	}
	statements, err := parser.Parse(r.Header + "AS '';")
	if err != nil || len(statements) != 1 {
		return nil, invalidPLChecked()
	}
	s, ok := statements[0].(*parser.CreateFunction)
	if !ok || s.Pos.Offset != 0 || s.Replace || s.Standard != nil || s.SymbolSpecified || len(s.Name.Parts) != 2 || s.Body != "" || s.Language.Text == "" || s.BodyPos.Offset < len(r.Header) {
		return nil, invalidPLChecked()
	}
	return s, nil
}

func (r PLCheckedReplay) Comment() (string, error) {
	if _, err := r.declaration(); err != nil {
		return "", err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return "-- " + plCheckedMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}

func DecodePLChecked(comment string) (PLCheckedReplay, bool, error) {
	var record PLCheckedReplay
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-pl-checked-") {
		return record, false, nil
	}
	bad := func() (PLCheckedReplay, bool, error) { return PLCheckedReplay{}, true, invalidPLChecked() }
	fields := strings.Fields(text)
	if len(fields) != 2 || fields[0] != plCheckedMarker {
		return bad()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(fields[1])
	if err != nil {
		return bad()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return bad()
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return bad()
		}
		seen[name] = true
		var target *string
		switch name {
		case "header":
			target = &record.Header
		case "owner":
			target = &record.Owner
		default:
			return bad()
		}
		// A token check rejects JSON null instead of accepting its zero value.
		value, err := decoder.Token()
		text, ok := value.(string)
		if err != nil || !ok {
			return bad()
		}
		*target = text
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return bad()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return bad()
	}
	if _, err := record.declaration(); err != nil {
		return bad()
	}
	return record, true, nil
}

// PairPLChecked requires adjacency both to CREATE and its actual owner transfer.
// Comparing the complete emitted header binds schema/name/kind/signature/language
// without blessing the body, borrowing source OIDs or crossing an owner switch.
func PairPLChecked(script string, statements []parser.Statement, records []PositionedPLChecked) (map[*parser.CreateFunction]PLCheckedReplay, error) {
	if len(records) == 0 {
		return nil, nil
	}
	pairs := make(map[*parser.CreateFunction]PLCheckedReplay, len(records))
	for _, entry := range records {
		header, err := entry.Record.declaration()
		if err != nil {
			return nil, err
		}
		if entry.End < 0 || entry.End > len(script) {
			return nil, invalidPLChecked()
		}
		index := -1
		for i, statement := range statements {
			if candidate, ok := statement.(*parser.CreateFunction); ok && candidate.Pos.Offset >= entry.End {
				index = i
				break
			}
		}
		if index < 0 || index+1 >= len(statements) {
			return nil, invalidPLChecked()
		}
		create, ok := statements[index].(*parser.CreateFunction)
		if !ok || create.Replace || create.Standard != nil || create.SymbolSpecified || create.Procedure != header.Procedure || len(create.Name.Parts) != 2 || create.Pos.Offset < entry.End || strings.TrimSpace(script[entry.End:create.Pos.Offset]) != "" || !strings.HasPrefix(script[create.Pos.Offset:], entry.Record.Header+"AS ") || pairs[create].Header != "" {
			return nil, invalidPLChecked()
		}
		owner, ok := statements[index+1].(*parser.AlterRoutine)
		kind := "function"
		if create.Procedure {
			kind = "procedure"
		}
		if !ok || owner.Action != "owner" || owner.Kind != kind || owner.OwnerIdentity || owner.Target.Text != entry.Record.Owner || len(owner.Routine.Name.Parts) != 2 || owner.Routine.Name.Parts[0].Text != create.Name.Parts[0].Text || owner.Routine.Name.Parts[1].Text != create.Name.Parts[1].Text {
			return nil, invalidPLChecked()
		}
		inputIndex := 0
		for i := range create.Arguments {
			argument := &create.Arguments[i]
			if argument.Mode == "out" || argument.Mode == "table" {
				continue
			}
			if inputIndex >= len(owner.Routine.Arguments) {
				return nil, invalidPLChecked()
			}
			input, other := &argument.Type, &owner.Routine.Arguments[inputIndex].Type
			// CREATE retains SQL declaration aliases (integer), whereas the
			// routine-identity grammar already canonicalizes them (int4).
			// Reuse that same standalone type parser, preserving quoted and
			// qualified user identities rather than adding another alias table.
			inputName := input.Name
			if input.Schema == nil && len(input.Parts) <= 1 && !input.Quoted {
				canonical, err := parser.ParseTypeName(context.Background(), input.Name, nil)
				if err != nil {
					return nil, invalidPLChecked()
				}
				inputName = canonical.Name
			}
			inputSchema, otherSchema := "", ""
			if input.Schema != nil {
				inputSchema = input.Schema.Text
			}
			if other.Schema != nil {
				otherSchema = other.Schema.Text
			}
			if inputName != other.Name || inputSchema != otherSchema || len(input.ArrayBounds) != len(other.ArrayBounds) {
				return nil, invalidPLChecked()
			}
			inputIndex++
		}
		if inputIndex != len(owner.Routine.Arguments) {
			return nil, invalidPLChecked()
		}
		pairs[create] = entry.Record
	}
	return pairs, nil
}
