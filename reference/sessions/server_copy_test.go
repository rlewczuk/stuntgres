//go:build !noservertests

package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func serverCopyBinary(rows ...[][]byte) []byte {
	b := append([]byte("PGCOPY\n\xff\r\n\x00"), make([]byte, 8)...)
	for _, row := range rows {
		b = server16(b, len(row))
		for _, v := range row {
			if v == nil {
				b = serverPut32(b, ^uint32(0))
			} else {
				b = serverPut32(b, uint32(len(v)))
				b = append(b, v...)
			}
		}
	}
	return server16(b, -1)
}
func serverCopyStart(t *testing.T, w *serverWire, sql string, extended bool, format byte, columns int) {
	t.Helper()
	if extended {
		for _, p := range [][]byte{serverParse("", sql), serverBind("", "", nil, nil), serverExecute("", 0), serverPacket('H', nil)} {
			w.send(t, p)
		}
		serverWantTags(t, serverThrough(t, w, '2'), "12")
	} else {
		w.send(t, serverPacket('Q', serverCString(nil, sql)))
	}
	f := w.frame(t)
	want := server16([]byte{format}, columns)
	for range columns {
		want = server16(want, int(format))
	}
	if f.tag != 'G' || !bytes.Equal(f.body, want) {
		t.Fatalf("CopyInResponse %c %x want %x", f.tag, f.body, want)
	}
}
func serverCopyFinish(t *testing.T, w *serverWire, extended bool) []serverFrame {
	t.Helper()
	w.send(t, serverPacket('c', nil))
	if extended {
		w.send(t, serverPacket('S', nil))
	}
	return w.ready(t)
}
func TestReferenceServerCopyRoundTrip(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			w := serverAuthenticated(t, dsn)
			serverWantCode(t, w.query(t, "CREATE TABLE server_copy(id int,v text)"), "")
			raw := f.RawPool(t, b)
			tx, err := raw.BeginTx(f.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			stmt, err := tx.PrepareContext(f.Ctx, pq.CopyIn("server_copy", "id", "v"))
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, r := range []struct {
				id int
				v  any
			}{{1, "pq"}, {2, nil}, {3, ""}} {
				if _, err = stmt.ExecContext(f.Ctx, r.id, r.v); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = stmt.ExecContext(f.Ctx); err != nil {
				t.Fatal(err)
			}
			if err = stmt.Close(); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			out, err := f.PSQL(t, b, "4,\"comma,\nline\"\n", "-c", "COPY server_copy FROM STDIN (FORMAT csv)")
			if err != nil || out != "COPY 1\n" {
				t.Fatalf("psql csv %q %v", out, err)
			}
			for _, extended := range []bool{false, true} {
				serverCopyStart(t, w, "COPY server_copy FROM STDIN", extended, 0, 2)
				for _, v := range []byte("5\tfragment\n") {
					w.send(t, serverPacket('d', []byte{v}))
				}
				frames := serverCopyFinish(t, w, extended)
				serverWantTags(t, frames, "CZ")
				serverWantCompletions(t, frames, "COPY 1")
				serverWantCode(t, w.query(t, "DELETE FROM server_copy WHERE id=5"), "")
			}
			payload := serverCopyBinary([][]byte{{0, 0, 0, 5}, []byte("binary")}, [][]byte{{0, 0, 0, 6}, nil})
			serverCopyStart(t, w, "COPY server_copy FROM STDIN (FORMAT binary)", true, 1, 2)
			for _, v := range payload {
				w.send(t, serverPacket('d', []byte{v}))
			}
			serverWantCompletions(t, serverCopyFinish(t, w, true), "COPY 2")
			for _, c := range []struct {
				option string
				format byte
				want   []byte
			}{
				{"", 0, []byte("1\tpq\n2\t\\N\n3\t\n4\tcomma,\\nline\n5\tbinary\n6\t\\N\n")},
				{" (FORMAT csv)", 0, []byte("1,pq\n2,\n3,\"\"\n4,\"comma,\nline\"\n5,binary\n6,\n")},
				{" (FORMAT binary)", 1, serverCopyBinary([][]byte{{0, 0, 0, 1}, []byte("pq")}, [][]byte{{0, 0, 0, 2}, nil}, [][]byte{{0, 0, 0, 3}, {}}, [][]byte{{0, 0, 0, 4}, []byte("comma,\nline")}, [][]byte{{0, 0, 0, 5}, []byte("binary")}, [][]byte{{0, 0, 0, 6}, nil})},
			} {
				frames := w.query(t, "COPY (SELECT id,v FROM server_copy ORDER BY id) TO STDOUT"+c.option)
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, "COPY 6")
				var data []byte
				for _, fr := range frames {
					if fr.tag == 'H' {
						if len(fr.body) != 7 || fr.body[0] != c.format || binary.BigEndian.Uint16(fr.body[1:]) != 2 {
							t.Fatal("CopyOut metadata", fr)
						}
					}
					if fr.tag == 'd' {
						data = append(data, fr.body...)
					}
				}
				if !bytes.Equal(data, c.want) {
					t.Fatalf("COPY output %q want %q", data, c.want)
				}
			}
			out, err = f.PSQL(t, b, "COPY (SELECT 42) TO STDOUT;\n")
			if err != nil || out != "42\n" {
				t.Fatalf("psql output %q %v", out, err)
			}
			var null sql.NullString
			if err = raw.QueryRowContext(f.Ctx, "SELECT v FROM server_copy WHERE id=2").Scan(&null); err != nil || null.Valid {
				t.Fatalf("NULL %+v %v", null, err)
			}
			binaryOut, err := f.PSQL(t, b, "", "-c", "COPY (SELECT 42::integer) TO STDOUT (FORMAT binary)")
			expected := serverCopyBinary([][]byte{{0, 0, 0, 42}})
			if err != nil || !bytes.Equal([]byte(binaryOut), expected) {
				t.Fatalf("actual psql binary output %x want %x: %v", binaryOut, expected, err)
			}
			serverWantCode(t, w.query(t, "CREATE TABLE server_psql_binary(v int)"), "")
			out, err = f.PSQL(t, b, string(expected), "-c", "COPY server_psql_binary FROM STDIN (FORMAT binary)")
			if err != nil || out != "COPY 1\n" {
				t.Fatalf("actual psql binary input %q %v", out, err)
			}
			serverScalar(t, w, "SELECT v FROM server_psql_binary", "42")
		})
	}
}
func TestReferenceServerCopyAtomicity(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			guard := serverAuthenticated(t, dsn)
			serverWantCode(t, guard.query(t, "CREATE TABLE server_copy_atomic(v int); INSERT INTO server_copy_atomic VALUES (99)"), "")
			for _, extended := range []bool{false, true} {
				for _, c := range []struct {
					name          string
					data          []byte
					fail          bool
					code, message string
				}{
					{"late_conversion", []byte("1\nbad\n"), false, "22P02", "invalid input syntax for type integer: \"bad\""},
					{"copy_fail", []byte("1\n"), true, "57014", "COPY from stdin failed: deliberate"},
					{"marker_fail", []byte("1\n\\.\nignored"), true, "57014", "COPY from stdin failed: deliberate"},
				} {
					t.Run(c.name+map[bool]string{true: "_extended", false: "_simple"}[extended], func(t *testing.T) {
						w := serverAuthenticated(t, dsn)
						serverCopyStart(t, w, "COPY server_copy_atomic FROM STDIN", extended, 0, 1)
						w.send(t, serverPacket('d', c.data))
						if c.fail {
							w.send(t, serverPacket('f', []byte("deliberate\x00")))
						} else {
							w.send(t, serverPacket('c', nil))
						}
						if extended {
							w.send(t, serverPacket('S', nil))
						}
						frames := w.ready(t)
						serverWantTags(t, frames, "EZ")
						serverWantCode(t, frames, c.code)
						for _, fr := range frames {
							if fr.tag == 'E' && serverFields(fr.body)['M'] != c.message {
								t.Fatal(serverFields(fr.body))
							}
						}
						serverWantCompletions(t, frames)
						serverScalar(t, guard, "SELECT count(*) FROM server_copy_atomic", "1")
						serverScalar(t, w, "SELECT 42", "42")
					})
				}
			}
			w := serverAuthenticated(t, dsn)
			serverCopyStart(t, w, "COPY server_copy_atomic FROM STDIN", false, 0, 1)
			w.send(t, serverPacket('d', []byte("1\n")))
			w.Close()
			serverWantCode(t, guard.query(t, "BEGIN; LOCK TABLE server_copy_atomic IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
			serverScalar(t, guard, "SELECT count(*) FROM server_copy_atomic", "1")
		})
	}
}
func TestReferenceServerCopyRecovery(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			w := serverAuthenticated(t, dsn)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, w.query(t, "CREATE TABLE server_copy_tail(v int)"), "")
			for _, same := range []bool{false, true} {
				serverCopyStart(t, w, "COPY server_copy_tail FROM STDIN", false, 0, 1)
				data := []byte("1\n\\.\n")
				if same {
					data = append(data, []byte("bad\n")...)
				}
				w.send(t, serverPacket('d', data))
				if !same {
					w.send(t, serverPacket('d', []byte("bad\n")))
				}
				serverScalar(t, peer, "SELECT count(*) FROM server_copy_tail", "0")
				serverWantCompletions(t, serverCopyFinish(t, w, false), "COPY 1")
				serverScalar(t, peer, "SELECT count(*) FROM server_copy_tail", "1")
				serverWantCode(t, w.query(t, "TRUNCATE server_copy_tail"), "")
			}
			serverCopyStart(t, w, "COPY server_copy_tail FROM STDIN (FORMAT binary)", false, 1, 1)
			w.send(t, serverPacket('d', append(serverCopyBinary([][]byte{{0, 0, 0, 1}}), 0)))
			w.send(t, serverPacket('c', nil))
			frames := w.ready(t)
			serverWantCode(t, frames, "22P04")
			serverScalar(t, peer, "SELECT count(*) FROM server_copy_tail", "0")
			serverCopyStart(t, w, "COPY server_copy_tail FROM STDIN", true, 0, 1)
			w.send(t, serverPacket('H', nil))
			w.send(t, serverPacket('S', nil))
			w.send(t, serverPacket('f', []byte("deliberate\x00")))
			serverWantTags(t, serverThrough(t, w, 'E'), "E")
			w.send(t, serverPacket('d', []byte("ignored")))
			w.send(t, serverPacket('c', nil))
			w.send(t, serverPacket('f', []byte("ignored\x00")))
			w.send(t, serverPacket('S', nil))
			serverWantTags(t, w.ready(t), "Z")
			serverScalar(t, w, "SELECT 42", "42")
		})
	}
}
