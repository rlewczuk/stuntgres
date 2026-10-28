//go:build !noservertests

package reference_test

import (
	"bytes"
	"database/sql"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
	"stuntgres/reference/fixture"
)

type clientEncodingCopyCase struct{ name, encoding, option, raw, unicode string }

var clientEncodingCopyCases = []clientEncodingCopyCase{
	{"latin1", "LATIN1", "", "\xe9", "é"},
	{"sjis_trail_slash", "SJIS", "", "\x83\x5c\\tend", "ソ\tend"},
	{"gb18030_four", "GB18030", "", "\x84\x30\x9c\x38", "飯"},
	{"jis2004_combined", "EUC_JIS_2004", "", "\xa5\xf7", "カ゚"},
	{"explicit_utf8", "LATIN1", ", ENCODING 'UTF8'", "é", "é"},
	{"explicit_sjis", "LATIN1", ", ENCODING 'SJIS'", "\x83\x5c", "ソ"},
}

var clientEncodingCopyOutputCases = []struct {
	q     string
	want  []byte
	count int64
}{
	{`COPY (SELECT U&'\00E9' AS U&"\00E9") TO STDOUT (FORMAT csv, HEADER, NULL U&'\00E9')`, []byte("\"\xe9\"\n\"\xe9\"\n"), 1},
	{`COPY (SELECT NULL::text AS v) TO STDOUT (NULL U&'\00E9')`, []byte("\xe9\n"), 1},
	{`COPY (SELECT 'a|#b'::text AS v) TO STDOUT (FORMAT csv, DELIMITER '|', QUOTE '#', ESCAPE '#')`, []byte("#a|##b#\n"), 1},
}

var clientEncodingCopyOptionCases = []struct{ option, code, message, positionToken, tags string }{
	{"ENCODING 'unknown'", "22023", `argument to option "encoding" must be a valid encoding name`, "ENCODING", "EZ"},
	{"ENCODING 'MULE_INTERNAL'", "42883", `default conversion function for encoding "UTF8" to "MULE_INTERNAL" does not exist`, "", "HEZ"},
	{"ENCODING 'LATIN1', ENCODING 'UTF8'", "42601", "conflicting or redundant options", "ENCODING 'UTF8'", "EZ"},
	{"ENCODING", "42601", "encoding requires a parameter", "", "EZ"},
}

func TestReferenceClientEncodingCopy(t *testing.T) {
	t.Run("wire", func(t *testing.T) {
		f := fixture.NewServerFixture(t)
		for _, b := range f.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				dsn := f.DSNFor(b)
				w := serverAuthenticated(t, dsn)
				clientEncodingCommand(t, w, "CREATE TABLE ce_copy(v text)", "CREATE TABLE")
				for _, c := range clientEncodingCopyCases {
					for _, format := range []string{"text", "csv"} {
						raw := c.raw
						if format == "csv" {
							raw = strings.ReplaceAll(raw, "\\t", "\t")
						}
						for split := 0; split <= len(raw); split++ {
							for _, extended := range []bool{false, true} {
								clientEncodingCommand(t, w, "SET client_encoding='"+c.encoding+"'; TRUNCATE ce_copy", "SET", "TRUNCATE TABLE")
								serverCopyStart(t, w, "COPY ce_copy FROM STDIN (FORMAT "+format+c.option+")", extended, 0, 1)
								for _, chunk := range []string{raw[:split], raw[split:] + "\n"} {
									w.send(t, serverPacket('d', []byte(chunk)))
								}
								frames := serverCopyFinish(t, w, extended)
								serverWantTags(t, frames, "CZ")
								serverWantCompletions(t, frames, "COPY 1")
								clientEncodingStatus(t, frames, "client_encoding")
								// An independently UTF8 peer proves Unicode storage, not roundtrip parity.
								peer := serverAuthenticated(t, dsn)
								serverScalar(t, peer, "SELECT v FROM ce_copy", c.unicode)
								peer.Close()
								frames = w.query(t, "COPY ce_copy TO STDOUT (FORMAT "+format+c.option+")")
								clientEncodingCopyBytes(t, frames, []byte(raw+"\n"), "COPY 1")
								serverScalar(t, w, "SHOW client_encoding", c.encoding)
							}
						}
					}
				}
				t.Run("csv_header_null_empty_quotes", func(t *testing.T) {
					clientEncodingCommand(t, w, "SET client_encoding='LATIN1'; TRUNCATE ce_copy", "SET", "TRUNCATE TABLE")
					serverCopyStart(t, w, `COPY ce_copy FROM STDIN (FORMAT csv, HEADER MATCH, NULL 'nil')`, false, 0, 1)
					input := []byte("v\n\xe9\nnil\n\"\"\n\"nil\"\n\"a,\"\"b\"\n")
					for _, v := range input {
						w.send(t, serverPacket('d', []byte{v}))
					}
					serverWantCompletions(t, serverCopyFinish(t, w, false), "COPY 5")
					frames := w.query(t, `COPY (SELECT v FROM ce_copy ORDER BY v NULLS FIRST) TO STDOUT (FORMAT csv, HEADER, NULL 'nil')`)
					// Empty is not NULL when NULL is the nonempty marker nil.
					clientEncodingCopyBytes(t, frames, []byte("v\nnil\n\n\"a,\"\"b\"\n\"nil\"\n\xe9\n"), "COPY 5")
				})
				for _, c := range clientEncodingCopyOutputCases {
					clientEncodingCopyBytes(t, w.query(t, c.q), c.want, "COPY 1")
				}
				for _, c := range []struct{ name, encoding, raw, code, message, context string }{
					{"escape_after_conversion", "LATIN1", "ok\n\\351\n", "22021", `invalid byte sequence for encoding "UTF8": 0xe9`, "COPY ce_copy, line 2: \"\\351\""},
					{"truncated", "SJIS", "ok\n\x82", "22021", `invalid byte sequence for encoding "SJIS": 0x82`, "COPY ce_copy, line 2"},
					{"malformed", "SJIS", "ok\n\x82\x20\n", "22021", `invalid byte sequence for encoding "SJIS": 0x82 0x20`, "COPY ce_copy, line 2"},
					{"nul", "LATIN1", "ok\n\x00\n", "22021", `invalid byte sequence for encoding "LATIN1": 0x00`, "COPY ce_copy, line 2"},
					{"unmapped", "GB18030", "ok\n\x84\x31\xa5\x30\n", "22P05", `character with byte sequence 0x84 0x31 0xa5 0x30 in encoding "GB18030" has no equivalent in encoding "UTF8"`, "COPY ce_copy, line 2"},
				} {
					t.Run(c.name, func(t *testing.T) {
						for _, ignore := range []string{"", ", ON_ERROR ignore"} {
							clientEncodingCommand(t, w, "TRUNCATE ce_copy; SET client_encoding='"+c.encoding+"'", "TRUNCATE TABLE", "SET")
							serverCopyStart(t, w, "COPY ce_copy FROM STDIN (FORMAT text"+ignore+")", false, 0, 1)
							w.send(t, serverPacket('d', []byte(c.raw)))
							frames := serverCopyFinish(t, w, false)
							serverWantTags(t, frames, "EZ")
							clientEncodingError(t, frames, c.code, c.message, c.context)
							serverScalar(t, w, "SELECT count(*) FROM ce_copy", "0")
							serverScalar(t, w, "SELECT 42", "42")
						}
					})
				}
				t.Run("end_marker_defers_prefetched_error", func(t *testing.T) {
					clientEncodingCommand(t, w, "TRUNCATE ce_copy; SET client_encoding='SJIS'", "TRUNCATE TABLE", "SET")
					serverCopyStart(t, w, "COPY ce_copy FROM STDIN", false, 0, 1)
					w.send(t, serverPacket('d', []byte("ok\n\\.\n\x82")))
					serverWantCompletions(t, serverCopyFinish(t, w, false), "COPY 1")
					serverScalar(t, w, "SELECT v FROM ce_copy", "ok")
				})
				t.Run("copyfail_reason", func(t *testing.T) {
					clientEncodingCommand(t, w, "TRUNCATE ce_copy; SET client_encoding='LATIN1'", "TRUNCATE TABLE", "SET")
					serverCopyStart(t, w, "COPY ce_copy FROM STDIN", false, 0, 1)
					w.send(t, serverPacket('d', []byte("ok\n")))
					w.send(t, serverPacket('f', []byte("\xe9\x00")))
					frames := w.ready(t)
					clientEncodingError(t, frames, "57014", "COPY from stdin failed: \xe9", "COPY ce_copy, line 2")
					serverScalar(t, w, "SELECT count(*) FROM ce_copy", "0")
					serverScalar(t, w, "SELECT 42", "42")
				})
				t.Run("late_output_failure", func(t *testing.T) {
					clientEncodingCommand(t, w, "TRUNCATE ce_copy; SET client_encoding='LATIN1'; INSERT INTO ce_copy VALUES('ok'),(U&'\\20AC')", "TRUNCATE TABLE", "SET", "INSERT 0 2")
					frames := w.query(t, "COPY ce_copy TO STDOUT")
					serverWantTags(t, frames, "HdEZ")
					clientEncodingError(t, frames, "22P05", clientEncodingEuroError, "")
					var data []byte
					for _, fr := range frames {
						if fr.tag == 'd' {
							data = append(data, fr.body...)
						}
					}
					if !bytes.Equal(data, []byte("ok\n")) {
						t.Fatalf("partial current COPY row %s", clientEncodingTranscript(frames))
					}
					serverScalar(t, w, "SELECT 42", "42")
				})
				for _, c := range clientEncodingCopyOptionCases {
					q := "COPY ce_copy TO STDOUT (" + c.option + ")"
					frames := clientEncodingQuery(t, w, q)
					serverWantTags(t, frames, c.tags)
					clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': c.code, 'M': c.message, 'P': clientEncodingCopyOptionPosition(q, c.positionToken)})
					serverWantCompletions(t, frames)
					serverScalar(t, w, "SELECT 42", "42")
				}
			})
		}
	})
	t.Run("public_stream", func(t *testing.T) { clientEncodingStreamCopy(t) })
}

// Source chunks are owned independently; no reader returns a borrowed wire frame.
type clientEncodingChunks struct{ chunks [][]byte }

func (c *clientEncodingChunks) Read(p []byte) (int, error) {
	for len(c.chunks) > 0 {
		if len(c.chunks[0]) == 0 {
			c.chunks = c.chunks[1:]
			continue
		}
		n := copy(p, c.chunks[0])
		c.chunks[0] = c.chunks[0][n:]
		return n, nil
	}
	return 0, io.EOF
}

func clientEncodingStreamCopy(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pool := p.Native
			if b.PG {
				pool = p.CopyPostgres
			}
			conn, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			exec := func(q string) {
				if _, err := conn.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			exec("CREATE TABLE ce_stream(v text)")
			for _, c := range clientEncodingCopyCases {
				for _, format := range []string{"text", "csv"} {
					raw := c.raw
					if format == "csv" {
						raw = strings.ReplaceAll(raw, "\\t", "\t")
					}
					for split := 0; split <= len(raw); split++ {
						exec("TRUNCATE ce_stream")
						exec("SET client_encoding='" + c.encoding + "'")
						r, err := fixture.CopyFrom(p.Ctx, conn, b.PG, "COPY ce_stream FROM STDIN (FORMAT "+format+c.option+")", &clientEncodingChunks{[][]byte{[]byte(raw[:split]), []byte(raw[split:] + "\n")}})
						if err != nil || r != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) {
							t.Fatalf("%s split%d: %+v %v", c.name, split, r, err)
						}
						var out bytes.Buffer
						r, err = clientEncodingStreamTo(p, conn, b.PG, "COPY ce_stream TO STDOUT (FORMAT "+format+c.option+")", &out)
						if err != nil || r.Count != 1 || r.CommandTag != "COPY 1" || out.String() != raw+"\n" {
							t.Fatalf("%s output %x %+v %v", c.name, out.Bytes(), r, err)
						}
						exec("SET client_encoding='UTF8'")
						var value string
						if err := conn.QueryRowContext(p.Ctx, "SELECT v FROM ce_stream").Scan(&value); err != nil || value != c.unicode {
							t.Fatalf("Unicode %q want %q: %v", value, c.unicode, err)
						}
					}
				}
			}
			exec("SET client_encoding='LATIN1'")
			exec("TRUNCATE ce_stream")
			r, err := fixture.CopyFrom(p.Ctx, conn, b.PG, "COPY ce_stream FROM STDIN", strings.NewReader("ok\n\\351\n"))
			if r != (stuntgres.CopyResult{}) {
				t.Fatalf("failed COPY published %+v", r)
			}
			fixture.State(t, err, "22021")
			var count int
			if err := conn.QueryRowContext(p.Ctx, "SELECT count(*) FROM ce_stream").Scan(&count); err != nil || count != 0 {
				t.Fatalf("input prefix count%d %v", count, err)
			}
			clientEncodingStreamEdges(t, p, b, conn)
		})
	}
}

func clientEncodingStreamTo(p *fixture.Pair, c *sql.Conn, pg bool, q string, dst io.Writer) (stuntgres.CopyResult, error) {
	if !pg {
		return stuntgres.CopyTo(p.Ctx, c, q, dst)
	}
	var result stuntgres.CopyResult
	err := c.Raw(func(raw any) error {
		tag, err := raw.(*stdlib.Conn).Conn().PgConn().CopyTo(p.Ctx, dst, q)
		if err == nil {
			result = stuntgres.CopyResult{Count: tag.RowsAffected(), CommandTag: tag.String()}
		}
		return err
	})
	return result, err
}

// COPY option locations belong to the particular DefElem: the invalid-name
// option, or the second redundant option. Conversion admission and defGetString
// errors have no parser position. These offsets are from the literal query,
// never from a backend diagnostic.
func clientEncodingCopyOptionPosition(query, token string) string {
	if token == "" {
		return ""
	}
	return strconv.Itoa(strings.Index(query, token) + 1)
}
