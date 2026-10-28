package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLWireCopy(t *testing.T) {
	t.Run("wire", xmlWire)
	t.Run("copy", func(t *testing.T) { xmlScenario(t, xmlCopy) })
}

func xmlWire(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			cfg, e := pgconn.ParseConfig(f.DSNFor(b))
			if e != nil {
				t.Fatal(e)
			}
			cfg.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
				notices = append(notices, fixture.ExtractDiagnostic(t, (*pgconn.PgError)(n)))
			}
			c, e := pgconn.ConnectConfig(f.Ctx, cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close(f.Ctx)
			for _, pf := range []int16{0, 1} {
				for _, rf := range []int16{0, 1} {
					for _, input := range [][]byte{[]byte("<a>é</a>"), nil, []byte("")} {
						r := c.ExecParams(f.Ctx, "SELECT $1::xml AS v", [][]byte{input}, []uint32{142}, []int16{pf}, []int16{rf}).Read()
						if r.Err != nil {
							t.Fatal(r.Err)
						}
						xmlWireField(t, r.FieldDescriptions, 142, -1, -1, rf)
						if len(r.Rows) != 1 || len(r.Rows[0]) != 1 || !bytes.Equal(r.Rows[0][0], input) || (r.Rows[0][0] == nil) != (input == nil) {
							t.Fatalf("wire NULL/payload pf%d rf%d got %#v want %q", pf, rf, r.Rows, input)
						}
					}
					input := []byte(`<?xml version="1.0" encoding="LATIN1"?><a>é</a>`)
					if pf == 1 {
						input = []byte("<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>")
					}
					r := c.ExecParams(f.Ctx, "SELECT $1::xml AS v", [][]byte{input}, []uint32{142}, []int16{pf}, []int16{rf}).Read()
					if r.Err != nil {
						t.Fatal(r.Err)
					}
					xmlWireField(t, r.FieldDescriptions, 142, -1, -1, rf)
					if len(r.Rows) != 1 || len(r.Rows[0]) != 1 || string(r.Rows[0][0]) != "<a>é</a>" {
						t.Fatalf("wire encoding %#v", r.Rows)
					}
					r = c.ExecParams(f.Ctx, "SELECT $1::xml AS v", [][]byte{[]byte("<a>")}, []uint32{142}, []int16{pf}, []int16{rf}).Read()
					d := xmlError("2200N", "invalid XML content", xmlBadA, 0)
					d.Context = "unnamed portal parameter $1"
					if pf == 0 {
						d.Context += " = '...'"
					}
					fixture.AssertDiagnostic(t, r.Err, &d)
					r = c.ExecParams(f.Ctx, "SELECT $1::xml AS v", [][]byte{[]byte("<healthy/>")}, []uint32{142}, []int16{pf}, []int16{rf}).Read()
					if r.Err != nil || len(r.Rows) != 1 || string(r.Rows[0][0]) != "<healthy/>" {
						t.Fatalf("wire recovery %#v", r)
					}
				}
			}
			description, e := c.Prepare(f.Ctx, "xml_wire_held", "SELECT $1::xml AS v", []uint32{142})
			if e != nil {
				t.Fatal(e)
			}
			if len(description.ParamOIDs) != 1 || description.ParamOIDs[0] != 142 {
				t.Fatalf("XML parameter descriptor %+v", description)
			}
			xmlWireField(t, description.Fields, 142, -1, -1, 0)
			for _, pf := range []int16{0, 1} {
				for _, rf := range []int16{0, 1} {
					for _, input := range [][]byte{[]byte("<held/>"), nil, []byte("")} {
						r := c.ExecPrepared(f.Ctx, "xml_wire_held", [][]byte{input}, []int16{pf}, []int16{rf}).Read()
						if r.Err != nil || len(r.Rows) != 1 || len(r.Rows[0]) != 1 || !bytes.Equal(r.Rows[0][0], input) || (r.Rows[0][0] == nil) != (input == nil) {
							t.Fatalf("held wire result %#v want %q", r, input)
						}
						xmlWireField(t, r.FieldDescriptions, 142, -1, -1, rf)
					}
					r := c.ExecPrepared(f.Ctx, "xml_wire_held", [][]byte{[]byte("<a>")}, []int16{pf}, []int16{rf}).Read()
					d := xmlError("2200N", "invalid XML content", xmlBadA, 0)
					d.Context = "unnamed portal parameter $1"
					if pf == 0 {
						d.Context += " = '...'"
					}
					fixture.AssertDiagnostic(t, r.Err, &d)
				}
			}
			if _, e := c.Exec(f.Ctx, "CREATE DOMAIN xml_wire_text AS text").ReadAll(); e != nil {
				t.Fatal(e)
			}
			for _, tc := range []struct {
				q    string
				oid  uint32
				size int16
				mod  int32
			}{
				{`SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS name) AS v`, 19, 64, -1},
				{`SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS varchar(9)) AS v`, 1043, -1, 13},
				{`SELECT XMLSERIALIZE(CONTENT '<a/>'::xml AS xml_wire_text) AS v`, 25, -1, -1},
			} {
				r := c.ExecParams(f.Ctx, tc.q, nil, nil, nil, []int16{0}).Read()
				if r.Err != nil {
					t.Fatal(r.Err)
				}
				xmlWireField(t, r.FieldDescriptions, tc.oid, tc.size, tc.mod, 0)
				if len(r.Rows) != 1 || string(r.Rows[0][0]) != "<a/>" {
					t.Fatal(r.Rows)
				}
			}
			xmlWireEncoding(t, f, c)
			xmlNamedPortalMode(t, f, c)
			if len(notices) != 0 {
				t.Fatalf("unexpected wire notices %#v", notices)
			}
			if _, e := c.Exec(f.Ctx, "SET XML OPTION DOCUMENT").ReadAll(); e != nil {
				t.Fatal(e)
			}
			for _, rf := range []int16{0, 1} {
				r := c.ExecParams(f.Ctx, "SELECT $1::xml::text AS v", [][]byte{[]byte(xmlAdjacency)}, []uint32{142}, []int16{0}, []int16{rf}).Read()
				if r.Err != nil || len(r.Rows) != 1 || string(r.Rows[0][0]) != xmlAdjacency || len(notices) != 0 {
					t.Fatalf("incidental XML parameter output notices %#v result %#v", notices, r)
				}
				r = c.ExecParams(f.Ctx, "SELECT $1::xml AS v", [][]byte{[]byte(xmlAdjacency)}, []uint32{142}, []int16{0}, []int16{rf}).Read()
				if r.Err != nil || len(r.Rows) != 1 || string(r.Rows[0][0]) != xmlAdjacency {
					t.Fatalf("typed wire output %#v", r)
				}
				xmlWireField(t, r.FieldDescriptions, 142, -1, -1, rf)
				d := xmlError("XX001", "could not parse XML declaration in stored value", "Space required.", 0)
				d.Severity, d.SeverityUnlocalized = "WARNING", "WARNING"
				if len(notices) != 1 || notices[0] != d {
					t.Fatalf("typed wire notices %#v", notices)
				}
				notices = nil
			}
		})
	}
}

func xmlWireEncoding(t *testing.T, f *fixture.ServerFixture, c *pgconn.PgConn) {
	t.Helper()
	// Text labels do not reinterpret decoded characters. Binary XML admits only
	// PostgreSQL encoding names: this new caller envelope remains proposed until
	// the root exercises it against the pinned PostgreSQL fixture.
	input := []byte(`<?xml version="1.0" encoding="UTF-16"?><a/>`)
	for _, rf := range []int16{0, 1} {
		r := c.ExecParams(f.Ctx, "SELECT $1::xml AS v", [][]byte{input}, []uint32{142}, []int16{0}, []int16{rf}).Read()
		if r.Err != nil || len(r.Rows) != 1 || len(r.Rows[0]) != 1 || !bytes.Equal(r.Rows[0][0], []byte("<a/>")) {
			t.Fatalf("text UTF-16 label rf%d result %#v", rf, r)
		}
		xmlWireField(t, r.FieldDescriptions, 142, -1, -1, rf)
		r = c.ExecParams(f.Ctx, "SELECT $1::xml AS v", [][]byte{input}, []uint32{142}, []int16{1}, []int16{rf}).Read()
		d := xmlError("22023", `invalid encoding name "UTF-16"`, "", 0)
		d.Context = "unnamed portal parameter $1"
		fixture.AssertDiagnostic(t, r.Err, &d)
	}
	if _, e := c.Exec(f.Ctx, "SET client_encoding='UTF8'; CREATE TABLE xml_wire_encoding(v xml)").ReadAll(); e != nil {
		t.Fatal(e)
	}
	original := []byte("<a>é</a>")
	insert := c.ExecParams(f.Ctx, "INSERT INTO xml_wire_encoding VALUES($1)", [][]byte{original}, []uint32{142}, []int16{0}, nil).Read()
	if insert.Err != nil || insert.CommandTag.String() != "INSERT 0 1" {
		t.Fatalf("UTF8 XML encoding seed %#v", insert)
	}
	for _, encoding := range []string{"LATIN1", "SQL_ASCII", "UTF8"} {
		if _, e := c.Exec(f.Ctx, "SET client_encoding='"+encoding+"'").ReadAll(); e != nil {
			t.Fatal(e)
		}
		for _, rf := range []int16{0, 1} {
			// Read an already stored UTF8 value so changed client encoding cannot
			// reinterpret the parameter used to construct the datum.
			r := c.ExecParams(f.Ctx, "SELECT (SELECT v FROM xml_wire_encoding) AS v", nil, nil, nil, []int16{rf}).Read()
			want := original
			if encoding == "LATIN1" {
				want = []byte("<a>\xe9</a>")
				if rf == 1 {
					want = []byte("<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>")
				}
			}
			if r.Err != nil || len(r.Rows) != 1 || len(r.Rows[0]) != 1 || !bytes.Equal(r.Rows[0][0], want) {
				t.Fatalf("XML client encoding %s rf%d result %#v want %q", encoding, rf, r, want)
			}
			xmlWireField(t, r.FieldDescriptions, 142, -1, -1, rf)
		}
	}
	if _, e := c.Exec(f.Ctx, "SET client_encoding='UTF8'").ReadAll(); e != nil {
		t.Fatal(e)
	}
}

func xmlNamedPortalMode(t *testing.T, f *fixture.ServerFixture, c *pgconn.PgConn) {
	t.Helper()
	if deadline, ok := f.Ctx.Deadline(); ok {
		if e := c.Conn().SetDeadline(deadline); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		name, portal string
		oid          uint32
		documentBind bool
	}{
		{"xml_text_execute_mode", "portal25", 25, false},
		{"xml_typed_bind_mode", "portal142", 142, false},
		{"xml_document_bind", "xml_doc_portal", 142, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			send := func(messages ...pgproto3.FrontendMessage) {
				t.Helper()
				for _, m := range messages {
					c.Frontend().Send(m)
				}
				if e := c.Frontend().Flush(); e != nil {
					t.Fatal(e)
				}
			}
			receive := func() pgproto3.BackendMessage {
				t.Helper()
				for {
					m, e := c.ReceiveMessage(f.Ctx)
					if e != nil {
						t.Fatal(e)
					}
					if _, ok := m.(*pgproto3.ParameterStatus); ok {
						continue
					}
					return m
				}
			}
			ready := func(status byte) {
				t.Helper()
				m := receive()
				r, ok := m.(*pgproto3.ReadyForQuery)
				if !ok || r.TxStatus != status {
					t.Fatalf("XML portal ReadyForQuery %#v want %c", m, status)
				}
			}
			fields := func() {
				t.Helper()
				m := receive()
				r, ok := m.(*pgproto3.RowDescription)
				if !ok || len(r.Fields) != 1 {
					t.Fatalf("XML portal descriptor %#v", m)
				}
				v := r.Fields[0]
				if string(v.Name) != "x" || v.TableOID != 0 || v.TableAttributeNumber != 0 || v.DataTypeOID != 142 || v.DataTypeSize != -1 || v.TypeModifier != -1 || v.Format != 0 {
					t.Fatalf("XML portal field %+v", v)
				}
			}
			simple := func(q, tag string) {
				t.Helper()
				rs, e := c.Exec(f.Ctx, q).ReadAll()
				if e != nil {
					t.Fatal(q, e)
				}
				if len(rs) != 1 || rs[0].CommandTag.String() != tag {
					t.Fatalf("XML portal utility %s results %#v want %s", q, rs, tag)
				}
			}
			mode := "CONTENT"
			if tc.documentBind {
				mode = "DOCUMENT"
			}
			simple("SET XML OPTION "+mode, "SET")
			simple("BEGIN", "BEGIN")
			rolledBack := false
			defer func() {
				if !rolledBack {
					if _, e := c.Exec(f.Ctx, "ROLLBACK").ReadAll(); e != nil {
						t.Error(e)
					}
				}
			}()
			send(&pgproto3.Parse{Name: tc.name, Query: "SELECT $1::xml AS x", ParameterOIDs: []uint32{tc.oid}}, &pgproto3.Describe{ObjectType: 'S', Name: tc.name}, &pgproto3.Sync{})
			if m := receive(); m == nil {
				t.Fatal("missing ParseComplete")
			} else if _, ok := m.(*pgproto3.ParseComplete); !ok {
				t.Fatalf("XML portal Parse %#v", m)
			}
			m := receive()
			params, ok := m.(*pgproto3.ParameterDescription)
			if !ok || len(params.ParameterOIDs) != 1 || params.ParameterOIDs[0] != tc.oid {
				t.Fatalf("XML portal parameters %#v want OID%d", m, tc.oid)
			}
			fields()
			ready('T')
			bind := &pgproto3.Bind{DestinationPortal: tc.portal, PreparedStatement: tc.name, ParameterFormatCodes: []int16{0}, Parameters: [][]byte{[]byte("text<a/>")}, ResultFormatCodes: []int16{0}}
			if tc.documentBind {
				send(bind, &pgproto3.Sync{})
				m := receive()
				failure, ok := m.(*pgproto3.ErrorResponse)
				if !ok {
					t.Fatalf("XML DOCUMENT Bind must fail at Bind, got %#v", m)
				}
				d := xmlError("2200M", "invalid XML document", xmlFragmentDetail, 0)
				d.Context = `portal "xml_doc_portal" parameter $1 = '...'`
				fixture.AssertDiagnostic(t, pgconn.ErrorResponseToPgError(failure), &d)
				ready('E')
			} else {
				send(bind, &pgproto3.Describe{ObjectType: 'P', Name: tc.portal}, &pgproto3.Sync{})
				if m := receive(); m == nil {
					t.Fatal("missing BindComplete")
				} else if _, ok := m.(*pgproto3.BindComplete); !ok {
					t.Fatalf("XML CONTENT Bind %#v", m)
				}
				fields()
				ready('T')
				simple("SET XML OPTION DOCUMENT", "SET")
				send(&pgproto3.Execute{Portal: tc.portal}, &pgproto3.Sync{})
				if tc.oid == 25 {
					m := receive()
					failure, ok := m.(*pgproto3.ErrorResponse)
					if !ok {
						t.Fatalf("TEXT-to-XML must fail at Execute, got %#v", m)
					}
					d := xmlError("2200M", "invalid XML document", xmlFragmentDetail, 0)
					fixture.AssertDiagnostic(t, pgconn.ErrorResponseToPgError(failure), &d)
					ready('E')
				} else {
					m := receive()
					row, ok := m.(*pgproto3.DataRow)
					if !ok || len(row.Values) != 1 || string(row.Values[0]) != "text<a/>" {
						t.Fatalf("XML Bind-accepted payload %#v", m)
					}
					m = receive()
					command, ok := m.(*pgproto3.CommandComplete)
					if !ok || string(command.CommandTag) != "SELECT 1" {
						t.Fatalf("XML portal completion %#v", m)
					}
					ready('T')
				}
			}
			simple("ROLLBACK", "ROLLBACK")
			rolledBack = true
			healthy := c.ExecParams(f.Ctx, "SELECT '<healthy/>'::xml AS v", nil, nil, nil, []int16{0}).Read()
			if healthy.Err != nil || len(healthy.Rows) != 1 || len(healthy.Rows[0]) != 1 || string(healthy.Rows[0][0]) != "<healthy/>" {
				t.Fatalf("XML portal recovery %#v", healthy)
			}
		})
	}
}

func xmlWireField(t *testing.T, fields []pgconn.FieldDescription, oid uint32, size int16, mod int32, format int16) {
	t.Helper()
	if len(fields) != 1 {
		t.Fatal(fields)
	}
	f := fields[0]
	if f.Name != "v" || f.DataTypeOID != oid || f.DataTypeSize != size || f.TypeModifier != mod || f.Format != format || f.TableOID != 0 || f.TableAttributeNumber != 0 {
		t.Fatalf("XML wire descriptor %+v", f)
	}
}

func xmlCopyBinary(values ...[]byte) []byte {
	var out bytes.Buffer
	out.WriteString("PGCOPY\n\xff\r\n\x00")
	binary.Write(&out, binary.BigEndian, int32(0))
	binary.Write(&out, binary.BigEndian, int32(0))
	for _, v := range values {
		binary.Write(&out, binary.BigEndian, int16(1))
		n := int32(len(v))
		if v == nil {
			n = -1
		}
		binary.Write(&out, binary.BigEndian, n)
		if v != nil {
			out.Write(v)
		}
	}
	binary.Write(&out, binary.BigEndian, int16(-1))
	return out.Bytes()
}

func xmlCopy(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml_copy(v xml)", 0))
	var notices []fixture.Diagnostic
	stream := fixture.CopyNoticeConnection(t, p, b.PG, &notices, nil)
	// CopyNoticeConnection is the notice-capable equivalent of Pair.CopyPostgres.
	// Also borrow Pair.CopyPostgres explicitly to cover the ordinary public stream.
	ordinary := p.Native
	if b.PG {
		ordinary = p.CopyPostgres
	}
	plain, e := ordinary.Conn(p.Ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer plain.Close()
	latin := []byte("<?xml version=\"1.0\" encoding=\"LATIN1\"?><a>\xe9</a>")
	input := xmlCopyBinary(latin, []byte("<b/>"), nil, []byte(""))
	result, e := fixture.CopyFrom(p.Ctx, plain, b.PG, "COPY xml_copy FROM STDIN (FORMAT binary)", bytes.NewReader(input))
	if e != nil {
		t.Fatal(e)
	}
	if result.Count != 4 || result.CommandTag != "COPY 4" {
		t.Fatal(result)
	}
	want := xmlSelect("SELECT v AS v FROM xml_copy", "XML", "<a>é</a>", "<b/>", nil, "")
	// COPY preserves stream order; no SQL ordering assertion depends on heap order.
	want.Bag = true
	xmlBoth(t, p, b, c, want)
	copyTo := func(conn *sql.Conn, q string) ([]byte, stuntgres.CopyResult, error) {
		var out bytes.Buffer
		var result stuntgres.CopyResult
		var err error
		if !b.PG {
			result, err = stuntgres.CopyTo(p.Ctx, conn, q, &out)
		} else {
			err = conn.Raw(func(raw any) error {
				tag, e := raw.(*stdlib.Conn).Conn().PgConn().CopyTo(p.Ctx, &out, q)
				if e == nil {
					result = stuntgres.CopyResult{Count: tag.RowsAffected(), CommandTag: tag.String()}
				}
				return e
			})
		}
		return out.Bytes(), result, err
	}
	got, result, e := copyTo(stream, "COPY (SELECT v FROM xml_copy ORDER BY COALESCE(v::text,'~')) TO STDOUT (FORMAT binary)")
	if e != nil {
		t.Fatal(e)
	}
	expected := xmlCopyBinary([]byte(""), []byte("<a>é</a>"), []byte("<b/>"), nil)
	if !bytes.Equal(got, expected) || result.Count != 4 || result.CommandTag != "COPY 4" {
		t.Fatalf("binary COPY framing got %q result %+v want %q", got, result, expected)
	}
	for _, format := range []string{"binary", "text"} {
		bad := xmlCopyBinary([]byte("<first/>"), []byte("<a>"))
		if format == "text" {
			bad = []byte("<first/>\n<a>\n")
		}
		_, err := fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY xml_copy FROM STDIN (FORMAT "+format+")", bytes.NewReader(bad))
		d := xmlError("2200N", "invalid XML content", xmlBadA, 0)
		// Full text and binary COPY envelopes are pinned-PostgreSQL observations.
		d.Context = "COPY xml_copy, line 2, column v"
		if format == "text" {
			d.Context += `: "<a>"`
		}
		fixture.AssertDiagnostic(t, err, &d)
		xmlBoth(t, p, b, c, want)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml_copy VALUES(XMLPARSE(DOCUMENT '"+xmlAdjacency+"'))", 1))
	got, result, e = copyTo(stream, "COPY (SELECT v FROM xml_copy WHERE v::text = '"+xmlAdjacency+"') TO STDOUT")
	if e != nil {
		t.Fatal(e)
	}
	if string(got) != xmlAdjacency+"\n" || result.Count != 1 {
		t.Fatalf("typed COPY output %q %+v", got, result)
	}
	d := xmlError("XX001", "could not parse XML declaration in stored value", "Space required.", 0)
	d.Severity, d.SeverityUnlocalized = "WARNING", "WARNING"
	if len(notices) != 1 || notices[0] != d {
		t.Fatalf("typed COPY notices %#v", notices)
	}
	notices = nil
	got, _, e = copyTo(stream, "COPY (SELECT v::text FROM xml_copy WHERE v::text = '"+xmlAdjacency+"') TO STDOUT")
	if e != nil || string(got) != xmlAdjacency+"\n" || len(notices) != 0 {
		t.Fatalf("raw COPY output %q notices %#v error %v", got, notices, e)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, xmlSelect("SELECT '<healthy/>'::xml AS v", "XML", "<healthy/>"))
}
