package reference_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Native Restore owns this protocol, not SQL execution. The public SQL witness
// in Persistence independently specifies the logical modes and real effects.
func triggerModeRejectReplay(t *testing.T, p *fixture.Pair, saved []byte, check func()) {
	t.Helper()
	const prefix = "-- stuntgres-fk-trigger-mode-v2 "
	// Find the terminal footer, not marker-looking text in an SQL literal.
	start := len(saved)
	for start > 0 {
		end := start
		if saved[end-1] == '\n' { end-- }
		line := bytes.LastIndexByte(saved[:end], '\n')+1
		if !bytes.HasPrefix(saved[line:end], []byte(prefix)) { break }
		start = line
	}
	if start == len(saved) {
		t.Fatal("FK modes have no identity-safe replay annotation")
	}
	relativeEnd := bytes.IndexByte(saved[start:], '\n')
	if relativeEnd < 0 {
		t.Fatal("unterminated FK mode annotation")
	}
	end := start + relativeEnd + 1
	payload := saved[start+len(prefix) : end-1]
	decoded, err := base64.StdEncoding.Strict().DecodeString(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(decoded, &record); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(saved[:start])
	if record["seal"] != hex.EncodeToString(sum[:]) {
		t.Fatal("footer does not seal the exact preceding SQL")
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(saved[start:]), "\n"), "\n") {
		if !strings.HasPrefix(line, prefix) { t.Fatal("FK footer is not terminal") }
		raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(line, prefix))
		if err != nil { t.Fatal(err) }
		var entry map[string]any
		if err := json.Unmarshal(raw, &entry); err != nil || entry["seal"] != record["seal"] {
			t.Fatal("inconsistent footer seal", err)
		}
	}
	replace := func(comment string) string { return string(saved[:start]) + comment + string(saved[end:]) }
	encode := func(raw []byte) string { return prefix + base64.StdEncoding.EncodeToString(raw) + "\n" }
	bad := []string{
		replace(prefix + "!invalid!\n"),
		replace(strings.Replace(string(saved[start:end]), "-v2 ", "-v999 ", 1)),
		replace(strings.Replace(string(saved[start:end]), "-v2 ", "-v1 ", 1)),
		string(saved[:end]) + string(saved[start:end]) + string(saved[end:]),
		replace(encode(append(bytes.Clone(decoded), []byte(" {}")...))),
		// Removing a complete record leaves an incomplete four-slot snapshot.
		string(saved[:start]) + string(saved[end:]),
	}
	for _, field := range []string{"owner_schema", "owner_table", "constraint", "slot", "mode", "seal"} {
		copyRecord := make(map[string]any, len(record))
		for k, v := range record {
			copyRecord[k] = v
		}
		delete(copyRecord, field)
		raw, err := json.Marshal(copyRecord)
		if err != nil {
			t.Fatal(err)
		}
		bad = append(bad, replace(encode(raw)))
	}
	for _, mutation := range []struct {
		field string
		value any
	}{
		{"constraint", "absent_constraint"}, {"owner_table", "absent_owner"}, {"owner_schema", "absent_schema"},
		{"mode", "X"}, {"slot", -1}, {"slot", 4}, {"slot", "2"}, {"slot", nil},
		{"seal", "X"}, {"seal", strings.Repeat("0", 64)}, {"unknown", "field"},
		{"sql", "obsolete"}, {"trigger", "obsolete"}, {"relation_table", "obsolete"},
	} {
		copyRecord := make(map[string]any, len(record))
		for k, v := range record {
			copyRecord[k] = v
		}
		copyRecord[mutation.field] = mutation.value
		raw, err := json.Marshal(copyRecord)
		if err != nil {
			t.Fatal(err)
		}
		bad = append(bad, replace(encode(raw)))
	}
	// Duplicate keys must not be silently accepted by encoding/json's normal
	// last-key-wins behavior, even when the repeated value is otherwise valid.
	duplicate := append([]byte(`{"constraint":"ignored",`), decoded[1:]...)
	bad = append(bad, replace(encode(duplicate)))
	for n, script := range bad {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatalf("invalid FK mode replay %d accepted", n)
		}
		check()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	reader := &triggerModeCancelReader{Reader: bytes.NewReader(saved), cancel: cancel}
	if err := stuntgres.Restore(ctx, p.Native, reader); !errors.Is(err, context.Canceled) {
		t.Fatal("reader cancellation", err)
	}
	check()
}

type triggerModeCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *triggerModeCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.cancel()
	}
	return n, err
}
