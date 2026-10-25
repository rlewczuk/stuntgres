//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
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

func TestReferenceServerQueryLog(t *testing.T) {
	f := fixture.NewServerFixture(t, fixture.ServerQueryLog())
	const (
		simple = "SELECT 1 AS a, 'x'::text AS b, NULL::integer AS c"
		multi  = "CREATE TABLE server_query_log(v integer, w text); INSERT INTO server_query_log VALUES (1, 'one')"
		copyIn = "COPY server_query_log FROM STDIN"
		lookup = "SELECT v, w FROM server_query_log WHERE v = $1::integer"
		divide = "SELECT v / $1::integer FROM server_query_log WHERE v = 1"
		listen = "LISTEN server_query_log"
		final  = "SELECT 42 AS answer"
	)
	var native []serverQueryLogCycle
	var pid uint32
	var secrets []string
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverDial(t, f.DSNFor(b))
			serverWantCode(t, w.login(t, f.DSNFor(b), 196608), "")
			var cycles []serverQueryLogCycle
			run := func(c serverQueryLogCycle, tags string) []serverFrame {
				t.Helper()
				serverWantTags(t, c.frames, tags)
				serverWantCode(t, c.frames, c.code)
				cycles = append(cycles, c)
				return c.frames
			}
			rows := run(serverQueryLogCycle{frontend: "Q", query: simple, frames: w.query(t, simple),
				decoded: map[int]any{1: map[string]any{"values": []any{"1", "x", nil}}, 2: map[string]any{"tag": "SELECT 1"}, 3: map[string]any{"status": "I"}}}, "TDCZ")
			if got := serverRawRows(t, rows); !reflect.DeepEqual(got, [][][]byte{{[]byte("1"), []byte("x"), nil}}) {
				t.Fatalf("simple rows %q", got)
			}
			run(serverQueryLogCycle{frontend: "Q", query: multi, frames: w.query(t, multi),
				decoded: map[int]any{0: map[string]any{"tag": "CREATE TABLE"}, 1: map[string]any{"tag": "INSERT 0 1"}}}, "CCZ")
			w.send(t, serverPacket('Q', append([]byte(copyIn), 0)))
			frames := serverThrough(t, w, 'G')
			w.send(t, append(serverPacket('d', []byte("2\ttwo\n")), serverPacket('c', nil)...))
			run(serverQueryLogCycle{frontend: "Qdc", query: copyIn, frames: append(frames, w.ready(t)...),
				decoded: map[int]any{0: map[string]any{"format": float64(0), "column_formats": []any{float64(0), float64(0)}}, 1: map[string]any{"tag": "COPY 1"}},
				steps:   map[int]map[string]any{1: {"type": "d", "data": base64.StdEncoding.EncodeToString([]byte("2\ttwo\n")), "decoded": map[string]any{"data": "2\ttwo\n"}}, 2: {"type": "c"}}}, "GCZ")
			rows = run(serverQueryLogCycle{frontend: "PBDES", query: lookup, params: []any{serverQueryLogParam("binary", "2")},
				frames:  serverCycle(t, w, serverParse("", lookup), serverBind("", "", []int{1}, [][]byte{{0, 0, 0, 2}}), serverDescribe('P', ""), serverExecute("", 0)),
				decoded: map[int]any{3: map[string]any{"values": []any{"2", "two"}}},
				steps:   map[int]map[string]any{2: {"type": "D", "target": "portal", "query": lookup}}}, "12TDCZ")
			if got := serverRawRows(t, rows); !reflect.DeepEqual(got, [][][]byte{{[]byte("2"), []byte("two")}}) {
				t.Fatalf("extended rows %q", got)
			}
			w.send(t, bytes.Join([][]byte{serverParse("", copyIn), serverBind("", "", nil, nil), serverExecute("", 0)}, nil))
			frames = serverThrough(t, w, 'G')
			w.send(t, bytes.Join([][]byte{serverPacket('d', []byte("3\tthree\n")), serverPacket('c', nil), serverPacket('S', nil)}, nil))
			run(serverQueryLogCycle{frontend: "PBEdcS", query: copyIn, frames: append(frames, w.ready(t)...),
				decoded: map[int]any{3: map[string]any{"tag": "COPY 1"}},
				steps:   map[int]map[string]any{2: {"type": "E", "query": copyIn}, 3: {"type": "d", "data": base64.StdEncoding.EncodeToString([]byte("3\tthree\n")), "decoded": map[string]any{"data": "3\tthree\n"}}}}, "12GCZ")
			run(serverQueryLogCycle{frontend: "PBES", query: divide, code: "22012", params: []any{serverQueryLogParam("text", "0")},
				frames: serverCycle(t, w, serverParse("", divide), serverBind("", "", nil, [][]byte{[]byte("0")}), serverExecute("", 0))}, "12EZ")
			run(serverQueryLogCycle{frontend: "Q", query: listen, frames: w.query(t, listen)}, "CZ")
			s := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, s.query(t, "NOTIFY server_query_log, 'idle'"), "")
			idle := w.frame(t)
			serverWantNotification(t, idle, s.pid, "server_query_log", "idle")
			run(serverQueryLogCycle{frames: []serverFrame{idle},
				decoded: map[int]any{0: map[string]any{"pid": float64(s.pid), "channel": "server_query_log", "payload": "idle"}}}, "A")
			serverScalar(t, w, final, "42")
			// Close before the server stops, so no shutdown error can follow.
			w.send(t, serverPacket('X', nil))
			if _, err := w.readFrame(); !errors.Is(err, io.EOF) {
				t.Fatalf("terminate: %v", err)
			}
			if !b.PG {
				native, pid = cycles, w.pid
				secrets = append(secrets, base64.StdEncoding.EncodeToString(w.key), hex.EncodeToString(w.key), base64.StdEncoding.EncodeToString(s.key), hex.EncodeToString(s.key))
			}
		})
	}
	f.Stop(t)
	path := filepath.Join(f.Logdir, "query.log")
	var records []map[string]any
	for _, r := range serverLogRecords(t, path) {
		if r["connection_id"] == float64(pid) {
			records = append(records, r)
		}
	}
	if len(records) != len(native)+1 {
		var summary []string
		for _, r := range records {
			var tags []string
			for _, m := range r["response"].([]any) {
				tags = append(tags, m.(map[string]any)["type"].(string))
			}
			summary = append(summary, fmt.Sprintf("%v:%s", r["frontend"], strings.Join(tags, "")))
		}
		t.Fatalf("%d records for connection %d, want %d: %v", len(records), pid, len(native)+1, summary)
	}
	if last := records[len(native)]; last["query"] != final || last["frontend"] != "Q" {
		t.Fatalf("final record %v", last)
	}
	for i, want := range native {
		serverWantQueryLogRecord(t, i, records[i], want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range append(secrets, fixture.ServerPassword, "$pbkdf2") {
		if strings.Contains(string(data), secret) {
			t.Fatal("secret in query log")
		}
	}
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
