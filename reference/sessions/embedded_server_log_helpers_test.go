package reference_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// serverQueryLogCycle is one command cycle as the client saw it, with the
// query.log fields it must produce.
type serverQueryLogCycle struct {
	frontend, query, code string
	params                []any
	frames                []serverFrame
	decoded               map[int]any // response index -> readable form
	steps                 map[int]map[string]any
}

func serverQueryLogParam(format, value string) map[string]any {
	return map[string]any{"index": float64(1), "oid": float64(23), "type": "integer", "format": format, "null": false, "encoding": "text", "value": value}
}

// serverWantQueryLogRecord checks one record against the cycle the client saw:
// every backend message must carry exactly the bytes that were received.
func serverWantQueryLogRecord(t *testing.T, i int, r map[string]any, want serverQueryLogCycle) {
	t.Helper()
	params := want.params
	if params == nil {
		params = []any{}
	}
	if r["cycle"] != float64(i+1) || r["message"] != "query" || r["database"] != fixture.ServerDatabase || r["user"] != fixture.ServerUser ||
		r["frontend"] != want.frontend || r["query"] != want.query || r["error"] != want.code || r["complete"] != true || !reflect.DeepEqual(r["params"], params) {
		t.Fatalf("record %d: %v", i, r)
	}
	if _, ok := r["duration_us"].(float64); !ok {
		t.Fatalf("record %d duration: %v", i, r)
	}
	steps, _ := r["steps"].([]any)
	if len(steps) != len(want.frontend) {
		t.Fatalf("record %d steps %v", i, steps)
	}
	for j, step := range steps {
		entry, _ := step.(map[string]any)
		if entry["type"] != want.frontend[j:j+1] {
			t.Fatalf("record %d step %d %v", i, j, entry)
		}
		if expected, ok := want.steps[j]; ok && !reflect.DeepEqual(entry, expected) {
			t.Fatalf("record %d step %d %v want %v", i, j, entry, expected)
		}
	}
	response, _ := r["response"].([]any)
	if len(response) != len(want.frames) {
		t.Fatalf("record %d has %d messages, client saw %s", i, len(response), serverTags(want.frames))
	}
	for j, message := range response {
		entry, _ := message.(map[string]any)
		body, err := base64.StdEncoding.DecodeString(entry["data"].(string))
		if err != nil || entry["type"] != string(want.frames[j].tag) || !bytes.Equal(body, want.frames[j].body) {
			t.Fatalf("record %d message %d %v differs from received %c %x", i, j, entry, want.frames[j].tag, want.frames[j].body)
		}
		if name, _ := entry["name"].(string); name == "" {
			t.Fatalf("record %d message %d unnamed", i, j)
		}
		if decoded, ok := want.decoded[j]; ok && !reflect.DeepEqual(entry["decoded"], decoded) {
			t.Fatalf("record %d message %d decoded %v want %v", i, j, entry["decoded"], decoded)
		}
	}
	if want.code != "" {
		decoded, _ := response[2].(map[string]any)["decoded"].(map[string]any)
		if decoded["code"] != want.code || decoded["message"] != "division by zero" || decoded["severity"] != "ERROR" {
			t.Fatalf("record %d error %v", i, decoded)
		}
	}
}
func serverDumpFiles(t *testing.T, dir, db string) []string {
	t.Helper()
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, db+"--") || !strings.HasSuffix(name, ".sql") {
			continue
		}
		suffix := strings.TrimSuffix(strings.TrimPrefix(name, db+"--"), ".sql")
		if len(suffix) != 14 {
			t.Fatalf("invalid published dump %s", name)
		}
		if _, e = time.Parse("20060102150405", suffix); e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(dir, name)
		info, e := entry.Info()
		if e != nil {
			t.Fatal(e)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			t.Fatalf("dump mode %v", info.Mode())
		}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
func serverLogRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if e = json.Unmarshal([]byte(line), &record); e != nil {
			t.Fatalf("invalid JSONL %q: %v", line, e)
		}
		stamp, ok := record["tstamp"].(string)
		if !ok {
			t.Fatal("missing tstamp", record)
		}
		if _, e = time.Parse(time.RFC3339, stamp); e != nil {
			t.Fatal(e)
		}
		severity, ok := record["severity"].(string)
		if !ok || severity != strings.ToUpper(severity) {
			t.Fatal(record)
		}
		if _, ok := record["message"].(string); !ok {
			t.Fatal(record)
		}
		out = append(out, record)
	}
	return out
}

// serverDualCycle is one command cycle on a dual-backend connection and the
// reference part of its query.log record.
type serverDualCycle struct {
	frontend, query string
	frames          []serverFrame // as the client received them (stuntgres)
	refTags         string        // reference message types
	refCode         string
	diff            []string // exact response_diff; nil means absent
	diffPrefix      []string // when the reference text is not fully known
}

// serverWantDualRecord checks one dual-backend record: the stuntgres
// response holds exactly the bytes the client received, and the reference
// part matches the expected types, SQLSTATE and differences.
func serverWantDualRecord(t *testing.T, i int, r map[string]any, want serverDualCycle) {
	t.Helper()
	if r["frontend"] != want.frontend || r["query"] != want.query || r["error"] != "" || r["error_ref"] != want.refCode || r["complete"] != true {
		t.Fatalf("record %d: %v", i, r)
	}
	response, _ := r["response"].([]any)
	reference, _ := r["response_ref"].([]any)
	if want.frames != nil {
		if len(response) != len(want.frames) {
			t.Fatalf("record %d has %d messages, client saw %s", i, len(response), serverTags(want.frames))
		}
		for j, message := range response {
			body, err := base64.StdEncoding.DecodeString(message.(map[string]any)["data"].(string))
			if err != nil || !bytes.Equal(body, want.frames[j].body) {
				t.Fatalf("record %d message %d differs from received", i, j)
			}
		}
	}
	var tags []byte
	for _, message := range reference {
		tags = append(tags, message.(map[string]any)["type"].(string)...)
	}
	if string(tags) != want.refTags {
		t.Fatalf("record %d reference %s, want %s: %v", i, tags, want.refTags, reference)
	}
	diff, present := r["response_diff"].([]any)
	var lines []string
	for _, line := range diff {
		lines = append(lines, line.(string))
	}
	switch {
	case want.diffPrefix != nil:
		if len(lines) != len(want.diffPrefix) {
			t.Fatalf("record %d diff %q, want prefixes %q", i, lines, want.diffPrefix)
		}
		for j, prefix := range want.diffPrefix {
			if !strings.HasPrefix(lines[j], prefix) {
				t.Fatalf("record %d diff %q, want prefixes %q", i, lines, want.diffPrefix)
			}
		}
	case want.diff == nil:
		if present {
			t.Fatalf("record %d unexpected diff %q", i, lines)
		}
		// Equal responses: the same messages with the same bytes.
		for j := range reference {
			if !reflect.DeepEqual(reference[j], response[j]) {
				t.Fatalf("record %d message %d: reference %v, stuntgres %v", i, j, reference[j], response[j])
			}
		}
	case !slices.Equal(lines, want.diff):
		t.Fatalf("record %d diff\n got  %q\n want %q", i, lines, want.diff)
	}
}
