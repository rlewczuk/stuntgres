//go:build !noservertests

package reference_test

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerPreparedLogs(t *testing.T) {
	f := fixture.NewServerFixture(t)
	const source = "SELECT 10 / $1::integer /* é phase2-log-source */"
	const mixed = "SELECT $1::integer / v, $2::boolean, $3::text, $4::text, $5::bytea FROM server_log_denominator /* other portal */"
	const bad = "SELECT $1::integer /* failed Bind source */"
	var secrets []string
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverDial(t, f.DSNFor(b))
			serverWantCode(t, w.login(t, f.DSNFor(b), 196610), "")
			if !b.PG {
				secrets = append(secrets, base64.StdEncoding.EncodeToString(w.key), hex.EncodeToString(w.key))
			}
			serverWantCode(t, w.query(t, "CREATE TABLE server_log_denominator(v int); INSERT INTO server_log_denominator VALUES (0)"), "")
			frames := serverCycle(t, w, serverParse("s", source), serverBind("p", "s", nil, [][]byte{[]byte("0")}), serverClose('S', "s"), serverExecute("p", 0))
			serverNotificationError(t, frames, "22012", "division by zero", "")
			for _, p := range [][]byte{serverParse("mixed", mixed), serverBind("other", "mixed", []int{1, 1, 0, 0, 1}, [][]byte{{0, 0, 0, 42}, {1}, nil, {}, {0, 255}}), serverPacket('H', nil)} {
				w.send(t, p)
			}
			serverWantTags(t, serverThrough(t, w, '2'), "12")
			frames = serverCycle(t, w, serverClose('S', "mixed"), serverExecute("other", 0))
			serverWantTags(t, frames, "3EZ")
			serverNotificationError(t, frames, "22012", "division by zero", "")
			frames = serverCycle(t, w, serverParse("bad", bad), serverBind("bad", "bad", nil, [][]byte{[]byte("bad")}))
			serverWantCode(t, frames, "22P02")
			serverScalar(t, w, "SELECT 42", "42")
		})
	}
	f.Stop(t)
	for _, file := range []string{"error.log", "server.log"} {
		records := serverLogRecords(t, filepath.Join(f.Logdir, file))
		for _, want := range []struct {
			source, code string
			params       []any
		}{
			{source, "22012", []any{map[string]any{"index": float64(1), "oid": float64(23), "type": "integer", "format": "text", "null": false, "encoding": "text", "value": "0"}}},
			{mixed, "22012", []any{map[string]any{"index": float64(1), "oid": float64(23), "type": "integer", "format": "binary", "null": false, "encoding": "text", "value": "42"}, map[string]any{"index": float64(2), "oid": float64(16), "type": "boolean", "format": "binary", "null": false, "encoding": "text", "value": "t"}, map[string]any{"index": float64(3), "oid": float64(25), "type": "text", "format": "text", "null": true, "encoding": "text", "value": nil}, map[string]any{"index": float64(4), "oid": float64(25), "type": "text", "format": "text", "null": false, "encoding": "text", "value": ""}, map[string]any{"index": float64(5), "oid": float64(17), "type": "bytea", "format": "binary", "null": false, "encoding": "text", "value": "\\x00ff"}}},
			{bad, "22P02", []any{map[string]any{"index": float64(1), "oid": float64(23), "type": "integer", "format": "text", "null": false, "encoding": "text", "value": "bad"}}},
		} {
			found := false
			for _, record := range records {
				if record["query"] == want.source && record["error"] == want.code {
					found = true
					if !reflect.DeepEqual(record["params"], want.params) {
						t.Fatalf("%s parameters %+v want %+v", file, record["params"], want.params)
					}
				}
			}
			if !found && file == "error.log" {
				t.Fatalf("%s missing source %q", file, want.source)
			}
		}
		data, err := os.ReadFile(filepath.Join(f.Logdir, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range append(secrets, fixture.ServerPassword, "wrong-password", "$pbkdf2") {
			if strings.Contains(string(data), secret) {
				t.Fatal("secret in SQL log")
			}
		}
	}
	for _, secret := range append(secrets, fixture.ServerPassword, "$pbkdf2") {
		if strings.Contains(f.Output(), secret) {
			t.Fatal("secret in stdout")
		}
	}
}
func TestReferenceServerPhase2Smoke(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			db := f.RawPool(t, b)
			if _, err := db.ExecContext(f.Ctx, "CREATE TABLE server_phase2_smoke(id int,v text)"); err != nil {
				t.Fatal(err)
			}
			result, err := db.ExecContext(f.Ctx, "INSERT INTO server_phase2_smoke VALUES ($1,$2)", 1, "bound")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatalf("count %d %v", n, err)
			}
			stmt, err := db.PrepareContext(f.Ctx, "SELECT v FROM server_phase2_smoke WHERE id=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for range 2 {
				var got string
				if err := stmt.QueryRowContext(f.Ctx, 1).Scan(&got); err != nil || got != "bound" {
					t.Fatalf("held %q %v", got, err)
				}
			}
			out, err := f.PSQL(t, b, "COPY server_phase2_smoke FROM STDIN;\n2\tpsql\n\\.\nSELECT id,v FROM server_phase2_smoke ORDER BY id;\n")
			if err != nil || out != "COPY 1\n1|bound\n2|psql\n" {
				t.Fatalf("actual psql COPY %q %v", out, err)
			}
			l := serverAuthenticated(t, f.DSNFor(b))
			s := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, l.query(t, "LISTEN server_smoke"), "")
			serverWantCode(t, s.query(t, "NOTIFY server_smoke, 'idle'"), "")
			serverWantNotification(t, l.frame(t), s.pid, "server_smoke", "idle")
		})
	}
}
