package partition

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"unicode/utf8"
)

const pendingReplayMarker = "stuntgres-partition-detach-pending-v1"

type PendingDetachReplay struct {
	ParentSchema string `json:"parent_schema"`
	ParentTable  string `json:"parent_table"`
	ChildSchema  string `json:"child_schema"`
	ChildTable   string `json:"child_table"`
	Strategy     string `json:"strategy"`
}

func (r PendingDetachReplay) Validate() error {
	if r.ParentSchema == "" || r.ParentTable == "" || r.ChildSchema == "" || r.ChildTable == "" || (r.Strategy != "r" && r.Strategy != "l" && r.Strategy != "h") {
		return fmt.Errorf("stuntgres: invalid pending partition detach record")
	}
	return nil
}
func (r PendingDetachReplay) Comment() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return "-- " + pendingReplayMarker + " " + base64.StdEncoding.EncodeToString(raw) + "\n", nil
}

// DecodePendingDetach is invoked only by the parser's real line-comment hook.
// Reserved-prefix corruption is an ordinary error, never ignored SQL authority.
func DecodePendingDetach(comment string) (PendingDetachReplay, bool, error) {
	var r PendingDetachReplay
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(comment), "--"))
	if !strings.HasPrefix(text, "stuntgres-partition-detach-pending-") {
		return r, false, nil
	}
	invalid := func() (PendingDetachReplay, bool, error) {
		return r, true, fmt.Errorf("stuntgres: malformed pending partition detach record")
	}
	fields := strings.Fields(text)
	if len(fields) != 2 || fields[0] != pendingReplayMarker {
		return invalid()
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(fields[1])
	if err != nil || !utf8.Valid(raw) {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	seen := uint8(0)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return invalid()
		}
		key, ok := token.(string)
		if !ok {
			return invalid()
		}
		var target *string
		var bit uint8
		switch key {
		case "parent_schema":
			target = &r.ParentSchema
			bit = 1
		case "parent_table":
			target = &r.ParentTable
			bit = 2
		case "child_schema":
			target = &r.ChildSchema
			bit = 4
		case "child_table":
			target = &r.ChildTable
			bit = 8
		case "strategy":
			target = &r.Strategy
			bit = 16
		default:
			return invalid()
		}
		if seen&bit != 0 {
			return invalid()
		}
		seen |= bit
		token, err = decoder.Token()
		if err != nil {
			return invalid()
		}
		value, ok := token.(string)
		if !ok || value == "" {
			return invalid()
		}
		*target = value
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || seen != 31 {
		return invalid()
	}
	if _, err = decoder.Token(); err != io.EOF {
		return invalid()
	}
	if err = r.Validate(); err != nil {
		return r, true, err
	}
	return r, true, nil
}

func PendingDetachRecords(view catalog.Read) []PendingDetachReplay {
	var records []PendingDetachReplay
	for _, d := range view.Relations() {
		if d.Membership == nil || !d.Membership.DetachPending {
			continue
		}
		parent := view.MustRelation(d.Membership.Parent)
		strategy := "r"
		switch d.Membership.Strategy {
		case parser.PartitionList:
			strategy = "l"
		case parser.PartitionHash:
			strategy = "h"
		}
		records = append(records, PendingDetachReplay{ParentSchema: view.Get(parent.Namespace()).Name(), ParentTable: parent.Name(), ChildSchema: view.Get(d.Namespace()).Name(), ChildTable: d.Name(), Strategy: strategy})
	}
	slices.SortFunc(records, func(a, b PendingDetachReplay) int {
		if n := strings.Compare(a.ParentSchema, b.ParentSchema); n != 0 {
			return n
		}
		if n := strings.Compare(a.ParentTable, b.ParentTable); n != 0 {
			return n
		}
		if n := strings.Compare(a.ChildSchema, b.ChildSchema); n != 0 {
			return n
		}
		return strings.Compare(a.ChildTable, b.ChildTable)
	})
	return records
}
