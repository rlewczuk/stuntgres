package reference_test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

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
func serverDumpBarrier(t *testing.T, f *fixture.ServerFixture, db string, count int) {
	t.Helper()
	f.WaitEvent(t, func(v map[string]any) bool {
		data, _ := json.Marshal(v)
		text := strings.ToLower(string(data))
		if !strings.Contains(text, "dump") || !strings.Contains(text, strings.ToLower(db)) || strings.Contains(text, "error") {
			return false
		}
		entries, e := os.ReadDir(f.Dumpdir)
		if e != nil {
			return false
		}
		n := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), db+"--") && strings.HasSuffix(entry.Name(), ".sql") {
				n++
			}
		}
		return n >= count
	})
}
func serverCLIWant(t *testing.T, f *fixture.ServerFixture, args ...string) string {
	t.Helper()
	out, e := f.RunCLI(t, args...)
	if e != nil {
		t.Fatalf("%v: %v\n%s", args, e, out)
	}
	return out
}

func TestReferenceServerPersistence(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name+"_seed", func(t *testing.T) {
			for _, c := range []struct{ db, want string }{{fixture.ServerDatabase, "11"}, {fixture.ServerSecondDatabase, "22"}} {
				w := serverAuthenticated(t, f.DSNForDatabase(b, c.db))
				serverWantCode(t, w.query(t, "CREATE TABLE server_persist(v integer); INSERT INTO server_persist VALUES ("+c.want+")"), "")
				serverScalar(t, w, "SELECT v FROM server_persist", c.want)
			}
			out, e := f.PSQL(t, b, "SELECT v FROM server_persist;")
			if e != nil || strings.TrimSpace(out) != "11" {
				t.Fatalf("psql seed: %q %v", out, e)
			}
		})
	}
	native, ok := f.NativeBackend()
	if !ok {
		return
	}
	primary := f.NativeDB(t, fixture.ServerDatabase)
	for i := range 2 {
		serverCLIWant(t, f, "dump", "-p", f.PIDFile)
		for _, db := range []string{fixture.ServerDatabase, fixture.ServerSecondDatabase} {
			serverDumpBarrier(t, f, db, i+1)
			files := serverDumpFiles(t, f.Dumpdir, db)
			if len(files) != i+1 {
				t.Fatalf("dump %d %s: files %v", i, db, files)
			}
		}
	}
	// Replay the actual exporter bytes on the pinned PostgreSQL connection, not
	// an independently manufactured SQL snapshot.
	for _, b := range f.Backends() {
		if !b.PG {
			continue
		}
		for _, c := range []struct {
			db   string
			want int
		}{{fixture.ServerDatabase, 11}, {fixture.ServerSecondDatabase, 22}} {
			files := serverDumpFiles(t, f.Dumpdir, c.db)
			sqlBytes, e := os.ReadFile(files[len(files)-1])
			if e != nil {
				t.Fatal(e)
			}
			serverExec(t, f.Ctx, b.DB, "DROP TABLE IF EXISTS server_persist CASCADE")
			out, e := f.PSQL(t, b, string(sqlBytes)+"\nSELECT v FROM server_persist;")
			if e != nil {
				t.Fatalf("native SQL replay: %v\n%s", e, out)
			}
			if !strings.HasSuffix(strings.TrimSpace(out), fmt.Sprint(c.want)) {
				t.Fatalf("replay %s: %q", c.db, out)
			}
			serverExec(t, f.Ctx, b.DB, "DROP TABLE server_persist")
		}
	}
	open, e := primary.Conn(f.Ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer open.Close()
	if _, e = open.ExecContext(f.Ctx, "BEGIN; INSERT INTO server_persist VALUES (99)"); e != nil {
		t.Fatal(e)
	}
	f.Stop(t)
	for _, db := range []string{fixture.ServerDatabase, fixture.ServerSecondDatabase} {
		if files := serverDumpFiles(t, f.Dumpdir, db); len(files) != 3 {
			t.Fatalf("final dump %s: %v", db, files)
		}
	}
	f.Restart(t, true)
	primary = f.NativeDB(t, fixture.ServerDatabase)
	second := f.NativeDB(t, fixture.ServerSecondDatabase)
	serverInt(t, f.Ctx, primary, "SELECT count(*) FROM server_persist", 1)
	serverInt(t, f.Ctx, primary, "SELECT v FROM server_persist", 11)
	serverInt(t, f.Ctx, second, "SELECT v FROM server_persist", 22)
	out, e := f.PSQL(t, native, "SELECT v FROM server_persist;")
	if e != nil || strings.TrimSpace(out) != "11" {
		t.Fatalf("restored psql: %q %v", out, e)
	}
	f.Stop(t)
	files := serverDumpFiles(t, f.Dumpdir, fixture.ServerDatabase)
	newest := files[len(files)-1]
	saved, e := os.ReadFile(newest)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []struct {
		name string
		sql  []byte
	}{{"corrupt_newest", []byte("this is not SQL;\n")}, {"missing_owner", append(saved, []byte("\nALTER TABLE public.server_persist OWNER TO server_missing_owner;\n")...)}} {
		t.Run(bad.name, func(t *testing.T) {
			if e := os.WriteFile(newest, bad.sql, 0600); e != nil {
				t.Fatal(e)
			}
			out, e := f.RunCLI(t, "start", "-l", f.Address, "-u", f.Userfile, "-d", f.Dumpdir, "-p", f.PIDFile, "-r")
			if e == nil {
				t.Fatalf("invalid restore startup succeeded: %s", out)
			}
			if _, e = os.Stat(f.PIDFile); !os.IsNotExist(e) {
				t.Fatalf("failed startup retained PID: %v", e)
			}
			if c, e := net.DialTimeout("tcp", f.Address, time.Second); e == nil {
				c.Close()
				t.Fatal("failed restore left listener")
			}
		})
	}
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
func TestReferenceServerLogs(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			q := "SELECT FROM /* exact original syntax source */"
			serverWantCode(t, w.query(t, q), "42601")
			unsupported := "COPY (SELECT 1) TO '/server-contract-no-such-dir/out'"
			frames := w.query(t, unsupported)
			if !b.PG {
				serverWantCode(t, frames, "0A000")
			} else {
				serverWantCode(t, frames, "58P01")
			}
			serverScalar(t, w, "SELECT 42", "42")
		})
	}
	b, ok := f.NativeBackend()
	if !ok {
		return
	}
	bad := f.AuthDSN(t, b, fixture.ServerUser, "wrong-password", fixture.ServerDatabase)
	w := serverDial(t, bad)
	serverWantCode(t, w.login(t, bad, 196608), "28P01")
	f.Stop(t)
	errors := serverLogRecords(t, filepath.Join(f.Logdir, "error.log"))
	for _, want := range []struct{ query, code, kind string }{{"SELECT FROM /* exact original syntax source */", "42601", "SYNTAX"}, {"COPY (SELECT 1) TO '/server-contract-no-such-dir/out'", "0A000", "UNSUPPORTED"}} {
		found := false
		for _, record := range errors {
			if record["query"] != want.query {
				continue
			}
			found = true
			if record["severity"] != "ERROR" || record["kind"] != want.kind || record["error"] != want.code || record["errmsg"] == "" {
				t.Fatal(record)
			}
			params, ok := record["params"].([]any)
			if !ok || len(params) != 0 {
				t.Fatal(record)
			}
		}
		if !found {
			t.Fatalf("missing SQL error %s", want.query)
		}
	}
	operations := serverLogRecords(t, filepath.Join(f.Logdir, "server.log"))
	for _, term := range []string{"start", "connection", "auth", "stop"} {
		found := false
		for _, record := range operations {
			data, _ := json.Marshal(record)
			if strings.Contains(strings.ToLower(string(data)), term) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing operation %s", term)
		}
	}
	for _, file := range []string{"server.log", "error.log"} {
		data, e := os.ReadFile(filepath.Join(f.Logdir, file))
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(data), fixture.ServerPassword) || strings.Contains(string(data), "wrong-password") || strings.Contains(string(data), "$pbkdf2") {
			t.Fatal("log discloses password")
		}
	}
}

func TestReferenceServerDaemon(t *testing.T) {
	f := fixture.NewServerFixture(t)
	b, ok := f.NativeBackend()
	if !ok {
		for _, oracle := range f.Backends() {
			serverScalar(t, serverAuthenticated(t, f.DSNFor(oracle)), "SELECT 42", "42")
		}
		return
	}
	f.Stop(t)
	args := []string{"start", "-l", f.Address, "-u", f.Userfile, "-d", f.Dumpdir, "-L", f.Logdir, "-p", f.PIDFile}
	serverCLIWant(t, f, args...)
	t.Cleanup(func() { f.RunCLI(t, "stop", "-p", f.PIDFile) })
	w := serverAuthenticated(t, f.DSNFor(b))
	serverScalar(t, w, "SELECT 42", "42")
	serverWantCode(t, w.query(t, "CREATE TABLE server_daemon(v integer); INSERT INTO server_daemon VALUES (42)"), "")
	bytes, e := os.ReadFile(f.PIDFile)
	if e != nil {
		t.Fatal(e)
	}
	var pid map[string]any
	if e = json.Unmarshal(bytes, &pid); e != nil {
		t.Fatal(e)
	}
	number, ok := pid["pid"].(float64)
	if !ok || number <= 0 || int(number) == os.Getpid() || pid["executable"] == "" || pid["start_time"] == nil {
		t.Fatal(pid)
	}
	if _, e = os.Stat(fmt.Sprintf("/proc/%d", int(number))); e != nil {
		t.Fatal(e)
	}
	out, e := f.RunCLI(t, args...)
	if e == nil {
		t.Fatalf("conflicting daemon succeeded: %s", out)
	}
	serverScalar(t, w, "SELECT v FROM server_daemon", "42")
	serverCLIWant(t, f, "dump", "-p", f.PIDFile)
	serverCLIWant(t, f, "stop", "-p", f.PIDFile)
	if _, e = os.Stat(f.PIDFile); !os.IsNotExist(e) {
		t.Fatalf("PID not removed: %v", e)
	}
	if c, e := net.DialTimeout("tcp", f.Address, time.Second); e == nil {
		c.Close()
		t.Fatal("stop returned with listener active")
	}
	if len(serverDumpFiles(t, f.Dumpdir, fixture.ServerDatabase)) == 0 {
		t.Fatal("daemon stop did not dump")
	}
	// A child that cannot bind must report failure, not readiness success.
	listener, e := net.Listen("tcp", f.Address)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	out, e = f.RunCLI(t, args...)
	if e == nil {
		t.Fatalf("listen failure reported success: %s", out)
	}
	if _, e = os.Stat(f.PIDFile); !os.IsNotExist(e) {
		t.Fatalf("failed daemon retained PID: %v", e)
	}
	listener.Close()
	before := len(serverDumpFiles(t, f.Dumpdir, fixture.ServerDatabase))
	serverCLIWant(t, f, "start", "-l", f.Address, "-u", f.Userfile, "-p", f.PIDFile)
	withoutDump := serverAuthenticated(t, f.DSNFor(b))
	serverCLIWant(t, f, "dump", "-p", f.PIDFile)
	serverScalar(t, withoutDump, "SELECT 42", "42")
	serverCLIWant(t, f, "stop", "-p", f.PIDFile)
	if got := len(serverDumpFiles(t, f.Dumpdir, fixture.ServerDatabase)); got != before {
		t.Fatalf("no-dumpdir control produced files: %d -> %d", before, got)
	}
}
