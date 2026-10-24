//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const serverCastServiceInsert = `insert into PUBLIC.SERVICE_CERTIFICATES (ID, SERVICE, CERTIFICATE, CERT_ACTIVE, VALID_DATE) values (cast($1 as uuid), $2, $3, $4, cast($5 as timestamp(6) with time zone))`
const serverCastServiceUUID = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"

func TestReferenceServerCastImplementedServiceCertificatesSmoke(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "SET TIME ZONE 'UTC'; CREATE TABLE PUBLIC.SERVICE_CERTIFICATES (ID uuid, SERVICE text, CERTIFICATE text, CERT_ACTIVE boolean, VALID_DATE timestamptz)"), "")
			frames := serverCycle(t, w, serverParse("service_insert", serverCastServiceInsert), serverDescribe('S', "service_insert"))
			serverWantTags(t, frames, "1tnZ")
			serverCastServiceParameters(t, frames, []uint32{2950, 25, 25, 16, 1184})
			serverWantReady(t, frames, 'I')

			// These are network-format expectations, independent of the server codecs.
			uuidBytes := []byte{0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8, 0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11}
			instant := time.Date(2003, 12, 31, 22, 0, 0, 123456000, time.UTC)
			epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			stampBytes := binary.BigEndian.AppendUint64(nil, uint64(instant.Sub(epoch)/time.Microsecond))
			for _, tc := range []struct {
				portal  string
				formats []int
				args    [][]byte
			}{
				{"service_a", nil, [][]byte{[]byte("{A0EEBC999C0B4EF8BB6D6BB9BD380A11}"), []byte("svc-a"), []byte("cert-a"), []byte("true"), []byte("2004-01-01 00:00:00.123456+02")}},
				{"service_b", []int{1, 0, 0, 1, 1}, [][]byte{uuidBytes, []byte("svc-b"), []byte("cert-b"), {0}, stampBytes}},
				{"service_c", nil, [][]byte{[]byte("00000000-0000-0000-0000-000000000002"), []byte("svc-c"), []byte("cert-c"), []byte("true"), []byte("2004-07-01 12:00:00.654321-04")}},
				{"service_null", nil, [][]byte{nil, []byte("svc-null"), []byte("cert-null"), []byte("false"), nil}},
			} {
				frames = serverCycle(t, w, serverBind(tc.portal, "service_insert", tc.formats, tc.args), serverDescribe('P', tc.portal), serverExecute(tc.portal, 0))
				serverWantTags(t, frames, "2nCZ")
				serverWantCompletions(t, frames, "INSERT 0 1")
				serverWantReady(t, frames, 'I')
			}
			serverCastServiceBag(t, w)
			for _, format := range []int{0, 1} {
				q := "SELECT id,valid_date,CAST(valid_date AS timestamp(6) with time zone) AS sized FROM public.service_certificates WHERE service='svc-a'"
				frames = serverCycle(t, w, serverParse("", q), serverBind("", "", nil, nil, format), serverDescribe('P', ""), serverExecute("", 0))
				want := [][][]byte{{[]byte(serverCastServiceUUID), []byte("2003-12-31 22:00:00.123456+00"), []byte("2003-12-31 22:00:00.123456+00")}}
				if format == 1 {
					want = [][][]byte{{uuidBytes, stampBytes, stampBytes}}
				}
				serverWantRaw(t, frames, want)
				cols := serverColumns(t, frames)
				if len(cols) != 3 {
					t.Fatalf("columns %+v", cols)
				}
				for i, oid := range []uint32{2950, 1184, 1184} {
					mod := int32(-1)
					if i == 2 {
						mod = 6
					}
					if cols[i].oid != oid || cols[i].typmod != mod || cols[i].format != int16(format) {
						t.Fatalf("column %d = %+v want oid %d typmod %d format %d", i, cols[i], oid, mod, format)
					}
				}
				serverWantReady(t, frames, 'I')
			}
			// Real runtime TEXT->UUID WHERE, not just target-inferred parameter input.
			q := "SELECT service FROM public.service_certificates WHERE id=CAST($1::text AS uuid) ORDER BY service"
			frames = serverCycle(t, w, serverParse("service_filter", q), serverDescribe('S', "service_filter"))
			serverComparisonDescription(t, frames, []uint32{25}, []uint32{25})
			for _, tc := range []struct {
				arg  []byte
				rows [][][]byte
			}{
				{[]byte(serverCastServiceUUID), [][][]byte{{[]byte("svc-a")}, {[]byte("svc-b")}}},
				{[]byte("00000000-0000-0000-0000-000000000002"), [][][]byte{{[]byte("svc-c")}}},
				{nil, nil},
			} {
				frames = serverCycle(t, w, serverBind("", "service_filter", nil, [][]byte{tc.arg}), serverExecute("", 0))
				serverWantRaw(t, frames, tc.rows)
				serverWantReady(t, frames, 'I')
			}
			for _, tc := range []struct{ id, date, code, message, context string }{
				{"bad", "2004-01-01+00", "22P02", `invalid input syntax for type uuid: "bad"`, "unnamed portal parameter $1 = '...'"},
				{serverCastServiceUUID, "nonsense", "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`, "unnamed portal parameter $5 = '...'"},
			} {
				frames = serverCycle(t, w, serverBind("", "service_insert", nil, [][]byte{[]byte(tc.id), []byte("bad"), []byte("cert"), []byte("true"), []byte(tc.date)}), serverExecute("", 0))
				serverWantTags(t, frames, "EZ")
				serverCastServiceError(t, frames, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: tc.context})
				serverWantReady(t, frames, 'I')
				serverCastServiceBag(t, w)
			}
			// PostgreSQL may evaluate these TEXT casts while constructing the bound plan.
			multi := `INSERT INTO public.service_certificates VALUES (CAST($1::text AS uuid),'late-a','cert',true,CAST($2::text AS timestamptz)),(CAST($3::text AS uuid),'late-b','cert',true,CAST($4::text AS timestamptz))`
			frames = serverCycle(t, w, serverParse("service_multi", multi), serverDescribe('S', "service_multi"))
			serverCastServiceParameters(t, frames, []uint32{25, 25, 25, 25})
			for _, tc := range []struct{ id, date, code, message, tags string }{
				{"bad", "2004-01-01+00", "22P02", `invalid input syntax for type uuid: "bad"`, "EZ"},
				{serverCastServiceUUID, "nonsense", "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`, "2EZ"},
			} {
				frames = serverCycle(t, w, serverBind("", "service_multi", nil, [][]byte{[]byte(serverCastServiceUUID), []byte("2004-01-01+00"), []byte(tc.id), []byte(tc.date)}), serverExecute("", 0))
				tags := tc.tags
				if !b.PG {
					tags = "2EZ"
				} // Native evaluates typed TEXT casts at Execute, not PostgreSQL Bind planning.
				serverWantTags(t, frames, tags)
				serverCastServiceError(t, frames, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				serverWantReady(t, frames, 'I')
				serverCastServiceBag(t, w)
			}
		})
	}
}

func serverCastServiceBag(t *testing.T, w *serverWire) {
	t.Helper()
	frames := w.query(t, "SELECT id,service,certificate,cert_active,valid_date FROM public.service_certificates ORDER BY service")
	serverWantRaw(t, frames, [][][]byte{
		{[]byte(serverCastServiceUUID), []byte("svc-a"), []byte("cert-a"), []byte("t"), []byte("2003-12-31 22:00:00.123456+00")},
		{[]byte(serverCastServiceUUID), []byte("svc-b"), []byte("cert-b"), []byte("f"), []byte("2003-12-31 22:00:00.123456+00")},
		{[]byte("00000000-0000-0000-0000-000000000002"), []byte("svc-c"), []byte("cert-c"), []byte("t"), []byte("2004-07-01 16:00:00.654321+00")},
		{nil, []byte("svc-null"), []byte("cert-null"), []byte("f"), nil},
	})
	cols := serverColumns(t, frames)
	if len(cols) != 5 {
		t.Fatalf("table columns %+v", cols)
	}
	for i, oid := range []uint32{2950, 25, 25, 16, 1184} {
		if cols[i].oid != oid || cols[i].typmod != -1 {
			t.Fatalf("table column %d: %+v", i, cols[i])
		}
	}
	serverWantReady(t, frames, 'I')
}

func serverCastServiceParameters(t *testing.T, frames []serverFrame, want []uint32) {
	t.Helper()
	serverWantCode(t, frames, "")
	expected := binary.BigEndian.AppendUint16(nil, uint16(len(want)))
	for _, oid := range want {
		expected = binary.BigEndian.AppendUint32(expected, oid)
	}
	seen := 0
	for _, fr := range frames {
		if fr.tag == 't' {
			seen++
			if !bytes.Equal(fr.body, expected) {
				t.Fatalf("ParameterDescription %x want %x", fr.body, expected)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("ParameterDescription count %d", seen)
	}
}

func serverCastServiceError(t *testing.T, frames []serverFrame, want fixture.Diagnostic) {
	t.Helper()
	serverWantCode(t, frames, want.Code)
	seen := 0
	for _, fr := range frames {
		if fr.tag != 'E' {
			continue
		}
		seen++
		fields := serverFields(fr.body)
		for key, value := range map[byte]string{
			'S': "ERROR", 'V': "ERROR", 'C': want.Code, 'M': want.Message,
			'D': want.Detail, 'H': want.Hint, 'P': want.Position, 'p': want.InternalPosition,
			'q': want.InternalQuery, 'W': want.Context, 's': want.Schema, 't': want.Table,
			'c': want.Column, 'd': want.DataType, 'n': want.Constraint,
		} {
			if fields[key] != value {
				t.Fatalf("diagnostic %c = %q want %q: %v", key, fields[key], value, fields)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("ErrorResponse count %d", seen)
	}
}
