package stored

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"stuntgres/internal/engine/catalog"
)

const foreignKeyModeMarker = "stuntgres-fk-trigger-mode-v2"

// ForeignKeyModeReplay uses only stable logical names. Seal binds a complete
// native FK snapshot to the exact SQL bytes, never to allocated RI identities.
type ForeignKeyModeReplay struct {
	OwnerSchema string `json:"owner_schema"`
	OwnerTable string `json:"owner_table"`
	Constraint string `json:"constraint"`
	Slot int `json:"slot"`
	Mode string `json:"mode"`
	Seal string `json:"seal"`
}

type PositionedForeignKeyMode struct {
	Record ForeignKeyModeReplay
	Start, End int
}

func (r ForeignKeyModeReplay) Validate() error {
	if r.OwnerSchema == "" || r.OwnerTable == "" || r.Constraint == "" || r.Slot < 0 || r.Slot > 3 || len(r.Mode) != 1 || !catalog.TriggerMode(r.Mode[0]).Valid() || len(r.Seal) != sha256.Size*2 {
		return fmt.Errorf("stuntgres: invalid foreign-key trigger mode record")
	}
	for _, c := range r.Seal {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return fmt.Errorf("stuntgres: invalid foreign-key snapshot seal")
		}
	}
	return nil
}


func (r ForeignKeyModeReplay) Comment() (string, error) {
	if err := r.Validate(); err != nil { return "", err }
	data, err := json.Marshal(r)
	if err != nil { return "", err }
	return "-- " + foreignKeyModeMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}

func DecodeForeignKeyMode(comment string) (ForeignKeyModeReplay, bool, error) {
	var r ForeignKeyModeReplay
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-fk-trigger-mode-") { return r, false, nil }
	bad := func() (ForeignKeyModeReplay, bool, error) {
		return ForeignKeyModeReplay{}, true, fmt.Errorf("stuntgres: invalid foreign-key trigger mode annotation")
	}
	fields := strings.Fields(text)
	if len(fields) != 2 || fields[0] != foreignKeyModeMarker { return bad() }
	data, err := base64.StdEncoding.Strict().DecodeString(fields[1])
	if err != nil { return bad() }
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') { return bad() }
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] { return bad() }
		seen[name] = true
		var target any
		switch name {
		case "owner_schema": target = &r.OwnerSchema
		case "owner_table": target = &r.OwnerTable
		case "constraint": target = &r.Constraint
		case "slot":
			var raw json.RawMessage
			if decoder.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &r.Slot) != nil { return bad() }
			continue
		case "mode": target = &r.Mode
		case "seal": target = &r.Seal
		default: return bad()
		}
		if decoder.Decode(target) != nil { return bad() }
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') { return bad() }
	if _, err = decoder.Token(); err != io.EOF || len(seen) != 6 { return bad() }
	if r.Validate() != nil { return bad() }
	return r, true, nil
}

// ValidateForeignKeyModeFooter removes only complete native record lines from
// the seal calculation. SQL appended after the footer remains ordinary SQL:
// it invalidates the waiver, rather than replacing its normal FK diagnostic.
func ValidateForeignKeyModeFooter(script string, records []PositionedForeignKeyMode) (bool, error) {
	if len(records) == 0 { return false, nil }
	digest := sha256.New()
	var buffer [4096]byte
	writeSQL := func(text string) {
		for len(text) != 0 {
			n := copy(buffer[:], text)
			_, _ = digest.Write(buffer[:n])
			text = text[n:]
		}
	}
	cursor := 0
	seal := records[0].Record.Seal
	masks := make(map[[3]string]uint8)
	for _, entry := range records {
		r := entry.Record
		if err := r.Validate(); err != nil { return false, err }
		if r.Seal != seal { return false, fmt.Errorf("stuntgres: inconsistent foreign-key snapshot seals") }
		if entry.Start < cursor || entry.End < entry.Start || entry.End > len(script) || entry.Start > 0 && script[entry.Start-1] != '\n' || !strings.HasPrefix(script[entry.Start:entry.End], "-- "+foreignKeyModeMarker+" ") {
			return false, fmt.Errorf("stuntgres: invalid foreign-key snapshot footer position")
		}
		key := [3]string{r.OwnerSchema,r.OwnerTable,r.Constraint}
		bit := uint8(1) << r.Slot
		if masks[key]&bit != 0 { return false, fmt.Errorf("stuntgres: duplicate foreign-key snapshot slot") }
		masks[key] |= bit
		writeSQL(script[cursor:entry.Start])
		cursor = entry.End
		if cursor < len(script) && script[cursor] == '\n' { cursor++ }
	}
	for _, mask := range masks {
		if mask != 15 { return false, fmt.Errorf("stuntgres: incomplete foreign-key mode snapshot") }
	}
	writeSQL(script[cursor:])
	return hex.EncodeToString(digest.Sum(nil)) == seal, nil
}
