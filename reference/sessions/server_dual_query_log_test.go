//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/base64"
	"net"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

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

func TestReferenceServerDualQueryLog(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	f := fixture.NewServerFixture(t, fixture.ServerDualReference(), fixture.ServerArgs(
		"--reference", fixture.ServerSecondDatabase+"=postgres://"+fixture.ServerUser+"@"+closed+"/absent",
		"--reference-timeout", "3s"))
	native, _ := f.NativeBackend()
	dsn := f.DSNFor(native)
	ref := f.ReferenceDB
	w := serverAuthenticated(t, dsn)
	var cycles []serverDualCycle
	run := func(c serverDualCycle, tags string) []serverFrame {
		t.Helper()
		serverWantTags(t, c.frames, tags)
		serverWantCode(t, c.frames, "")
		if c.refTags == "" {
			c.refTags = tags
		}
		cycles = append(cycles, c)
		return c.frames
	}
	refExec := func(q string) {
		t.Helper()
		if _, err := ref.ExecContext(f.Ctx, q); err != nil {
			t.Fatalf("reference %s: %v", q, err)
		}
	}

	// Matching cycles: simple, extended with a binary parameter, COPY.
	const (
		simple   = "SELECT 1 AS a, 'x'::text AS b, NULL::integer AS c"
		extended = "SELECT $1::integer + 1 AS n"
		create   = "CREATE TABLE dual_t(v integer); INSERT INTO dual_t VALUES (1), (2)"
		copyIn   = "COPY dual_t FROM STDIN"
		total    = "SELECT count(*)::integer AS n, sum(v)::integer AS s FROM dual_t"
		count    = "SELECT count(*)::integer AS n FROM dual_t"
		locked   = "SELECT count(*)::integer AS n FROM dual_lock"
	)
	run(serverDualCycle{frontend: "Q", query: simple, frames: w.query(t, simple)}, "TDCZ")
	rows := run(serverDualCycle{frontend: "PBDES", query: extended, frames: serverCycle(t, w,
		serverParse("", extended), serverBind("", "", []int{1}, [][]byte{{0, 0, 0, 41}}), serverDescribe('P', ""), serverExecute("", 0))}, "12TDCZ")
	serverWantRaw(t, rows, [][][]byte{{[]byte("42")}})
	run(serverDualCycle{frontend: "Q", query: create, frames: w.query(t, create)}, "CCZ")
	w.send(t, serverPacket('Q', append([]byte(copyIn), 0)))
	frames := serverThrough(t, w, 'G')
	w.send(t, append(serverPacket('d', []byte("3\n")), serverPacket('c', nil)...))
	run(serverDualCycle{frontend: "Qdc", query: copyIn, frames: append(frames, w.ready(t)...)}, "GCZ")
	w.send(t, bytes.Join([][]byte{serverParse("", copyIn), serverBind("", "", nil, nil), serverExecute("", 0)}, nil))
	frames = serverThrough(t, w, 'G')
	w.send(t, bytes.Join([][]byte{serverPacket('d', []byte("4\n")), serverPacket('c', nil), serverPacket('S', nil)}, nil))
	run(serverDualCycle{frontend: "PBEdcS", query: copyIn, frames: append(frames, w.ready(t)...)}, "12GCZ")
	rows = run(serverDualCycle{frontend: "Q", query: total, frames: w.query(t, total)}, "TDCZ")
	serverWantRaw(t, rows, [][][]byte{{[]byte("4"), []byte("10")}})

	// A value changed only on the reference.
	refExec("UPDATE dual_t SET v = 40 WHERE v = 4")
	run(serverDualCycle{frontend: "Q", query: total, frames: w.query(t, total),
		diff: []string{`#1 DataRow values[1]: stuntgres "10", reference "46"`}}, "TDCZ")

	// A table missing only on the reference.
	refExec("DROP TABLE dual_t")
	run(serverDualCycle{frontend: "Q", query: count, frames: w.query(t, count), refTags: "EZ", refCode: "42P01",
		diff: []string{
			`stuntgres only #0 RowDescription {"columns":[{"attnum":0,"format":0,"name":"n","table_oid":0,"type_len":4,"type_oid":23,"typmod":-1}]}`,
			`stuntgres only #1 DataRow {"values":["4"]}`,
			"stuntgres only #2 CommandComplete SELECT 1",
			`reference only #0 ErrorResponse 42P01 relation "dual_t" does not exist`,
		}}, "TDCZ")

	// A reference blocked past --reference-timeout is canceled; the client
	// still receives stuntgres's rows once the reference has answered.
	run(serverDualCycle{frontend: "Q", query: "CREATE TABLE dual_lock(v integer)", frames: w.query(t, "CREATE TABLE dual_lock(v integer)")}, "CZ")
	holder, err := ref.Conn(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := holder.BeginTx(f.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(f.Ctx, "LOCK TABLE dual_lock IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	rows = run(serverDualCycle{frontend: "Q", query: locked, frames: w.query(t, locked), refTags: "EZ", refCode: "57014",
		diffPrefix: []string{"stuntgres only #0 RowDescription", "stuntgres only #1 DataRow", "stuntgres only #2 CommandComplete SELECT 1",
			"reference only #0 ErrorResponse 57014 canceling statement due to user request"}}, "TDCZ")
	serverWantRaw(t, rows, [][][]byte{{[]byte("0")}})
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}
	serverScalar(t, w, "SELECT 7 AS seven", "7")
	cycles = append(cycles, serverDualCycle{frontend: "Q", query: "SELECT 7 AS seven", refTags: "TDCZ"})

	// An unreachable reference never blocks its database's clients.
	unreachable := serverAuthenticated(t, f.DSNForDatabase(native, fixture.ServerSecondDatabase))
	serverWantRaw(t, unreachable.query(t, "SELECT 1 AS a"), [][][]byte{{[]byte("1")}})
	f.WaitEvent(t, func(e map[string]any) bool {
		return e["message"] == "reference connection failed" && e["database"] == fixture.ServerSecondDatabase
	})
	// A database without a reference keeps single-backend records.
	single := serverAuthenticated(t, f.DSNForDatabase(native, fixture.ServerUser))
	serverWantRaw(t, single.query(t, "SELECT 42 AS answer"), [][][]byte{{[]byte("42")}})
	for _, c := range []*serverWire{w, unreachable, single} {
		c.send(t, serverPacket('X', nil))
	}
	f.Stop(t)

	records := map[uint32][]map[string]any{}
	for _, r := range serverLogRecords(t, filepath.Join(f.Logdir, "query.log")) {
		id, _ := r["connection_id"].(float64)
		records[uint32(id)] = append(records[uint32(id)], r)
	}
	dual := records[w.pid]
	if len(dual) != len(cycles) {
		t.Fatalf("%d dual records, want %d: %v", len(dual), len(cycles), dual)
	}
	for i, want := range cycles {
		serverWantDualRecord(t, i, dual[i], want)
	}
	if r := records[unreachable.pid]; len(r) != 1 || r[0]["error"] != "" || r[0]["error_ref"] != "08001" {
		t.Fatalf("unreachable reference records %v", r)
	} else {
		reference := r[0]["response_ref"].([]any)
		decoded := reference[0].(map[string]any)["decoded"].(map[string]any)
		message, _ := decoded["message"].(string)
		if len(reference) != 1 || decoded["severity"] != "FATAL" || !strings.HasPrefix(message, "reference connection failed: ") {
			t.Fatalf("unreachable reference response %v", reference)
		}
		diff, _ := r[0]["response_diff"].([]any)
		if len(diff) != 5 || diff[3] != `stuntgres only #3 ReadyForQuery {"status":"I"}` || !strings.HasPrefix(diff[4].(string), "reference only #0 ErrorResponse 08001 reference connection failed: ") {
			t.Fatalf("unreachable reference diff %v", diff)
		}
	}
	if r := records[single.pid]; len(r) != 1 || r[0]["query"] != "SELECT 42 AS answer" {
		t.Fatalf("single-backend records %v", r)
	} else {
		for _, key := range []string{"response_ref", "error_ref", "response_diff"} {
			if _, ok := r[0][key]; ok {
				t.Fatalf("single-backend record has %s: %v", key, r[0])
			}
		}
	}
	if strings.Contains(f.Output(), fixture.ServerPassword) {
		t.Fatal("reference password in server output")
	}
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
