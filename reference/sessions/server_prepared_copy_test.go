//go:build !noservertests

package reference_test

import (
	"bytes"
	"testing"

	"stuntgres/reference/fixture"
)

// These helpers invoke COPY entries created by wire Parse. SQL PREPARE COPY
// remains outside PostgreSQL's PREPARE grammar; no such SQL is synthesized.
func serverPreparedCopyStart(t *testing.T, w *serverWire, extended, from bool, format byte, columns int, invocation string) {
	t.Helper()
	if extended {
		for _, packet := range [][]byte{serverParse("wrapper", invocation), serverBind("p", "wrapper", nil, nil), serverExecute("p", 0), serverPacket('H', nil)} {
			w.send(t, packet)
		}
		serverWantTags(t, serverThrough(t, w, '2'), "12")
	} else {
		w.send(t, serverPacket('Q', serverCString(nil, invocation)))
	}
	response := w.frame(t)
	tag := byte('H')
	if from {
		tag = 'G'
	}
	expected := server16([]byte{format}, columns)
	for range columns {
		expected = server16(expected, int(format))
	}
	if response.tag != tag || !bytes.Equal(response.body, expected) {
		t.Fatalf("prepared COPY response %c %x want %c %x", response.tag, response.body, tag, expected)
	}
}

func serverPreparedCopyOutput(t *testing.T, w *serverWire, want []byte, count string) []serverFrame {
	t.Helper()
	frames := serverThrough(t, w, 'C')
	serverWantCode(t, frames, "")
	if len(frames) < 3 || frames[len(frames)-2].tag != 'c' {
		t.Fatalf("COPY output termination %s", serverTags(frames))
	}
	var data []byte
	for _, fr := range frames[:len(frames)-2] {
		if fr.tag != 'd' {
			t.Fatalf("COPY output order %s", serverTags(frames))
		}
		data = append(data, fr.body...)
	}
	if len(frames[len(frames)-2].body) != 0 {
		t.Fatal("backend CopyDone body")
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("prepared COPY bytes %x want %x", data, want)
	}
	serverWantCompletions(t, frames, count)
	return frames
}

func TestReferenceServerPreparedCopy(t *testing.T) {
	f := fixture.NewServerFixture(t)
	text := []byte("1\talpha\n2\t\\N\n3\t\n4\tcomma,\\nline\n")
	csv := []byte("1,alpha\n2,\n3,\"\"\n4,\"comma,\nline\"\n")
	binary := serverCopyBinary([][]byte{{0, 0, 0, 1}, []byte("alpha")}, [][]byte{{0, 0, 0, 2}, nil}, [][]byte{{0, 0, 0, 3}, {}}, [][]byte{{0, 0, 0, 4}, []byte("comma,\nline")})
	expectedRows := [][][]byte{{[]byte("1"), []byte("alpha")}, {[]byte("2"), nil}, {[]byte("3"), {}}, {[]byte("4"), []byte("comma,\nline")}}
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, extended := range []bool{false, true} {
				for _, from := range []bool{false, true} {
					for _, format := range []struct {
						name, option string
						code         byte
						data         []byte
					}{
						{"text", "", 0, text}, {"csv", " (FORMAT csv)", 0, csv}, {"binary", " (FORMAT binary)", 1, binary},
					} {
						route := "simple"
						if extended {
							route = "extended"
						}
						direction := "to"
						if from {
							direction = "from"
						}
						t.Run(route+"/"+direction+"/"+format.name, func(t *testing.T) {
							table := "server_prepared_copy_" + route + "_" + direction + "_" + format.name
							w := serverAuthenticated(t, dsn)
							peer := serverAuthenticated(t, dsn)
							serverWantCode(t, w.query(t, "CREATE TABLE "+table+"(id integer,v text)"), "")
							if !from {
								serverWantCode(t, w.query(t, "INSERT INTO "+table+" VALUES (1,'alpha'),(2,NULL),(3,''),(4,'comma,"+"\n"+"line')"), "")
							}
							source := "COPY (SELECT id,v FROM " + table + " ORDER BY id) TO STDOUT" + format.option
							if from {
								source = "COPY " + table + " FROM STDIN" + format.option
							}
							serverWantTags(t, serverCycle(t, w, serverParse("cp", source)), "1Z")
							if extended {
								serverWantCode(t, w.query(t, "BEGIN"), "")
							}
							serverPreparedCopyStart(t, w, extended, from, format.code, 2, "EXECUTE cp")
							if from {
								// COPY packet boundaries are independent of headers, fields, records and
								// CSV quoted newlines. Each frontend data frame here holds one byte.
								for _, v := range format.data {
									w.send(t, serverPacket('d', []byte{v}))
								}
								serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
								w.send(t, serverPacket('c', nil))
								w.send(t, serverPacket('H', nil))
								frames := serverThrough(t, w, 'C')
								serverWantTags(t, frames, "C")
								serverWantCompletions(t, frames, "COPY 4")
								if extended {
									serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
								}
							} else {
								serverPreparedCopyOutput(t, w, format.data, "COPY 4")
							}
							if extended {
								w.send(t, serverPacket('S', nil))
							}
							frames := w.ready(t)
							serverWantTags(t, frames, "Z")
							ready := byte('I')
							if extended {
								ready = 'T'
							}
							serverWantReady(t, frames, ready)
							serverWantRaw(t, w.query(t, "SELECT id,v FROM "+table+" ORDER BY id"), expectedRows)
							if extended {
								// The wrapper executed a non-row COPY strategy, not a fetchable store.
								// A savepoint permits checking 55000 without undoing the successful COPY.
								serverWantCode(t, w.query(t, "SAVEPOINT copied"), "")
								frames = serverCycle(t, w, serverExecute("p", 0))
								serverWantTags(t, frames, "EZ")
								serverNotificationError(t, frames, "55000", "portal \"p\" cannot be run", "")
								serverWantReady(t, frames, 'E')
								serverWantCode(t, w.query(t, "ROLLBACK TO copied"), "")
								serverWantTags(t, serverCycle(t, w, serverClose('P', "p")), "3Z")
								frames = w.query(t, "COMMIT")
								serverWantTags(t, frames, "CZ")
								serverWantCompletions(t, frames, "COMMIT")
								serverWantReady(t, frames, 'I')
							}
							serverWantRaw(t, peer.query(t, "SELECT id,v FROM "+table+" ORDER BY id"), expectedRows)
							serverScalar(t, w, "SELECT 42", "42")
						})
					}
				}
			}

			for _, fail := range []bool{true, false} {
				name := "copy_fail_simple"
				extended := false
				if !fail {
					name = "late_conversion_extended"
					extended = true
				}
				t.Run(name, func(t *testing.T) {
					table := "server_prepared_copy_failure_" + name
					w := serverAuthenticated(t, dsn)
					peer := serverAuthenticated(t, dsn)
					serverWantCode(t, w.query(t, "CREATE TABLE "+table+"(id integer,v text); INSERT INTO "+table+" VALUES (99,'sentinel')"), "")
					serverWantTags(t, serverCycle(t, w, serverParse("cp", "COPY "+table+" FROM STDIN")), "1Z")
					if extended {
						serverWantCode(t, w.query(t, "BEGIN"), "")
					}
					serverPreparedCopyStart(t, w, extended, true, 0, 2, "EXECUTE cp")
					if fail {
						w.send(t, serverPacket('d', []byte("1\tprefix\n")))
						w.send(t, serverPacket('f', []byte("deliberate\x00")))
					} else {
						w.send(t, serverPacket('d', []byte("1\tprefix\nbad\tfail\n")))
					}
					frames := serverThrough(t, w, 'E')
					serverWantTags(t, frames, "E")
					if fail {
						serverNotificationError(t, frames, "57014", "COPY from stdin failed: deliberate", "", "COPY "+table+", line 2")
					} else {
						serverNotificationError(t, frames, "22P02", "invalid input syntax for type integer: \"bad\"", "", "COPY "+table+", line 2, column id: \"bad\"")
					}
					serverWantCompletions(t, frames)
					// Late COPY input is discarded. Extended failures also suppress ordinary
					// queued work until a future Sync, without publishing the valid prefix.
					for _, packet := range [][]byte{serverPacket('d', []byte("77\tlate\n")), serverPacket('c', nil), serverPacket('f', []byte("late\x00"))} {
						w.send(t, packet)
					}
					if extended {
						for _, packet := range [][]byte{serverParse("ignored", "INSERT INTO "+table+" VALUES (77,'skipped')"), serverBind("ignored", "ignored", nil, nil), serverExecute("ignored", 0), serverPacket('H', nil), serverPacket('S', nil)} {
							w.send(t, packet)
						}
					}
					frames = append(frames, w.ready(t)...)
					serverWantTags(t, frames, "EZ")
					ready := byte('I')
					if extended {
						ready = 'E'
					}
					serverWantReady(t, frames, ready)
					serverWantCompletions(t, frames)
					serverWantRaw(t, peer.query(t, "SELECT id,v FROM "+table+" ORDER BY id"), [][][]byte{{[]byte("99"), []byte("sentinel")}})
					if extended {
						serverWantCode(t, w.query(t, "ROLLBACK"), "")
					}
					serverScalar(t, w, "SELECT 42", "42")
				})
			}

			t.Run("copy_to_explicit_formal_expression_once", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				serverWantCode(t, w.query(t, "CREATE SEQUENCE server_prepared_copy_argument START 1"), "")
				// Explicit unused formals still require SQL EXECUTE's canonical argument
				// importer. The source uses the already-admitted direct COPY SELECT path.
				serverWantTags(t, serverCycle(t, w, serverParse("cp", "COPY (SELECT 42 AS id) TO STDOUT", 23)), "1Z")
				serverScalar(t, w, "SELECT is_called FROM server_prepared_copy_argument", "f")
				frames := w.query(t, "EXECUTE cp")
				serverWantTags(t, frames, "EZ")
				serverWantReady(t, frames, 'I')
				serverWantCode(t, frames, "42601")
				fields := serverFields(frames[0].body)
				for key, want := range map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "42601", 'M': "wrong number of parameters for prepared statement \"cp\"", 'D': "Expected 1 parameters but got 0.", 'P': "", 'H': "", 'W': "", 'p': "", 'q': "", 's': "", 't': "", 'c': "", 'd': "", 'n': ""} {
					if fields[key] != want {
						t.Fatalf("SQL EXECUTE count diagnostic field %c=%q want %q: %v", key, fields[key], want, fields)
					}
				}
				serverScalar(t, w, "SELECT 42", "42")
				serverScalar(t, w, "SELECT is_called FROM server_prepared_copy_argument", "f")
				frames = w.query(t, "EXECUTE cp('bad')")
				serverWantTags(t, frames, "EZ")
				serverWantReady(t, frames, 'I')
				serverNotificationError(t, frames, "22P02", "invalid input syntax for type integer: \"bad\"", "12")
				serverScalar(t, w, "SELECT 42", "42")
				serverScalar(t, w, "SELECT is_called FROM server_prepared_copy_argument", "f")
				serverPreparedCopyStart(t, w, false, false, 0, 1, "EXECUTE cp(nextval('server_prepared_copy_argument')::integer)")
				serverPreparedCopyOutput(t, w, []byte("42\n"), "COPY 1")
				frames = w.ready(t)
				serverWantTags(t, frames, "Z")
				serverWantReady(t, frames, 'I')
				serverScalar(t, w, "SELECT last_value FROM server_prepared_copy_argument", "1")
				serverScalar(t, w, "SELECT 42", "42")
				serverScalar(t, w, "SELECT last_value FROM server_prepared_copy_argument", "1")
			})
		})
	}
}
