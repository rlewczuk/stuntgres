//go:build !noservertests

package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const insertDefaultFiderSix = `INSERT INTO attachments (tenant_id, comment_id, attachment_bkey)
SELECT $1, $2, $3
WHERE NOT EXISTS (
  SELECT 1 FROM attachments
  WHERE tenant_id = $4
    AND (comment_id = $5 OR ($5 IS NULL AND comment_id IS NULL))
    AND attachment_bkey = $6
)`

const insertDefaultFiderNine = `INSERT INTO attachments (tenant_id, post_id, comment_id, user_id, attachment_bkey)
SELECT $1, $2, $3, $4, $5
WHERE NOT EXISTS (
  SELECT 1 FROM attachments
  WHERE tenant_id = $6
    AND post_id = $7
    AND (comment_id = $8 OR ($8 IS NULL AND comment_id IS NULL))
    AND attachment_bkey = $9
)`

const insertDefaultAttachments = `CREATE TABLE attachments (
 id serial PRIMARY KEY, tenant_id int NOT NULL, comment_id int NULL,
 attachment_bkey varchar(512) NOT NULL
)`

const insertDefaultAttachmentsNine = `CREATE TABLE attachments (
 id serial PRIMARY KEY, tenant_id int NOT NULL, comment_id int NULL,
 attachment_bkey varchar(512) NOT NULL, post_id int, user_id int
)`

// Keep the message phase and every logical error field visible on the red checkpoint.
func insertDefaultFrames(t *testing.T, phase string, frames []serverFrame, tags string, ready byte) {
	t.Helper()
	for _, frame := range frames {
		if frame.tag == 'E' {
			t.Fatalf("%s: ErrorResponse fields=%v transcript=%s Ready=%q", phase, serverFields(frame.body), serverTags(frames), frames[len(frames)-1].body)
		}
	}
	serverWantTags(t, frames, tags)
	serverWantReady(t, frames, ready)
}

func insertDefaultParameters(t *testing.T, frames []serverFrame, oids []uint32) {
	t.Helper()
	want := server16(nil, len(oids))
	for _, oid := range oids {
		want = serverPut32(want, oid)
	}
	seen := 0
	for _, frame := range frames {
		if frame.tag == 't' {
			seen++
			if !bytes.Equal(frame.body, want) {
				t.Fatalf("ParameterDescription=%x want=%x", frame.body, want)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("ParameterDescription count=%d want=1", seen)
	}
}

func insertDefaultPrepare(t *testing.T, w *serverWire, name, query string, oids []uint32, zeroHints bool, ready byte) {
	t.Helper()
	var hints []uint32
	if zeroHints {
		hints = make([]uint32, len(oids))
	}
	frames := serverCycle(t, w, serverParse(name, query, hints...), serverDescribe('S', name))
	insertDefaultFrames(t, "Parse/Describe/Sync", frames, "1tnZ", ready)
	insertDefaultParameters(t, frames, oids)
	insertDefaultDescribe(t, w, name, oids, ready)
}

func insertDefaultDescribe(t *testing.T, w *serverWire, name string, oids []uint32, ready byte) {
	t.Helper()
	frames := serverCycle(t, w, serverDescribe('S', name))
	insertDefaultFrames(t, "repeated Describe/Sync", frames, "tnZ", ready)
	insertDefaultParameters(t, frames, oids)
}

func insertDefaultExecute(t *testing.T, w *serverWire, name string, args [][]byte, count int, ready byte) {
	t.Helper()
	// The unnamed portal is replaceable even inside BEGIN. Bind and Execute share
	// one Sync group so an autocommit Sync cannot retire a merely bound portal.
	frames := serverCycle(t, w, serverBind("", name, nil, args), serverExecute("", 0))
	insertDefaultFrames(t, "Bind/Execute/Sync", frames, "2CZ", ready)
	want := fmt.Sprintf("INSERT 0 %d\x00", count)
	if !bytes.Equal(frames[1].body, []byte(want)) {
		t.Fatalf("CommandComplete=%q want=%q", frames[1].body, want)
	}
}

func insertDefaultCommand(t *testing.T, w *serverWire, query, tag string, ready byte) {
	t.Helper()
	frames := w.query(t, query)
	insertDefaultFrames(t, query, frames, "CZ", ready)
	serverWantCompletions(t, frames, tag)
}

func insertDefaultRead(t *testing.T, w *serverWire, query string, names []string, oids []uint32, rows [][]string, ready byte) {
	t.Helper()
	frames := w.query(t, query)
	insertDefaultFrames(t, "readback "+query, frames, "T"+strings.Repeat("D", len(rows))+"CZ", ready)
	serverWantCompletions(t, frames, fmt.Sprintf("SELECT %d", len(rows)))
	columns := serverColumns(t, frames)
	if len(columns) != len(names) || len(oids) != len(names) {
		t.Fatalf("columns=%v want names=%v OIDs=%v", columns, names, oids)
	}
	for i, c := range columns {
		length := int16(-1)
		switch oids[i] {
		case 16:
			length = 1
		case 23:
			length = 4
		case 20, 1184:
			length = 8
		}
		typmod := int32(-1)
		if oids[i] == 1043 {
			typmod = 516 // varchar(512), including PostgreSQL's four-byte header.
		}
		if c.name != names[i] || c.oid != oids[i] || c.length != length || c.typmod != typmod || c.format != 0 {
			t.Fatalf("column[%d]=%+v want name=%s OID=%d length=%d typmod=%d text", i, c, names[i], oids[i], length, typmod)
		}
	}
	var raw [][][]byte
	for _, row := range rows {
		values := make([][]byte, len(row))
		for i, value := range row {
			if value != "<NULL>" {
				values[i] = []byte(value)
			}
		}
		raw = append(raw, values)
	}
	serverWantRaw(t, frames, raw)
}

func insertDefaultArgs(args ...any) [][]byte {
	out := make([][]byte, len(args))
	for i, arg := range args {
		if arg != nil {
			out[i] = []byte(fmt.Sprint(arg))
		}
	}
	return out
}

type insertDefaultFamily struct {
	name, declaration string
	oid               uint32
	first             int
}

var insertDefaultFamilies = []insertDefaultFamily{
	{"constant", "id int DEFAULT 7", 23, 7},
	{"serial", "id serial", 23, 1},
	{"nextval", "id bigint DEFAULT nextval('%s_seq')", 20, 41},
	{"now", "stamp timestamptz DEFAULT now()", 1184, 0},
}

func (family insertDefaultFamily) setup(table string) []string {
	var out []string
	declaration := family.declaration
	if family.name == "nextval" {
		out = append(out, "CREATE SEQUENCE "+table+"_seq START WITH 41")
		declaration = fmt.Sprintf(declaration, table)
	}
	return append(out, "CREATE TABLE "+table+" ("+declaration+", c int)",
		"CREATE TABLE "+table+"_source (k int)",
		"INSERT INTO "+table+"_source VALUES (1),(2),(3)")
}

type insertDefaultSource struct {
	name, suffix string
	oids         []uint32
	uses         []insertDefaultUse
}

type insertDefaultUse struct {
	args  []any
	count int
}

func insertDefaultSources(table string) []insertDefaultSource {
	return []insertDefaultSource{
		{"singleton", "SELECT $1", []uint32{23}, []insertDefaultUse{{[]any{1}, 1}, {[]any{2}, 1}}},
		{"boolean", "SELECT $1 WHERE $2", []uint32{23, 16}, []insertDefaultUse{{[]any{1, true}, 1}, {[]any{9, false}, 0}, {[]any{2, true}, 1}}},
		{"relation", "SELECT $1 FROM " + table + "_source WHERE k >= $2", []uint32{23, 23}, []insertDefaultUse{{[]any{9, 4}, 0}, {[]any{1, 3}, 1}, {[]any{2, 2}, 2}}},
		{"anti_exists", "SELECT $1 WHERE NOT EXISTS (SELECT 1 FROM " + table + " WHERE c = $2)", []uint32{23, 23}, []insertDefaultUse{{[]any{1, 1}, 1}, {[]any{1, 1}, 0}, {[]any{2, 2}, 1}}},
	}
}
