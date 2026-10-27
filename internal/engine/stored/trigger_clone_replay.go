package stored

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"stuntgres/internal/parser"
)

const triggerCloneMarker = "stuntgres-trigger-clone-v1"

// TriggerCloneReplay links a real child CREATE to its historical parent. Ordinary
// PostgreSQL consumes the SQL; only native Restore reinstates clone provenance.
type TriggerCloneReplay struct {
	ParentSchema string `json:"parent_schema"`
	ParentTable string `json:"parent_table"`
	ParentTrigger string `json:"parent_trigger"`
	ChildSchema string `json:"child_schema"`
	ChildTable string `json:"child_table"`
	ChildTrigger string `json:"child_trigger"`
	SQL string `json:"sql"`
}

type PositionedTriggerClone struct {
	Record TriggerCloneReplay
	End int
}

func (r TriggerCloneReplay) Validate() error {
	if r.ParentSchema == "" || r.ParentTable == "" || r.ParentTrigger == "" || r.ChildSchema == "" || r.ChildTable == "" || r.ChildTrigger == "" || r.ChildTrigger != r.ParentTrigger || r.SQL == "" {
		return fmt.Errorf("stuntgres: invalid retained trigger clone record")
	}
	statements, err := parser.Parse(r.SQL)
	if err != nil || len(statements) != 1 {
		return fmt.Errorf("stuntgres: invalid retained trigger clone SQL")
	}
	s, ok := statements[0].(*parser.CreateTrigger)
	if !ok || !s.Row || s.Replace || s.Name.Text != r.ChildTrigger || len(s.Table.Parts) != 2 || s.Table.Parts[0].Text != r.ChildSchema || s.Table.Parts[1].Text != r.ChildTable {
		return fmt.Errorf("stuntgres: mismatched retained trigger clone SQL")
	}
	return nil
}

func (r TriggerCloneReplay) Comment() (string, error) {
	if err := r.Validate(); err != nil { return "", err }
	data, err := json.Marshal(r)
	if err != nil { return "", err }
	return "-- " + triggerCloneMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}

func DecodeTriggerClone(comment string) (TriggerCloneReplay, bool, error) {
	var record TriggerCloneReplay
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-trigger-clone-") { return record, false, nil }
	bad := func() (TriggerCloneReplay, bool, error) { return TriggerCloneReplay{}, true, fmt.Errorf("stuntgres: invalid retained trigger clone annotation") }
	fields := strings.Fields(text)
	if len(fields) != 2 || fields[0] != triggerCloneMarker { return bad() }
	data, err := base64.StdEncoding.Strict().DecodeString(fields[1])
	if err != nil { return bad() }
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') { return bad() }
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] { return bad() }
		seen[name] = true
		var target *string
		switch name {
		case "parent_schema": target = &record.ParentSchema
		case "parent_table": target = &record.ParentTable
		case "parent_trigger": target = &record.ParentTrigger
		case "child_schema": target = &record.ChildSchema
		case "child_table": target = &record.ChildTable
		case "child_trigger": target = &record.ChildTrigger
		case "sql": target = &record.SQL
		default: return bad()
		}
		if err := decoder.Decode(target); err != nil { return bad() }
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') { return bad() }
	if _, err := decoder.Token(); err != io.EOF { return bad() }
	if err := record.Validate(); err != nil { return bad() }
	return record, true, nil
}

func PairTriggerClones(script string, statements []parser.Statement, records []PositionedTriggerClone) (map[*parser.CreateTrigger]TriggerCloneReplay, error) {
	if len(records) == 0 { return nil, nil }
	pairs := make(map[*parser.CreateTrigger]TriggerCloneReplay, len(records))
	seen := make(map[[3]string]bool, len(records))
	for _, entry := range records {
		var paired *parser.CreateTrigger
		for _, statement := range statements {
			if candidate, ok := statement.(*parser.CreateTrigger); ok && candidate.Pos.Offset >= entry.End {
				paired = candidate
				break
			}
		}
		if paired == nil || entry.End < 0 || entry.End > len(script) || paired.Pos.Offset < entry.End || strings.TrimSpace(script[entry.End:paired.Pos.Offset]) != "" || !strings.HasPrefix(script[paired.Pos.Offset:], entry.Record.SQL) {
			return nil, fmt.Errorf("stuntgres: unpaired retained trigger clone annotation")
		}
		if err := entry.Record.Validate(); err != nil { return nil, err }
		key := [3]string{entry.Record.ChildSchema, entry.Record.ChildTable, entry.Record.ChildTrigger}
		if seen[key] || pairs[paired].SQL != "" { return nil, fmt.Errorf("stuntgres: duplicate retained trigger clone annotation") }
		seen[key], pairs[paired] = true, entry.Record
	}
	return pairs, nil
}
