package reference_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func embeddedRaw(t *testing.T, dsn string) *serverWire {
	t.Helper()
	w := serverDial(t, dsn)
	serverWantCode(t, w.login(t, dsn, 196608), "")
	return w
}
func embeddedTerminate(t *testing.T, w *serverWire) {
	t.Helper()
	w.send(t, serverPacket('X', nil))
	if _, err := w.readFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("Terminate: %v", err)
	}
}

func embeddedLogScenario(t *testing.T, dsn string) (uint32, []serverQueryLogCycle, []string) {
	t.Helper()
	w := embeddedRaw(t, dsn)
	var cycles []serverQueryLogCycle
	run := func(c serverQueryLogCycle, tags string) []serverFrame {
		t.Helper()
		serverWantTags(t, c.frames, tags)
		serverWantCode(t, c.frames, c.code)
		cycles = append(cycles, c)
		return c.frames
	}
	const simple = "SELECT 1 AS a, 'x'::text AS b, NULL::integer AS c"
	frames := run(serverQueryLogCycle{frontend: "Q", query: simple, frames: w.query(t, simple), decoded: map[int]any{1: map[string]any{"values": []any{"1", "x", nil}}}}, "TDCZ")
	serverWantRaw(t, frames, [][][]byte{{[]byte("1"), []byte("x"), nil}})
	const create = "CREATE TABLE embedded_log(v integer, w text); INSERT INTO embedded_log VALUES (1, 'one')"
	run(serverQueryLogCycle{frontend: "Q", query: create, frames: w.query(t, create)}, "CCZ")
	const copyIn = "COPY embedded_log FROM STDIN"
	w.send(t, serverPacket('Q', append([]byte(copyIn), 0)))
	frames = serverThrough(t, w, 'G')
	w.send(t, append(serverPacket('d', []byte("2\ttwo\n")), serverPacket('c', nil)...))
	run(serverQueryLogCycle{frontend: "Qdc", query: copyIn, frames: append(frames, w.ready(t)...), decoded: map[int]any{1: map[string]any{"tag": "COPY 1"}}, steps: map[int]map[string]any{1: {"type": "d", "data": base64.StdEncoding.EncodeToString([]byte("2\ttwo\n")), "decoded": map[string]any{"data": "2\ttwo\n"}}, 2: {"type": "c"}}}, "GCZ")
	const lookup = "SELECT v, w FROM embedded_log WHERE v = $1::integer"
	frames = run(serverQueryLogCycle{frontend: "PBDES", query: lookup, params: []any{serverQueryLogParam("binary", "2")}, frames: serverCycle(t, w, serverParse("", lookup), serverBind("", "", []int{1}, [][]byte{{0, 0, 0, 2}}), serverDescribe('P', ""), serverExecute("", 0)), decoded: map[int]any{3: map[string]any{"values": []any{"2", "two"}}}}, "12TDCZ")
	serverWantRaw(t, frames, [][][]byte{{[]byte("2"), []byte("two")}})
	const divide = "SELECT v / $1::integer FROM embedded_log WHERE v = 1"
	run(serverQueryLogCycle{frontend: "PBES", query: divide, code: "22012", params: []any{serverQueryLogParam("text", "0")}, frames: serverCycle(t, w, serverParse("", divide), serverBind("", "", nil, [][]byte{[]byte("0")}), serverExecute("", 0))}, "12EZ")
	run(serverQueryLogCycle{frontend: "Q", query: "LISTEN embedded_log", frames: w.query(t, "LISTEN embedded_log")}, "CZ")
	sender := embeddedRaw(t, dsn)
	serverWantCode(t, sender.query(t, "NOTIFY embedded_log, 'idle'"), "")
	idle := w.frame(t)
	if idle.tag != 'A' || !bytes.Equal(idle.body, append(serverPut32(nil, sender.pid), []byte("embedded_log\x00idle\x00")...)) {
		t.Fatalf("idle notification: %+v", idle)
	}
	cycles = append(cycles, serverQueryLogCycle{frames: []serverFrame{idle}, decoded: map[int]any{0: map[string]any{"pid": float64(sender.pid), "channel": "embedded_log", "payload": "idle"}}})
	run(serverQueryLogCycle{frontend: "Q", query: "SELECT 42 AS answer", frames: w.query(t, "SELECT 42 AS answer"), decoded: map[int]any{1: map[string]any{"values": []any{"42"}}}}, "TDCZ")
	embeddedTerminate(t, sender)
	embeddedTerminate(t, w)
	return w.pid, cycles, []string{base64.StdEncoding.EncodeToString(w.key), hex.EncodeToString(w.key), base64.StdEncoding.EncodeToString(sender.key), hex.EncodeToString(sender.key)}
}

func embeddedNormalizedRecords(t *testing.T, records []map[string]any) []map[string]any {
	t.Helper()
	// Preserve all logical fields and wire bytes except generated table/PID
	// identities, after the unnormalized records passed their independent goldens.
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	for _, r := range out {
		delete(r, "tstamp")
		delete(r, "duration_us")
		delete(r, "connection_id")
		response, _ := r["response"].([]any)
		for _, item := range response {
			message := item.(map[string]any)
			body, err := base64.StdEncoding.DecodeString(message["data"].(string))
			if err != nil {
				t.Fatal(err)
			}
			decoded, _ := message["decoded"].(map[string]any)
			switch message["type"] {
			case "A":
				clear(body[:4])
				decoded["pid"] = float64(0)
			case "T":
				columns, _ := decoded["columns"].([]any)
				pos := 2
				for _, item := range columns {
					column := item.(map[string]any)
					column["table_oid"] = float64(0)
					end := bytes.IndexByte(body[pos:], 0)
					if end < 0 || pos+end+19 > len(body) {
						t.Fatal("malformed RowDescription")
					}
					pos += end + 1
					clear(body[pos : pos+4])
					pos += 18
				}
			}
			message["data"] = base64.StdEncoding.EncodeToString(body)
		}
	}
	return out
}

func TestReferenceEmbeddedServerLogging(t *testing.T) {
	var baseline []map[string]any
	for _, form := range []string{"writer", "directory", "cli"} {
		t.Run(form, func(t *testing.T) {
			var path string
			var stop func()
			var backends []fixture.EmbeddedServerBackend
			var query, operations embeddedBuffer
			if form == "cli" {
				f := fixture.NewServerFixture(t, fixture.ServerQueryLog())
				for _, b := range f.Backends() {
					backends = append(backends, fixture.EmbeddedServerBackend{Name: b.Name, DSN: f.DSNFor(b)})
				}
				path = filepath.Join(f.Logdir, "query.log")
				stop = func() { f.Stop(t) }
			} else {
				cfg := embeddedConfig()
				cfg.QueryLog = true
				cfg.LogWriter = &operations
				if form == "writer" {
					cfg.QueryLogWriter = &query
				} else {
					cfg.LogDir = t.TempDir()
					path = filepath.Join(cfg.LogDir, "query.log")
				}
				f := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
				backends = f.Backends()
				stop = func() { embeddedShutdown(t, f.Server) }
			}
			var pid uint32
			var cycles []serverQueryLogCycle
			var secrets []string
			for _, b := range backends {
				t.Run(b.Name, func(t *testing.T) {
					id, c, s := embeddedLogScenario(t, b.DSN)
					if b.Name != "postgres" {
						pid, cycles, secrets = id, c, s
					}
				})
			}
			stop()
			if form == "writer" {
				path = filepath.Join(t.TempDir(), "query.log")
				if err := os.WriteFile(path, []byte(query.String()), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var records []map[string]any
			for _, r := range serverLogRecords(t, path) {
				if r["connection_id"] == float64(pid) {
					records = append(records, r)
				}
			}
			if len(records) != len(cycles) {
				t.Fatalf("records %d cycles %d", len(records), len(cycles))
			}
			for i, want := range cycles {
				serverWantQueryLogRecord(t, i, records[i], want)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range append(secrets, fixture.ServerPassword, "$pbkdf2") {
				if strings.Contains(string(data), secret) {
					t.Fatal("query log leaked credentials/key/verifier")
				}
			}
			if strings.Contains(operations.String(), `"message":"query"`) {
				t.Fatal("query records mirrored to operational sink")
			}
			if query.closed || operations.closed {
				t.Fatal("caller writer was closed")
			}
			normalized := embeddedNormalizedRecords(t, records)
			if baseline == nil {
				baseline = normalized
			} else if !reflect.DeepEqual(normalized, baseline) {
				t.Fatalf("%s Config/CLI logical records differ\ngot %#v\nwant %#v", form, normalized, baseline)
			}
		})
	}
	t.Run("dual", embeddedDualLogging)
}

func embeddedDualLogging(t *testing.T) {
	cfg := embeddedConfig()
	var reference fixture.EmbeddedServerBackend
	if *fixture.Mode {
		if err := fixture.EnsureStarted(); err != nil {
			t.Fatal(err)
		}
		reference = fixture.EmbeddedServerBackend{DSN: fixture.DSN}
	} else {
		// The native quick reference is an actual separately owned TCP server.
		ref := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
		reference = ref.Backends()[0]
	}
	// Match the subprocess reference contract without changing the direct DSN.
	referenceURL, err := url.Parse(reference.DSN)
	if err != nil {
		t.Fatal(err)
	}
	referenceURL.Path, referenceURL.RawQuery = "/"+fixture.ServerDatabase, "sslmode=disable"
	cfg.Accounts[0].Databases = append(cfg.Accounts[0].Databases, "embedded_unreferenced")
	cfg.QueryLog = true
	cfg.LogDir = t.TempDir()
	cfg.References = []string{fixture.ServerDatabase + "=" + referenceURL.String()}
	f := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
	direct := embeddedConnect(t, f.Ctx, reference, false)
	w := embeddedRaw(t, f.Backends()[0].DSN)
	var cycles []serverDualCycle
	run := func(c serverDualCycle, tags string) []serverFrame {
		t.Helper()
		serverWantTags(t, c.frames, tags)
		serverWantCode(t, c.frames, "")
		c.refTags = tags
		cycles = append(cycles, c)
		return c.frames
	}
	run(serverDualCycle{frontend: "Q", query: "SELECT 1", frames: w.query(t, "SELECT 1")}, "TDCZ")
	const extended = "SELECT $1::integer + 1 AS n"
	frames := run(serverDualCycle{frontend: "PBDES", query: extended, frames: serverCycle(t, w, serverParse("", extended), serverBind("", "", []int{1}, [][]byte{{0, 0, 0, 41}}), serverDescribe('P', ""), serverExecute("", 0))}, "12TDCZ")
	serverWantRaw(t, frames, [][][]byte{{[]byte("42")}})
	const create = "CREATE TABLE dual_t(v integer); INSERT INTO dual_t VALUES (1), (2)"
	run(serverDualCycle{frontend: "Q", query: create, frames: w.query(t, create)}, "CCZ")
	const copyIn = "COPY dual_t FROM STDIN"
	w.send(t, serverPacket('Q', append([]byte(copyIn), 0)))
	frames = serverThrough(t, w, 'G')
	w.send(t, append(serverPacket('d', []byte("3\n4\n")), serverPacket('c', nil)...))
	run(serverDualCycle{frontend: "Qdc", query: copyIn, frames: append(frames, w.ready(t)...)}, "GCZ")
	const total = "SELECT count(*)::integer AS n, sum(v)::integer AS s FROM dual_t"
	frames = run(serverDualCycle{frontend: "Q", query: total, frames: w.query(t, total)}, "TDCZ")
	serverWantRaw(t, frames, [][][]byte{{[]byte("4"), []byte("10")}})
	embeddedExec(t, f.Ctx, direct, "UPDATE dual_t SET v = 40 WHERE v = 4")
	frames = run(serverDualCycle{frontend: "Q", query: total, frames: w.query(t, total), diff: []string{`#1 DataRow values[1]: stuntgres "10", reference "46"`}}, "TDCZ")
	serverWantRaw(t, frames, [][][]byte{{[]byte("4"), []byte("10")}})
	embeddedInt(t, f.Ctx, direct, "SELECT sum(v)::integer FROM dual_t", 46)
	single := embeddedRaw(t, embeddedEndpoint(t, f.Server, fixture.ServerUser, fixture.ServerPassword, "embedded_unreferenced").DSN)
	serverScalar(t, single, "SELECT 42 AS answer", "42")
	embeddedTerminate(t, w)
	embeddedTerminate(t, single)
	embeddedShutdown(t, f.Server)
	var dual, plain []map[string]any
	for _, r := range serverLogRecords(t, filepath.Join(cfg.LogDir, "query.log")) {
		if r["connection_id"] == float64(w.pid) {
			dual = append(dual, r)
		}
		if r["connection_id"] == float64(single.pid) {
			plain = append(plain, r)
		}
	}
	if len(dual) != len(cycles) {
		t.Fatalf("dual records %d cycles %d", len(dual), len(cycles))
	}
	for i, c := range cycles {
		serverWantDualRecord(t, i, dual[i], c)
	}
	if len(plain) != 1 {
		t.Fatalf("no-reference records: %v", plain)
	}
	for _, key := range []string{"response_ref", "error_ref", "response_diff"} {
		if _, ok := plain[0][key]; ok {
			t.Fatalf("no-reference record includes %s", key)
		}
	}
}
