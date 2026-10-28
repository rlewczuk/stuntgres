package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/sqlstate"
)

// Ltree constructs an installation-owned scalar identity, not a builtin OID.
func Ltree(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid ltree descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "LTREE", ltree: true}, nil
}

func (d *Descriptor) IsLtree() bool { return d != nil && d.ltree }

type ltreePayload struct {
	text string
	labels []labelSpan
}

func ltreeError(code, message, detail string) error {
	return sqlstate.SoftInputError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail})
}

func (d *Descriptor) ltreeInput(ctx context.Context, text string) (Value, error) {
	// PostgreSQL counts all separators before checking syntax or label length.
	labels := 1
	for i := range len(text) {
		if i&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
		if text[i] == '.' { labels++ }
	}
	if labels > 65535 {
		return Value{}, ltreeError("54000", fmt.Sprintf("number of ltree labels (%d) exceeds the maximum allowed (65535)", labels), "")
	}
	start := 0
	var index []labelSpan
	if len(text) != 0 { index = make([]labelSpan, 0, labels) }
	for i := 0; i <= len(text); i++ {
		if i&255 == 0 { if err := ctx.Err(); err != nil { return Value{}, err } }
		if i < len(text) && text[i] != '.' {
			// UTF8/C accepts only ASCII. All preceding accepted bytes are also
			// characters, so this position identifies the first invalid rune.
			if !lqueryLabel(text[i]) { return Value{}, ltreeError("42601", fmt.Sprintf("ltree syntax error at character %d", i+1), "") }
			continue
		}
		if i == start && len(text) != 0 {
			if i == len(text) { return Value{}, ltreeError("42601", "ltree syntax error", "Unexpected end of input.") }
			return Value{}, ltreeError("42601", fmt.Sprintf("ltree syntax error at character %d", i+1), "")
		}
		if i-start > 1000 { return Value{}, ltreeError("42622", "label string is too long", fmt.Sprintf("Label length is %d, must be at most 1000, at character %d.", i-start, i+1)) }
		if i > start { index = append(index, labelSpan{start, i}) }
		start = i+1
	}
	return Value{typ: d, ltree: &ltreePayload{text: text, labels: index}}, nil
}

// A present carrier represents even the empty path; zero Value is not a path.
func (v Value) validLtree() bool { return v.ltree != nil && (v.ltree.text == "") == (len(v.ltree.labels) == 0) }

func (d *Descriptor) ltreeOutput(ctx context.Context, v Value) (string, error) {
	if err := ctx.Err(); err != nil { return "", err }
	if v.typ != d || v.null || !v.validLtree() { return "", fmt.Errorf("invalid ltree payload") }
	return v.ltree.text, nil
}

// RemapLtree is reserved for an explicit, source-authorized catalog import.
// Retag never authorizes crossing installation generations, even for NULL.
func (v Value) RemapLtree(target *Descriptor) (Value, error) {
	if !v.typ.IsLtree() || !target.IsLtree() || !v.null && !v.validLtree() { return Value{}, fmt.Errorf("incompatible imported ltree") }
	v.typ = target
	return v, nil
}
