package main

import (
	"encoding/json"
	"testing"
)

func TestServerQueryLogDecode(t *testing.T) {
	cstr := func(parts ...string) []byte {
		var b []byte
		for _, p := range parts {
			b = appendString(b, p)
		}
		return b
	}
	field := func(b []byte, value []byte) []byte {
		if value == nil {
			return appendInt32(b, 0xFFFFFFFF)
		}
		return append(appendInt32(b, uint32(len(value))), value...)
	}
	column := func(b []byte, name string, format int16) []byte {
		b = appendString(b, name)
		b = appendInt32(b, 16384)
		b = appendInt16(b, 2)
		b = appendInt32(b, 23)
		b = appendInt16(b, 4)
		b = appendInt32(b, 0xFFFFFFFF)
		return appendInt16(b, format)
	}
	rowDescription := column(column(appendInt16(nil, 2), "a", 0), "b", 1)
	dataRow := field(field(appendInt16(nil, 2), []byte("x\ty")), []byte{0, 0, 0, 42})
	for _, c := range []struct {
		name string
		tag  byte
		body []byte
		want string // name and decoded form, as JSON
	}{
		{"row description", 'T', rowDescription, `["RowDescription",{"columns":[{"attnum":2,"format":0,"name":"a","table_oid":16384,"type_len":4,"type_oid":23,"typmod":-1},{"attnum":2,"format":1,"name":"b","table_oid":16384,"type_len":4,"type_oid":23,"typmod":-1}]}]`},
		{"data row after binary column", 'D', dataRow, `["DataRow",{"values":["x\ty",{"hex":"0000002a"}]}]`},
		{"data row null and unprintable", 'D', field(field(appendInt16(nil, 2), nil), []byte{0xff, 'a'}), `["DataRow",{"values":[null,{"hex":"ff61"}]}]`},
		{"command complete", 'C', cstr("INSERT 0 3"), `["CommandComplete",{"tag":"INSERT 0 3"}]`},
		{"error", 'E', append([]byte("SERROR\x00C22012\x00Mdivision by zero\x00P8\x00Xextra\x00"), 0), `["ErrorResponse",{"X":"extra","code":"22012","message":"division by zero","position":"8","severity":"ERROR"}]`},
		{"notice", 'N', append([]byte("SNOTICE\x00Mhello\x00"), 0), `["NoticeResponse",{"message":"hello","severity":"NOTICE"}]`},
		{"ready", 'Z', []byte("T"), `["ReadyForQuery",{"status":"T"}]`},
		{"parameter status", 'S', cstr("TimeZone", "UTC"), `["ParameterStatus",{"name":"TimeZone","value":"UTC"}]`},
		{"notification", 'A', append(appendInt32(nil, 7), cstr("ch", "é")...), `["NotificationResponse",{"channel":"ch","payload":"é","pid":7}]`},
		{"parameter description", 't', appendInt32(appendInt32(appendInt16(nil, 2), 23), 25), `["ParameterDescription",{"type_oids":[23,25]}]`},
		{"copy in", 'G', appendInt16(appendInt16(appendInt16([]byte{0}, 2), 0), 1), `["CopyInResponse",{"column_formats":[0,1],"format":0}]`},
		{"copy out binary", 'H', appendInt16([]byte{1}, 0), `["CopyOutResponse",{"column_formats":[],"format":1}]`},
		{"copy data", 'd', []byte("1\tone\n"), `["CopyData",{"data":"1\tone\n"}]`},
		{"function result", 'V', field(nil, []byte("t")), `["FunctionCallResponse",{"value":"t"}]`},
		{"function null", 'V', field(nil, nil), `["FunctionCallResponse",{"value":null}]`},
		{"empty bodies", '1', nil, `["ParseComplete",{}]`},
		{"portal suspended", 's', nil, `["PortalSuspended",{}]`},
		{"truncated row", 'D', appendInt16(nil, 1), `["DataRow",null]`},
		{"trailing bytes", 'Z', []byte("II"), `["ReadyForQuery",null]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Formats come from the preceding RowDescription, as in a cycle.
			formats := []int16{0, 1}
			if c.name == "data row null and unprintable" {
				formats = nil
			}
			name, decoded := decodeBackend(c.tag, c.body, &formats)
			got, err := json.Marshal([]any{name, decoded})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got  %s\nwant %s", got, c.want)
			}
		})
	}
	t.Run("row description sets formats", func(t *testing.T) {
		var formats []int16
		decodeBackend('T', rowDescription, &formats)
		_, decoded := decodeBackend('D', dataRow, &formats)
		got, _ := json.Marshal(decoded)
		if string(got) != `{"values":["x\ty",{"hex":"0000002a"}]}` {
			t.Fatal(string(got))
		}
	})
}
