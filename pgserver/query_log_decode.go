package pgserver

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"unicode"
	"unicode/utf8"
)

func backendNames(tag byte) (string, bool) {
	switch tag {
	case '1':
		return "ParseComplete", true
	case '2':
		return "BindComplete", true
	case '3':
		return "CloseComplete", true
	case 'A':
		return "NotificationResponse", true
	case 'C':
		return "CommandComplete", true
	case 'c':
		return "CopyDone", true
	case 'd':
		return "CopyData", true
	case 'D':
		return "DataRow", true
	case 'E':
		return "ErrorResponse", true
	case 'G':
		return "CopyInResponse", true
	case 'H':
		return "CopyOutResponse", true
	case 'I':
		return "EmptyQueryResponse", true
	case 'K':
		return "BackendKeyData", true
	case 'n':
		return "NoData", true
	case 'N':
		return "NoticeResponse", true
	case 'R':
		return "Authentication", true
	case 's':
		return "PortalSuspended", true
	case 'S':
		return "ParameterStatus", true
	case 't':
		return "ParameterDescription", true
	case 'T':
		return "RowDescription", true
	case 'v':
		return "NegotiateProtocolVersion", true
	case 'V':
		return "FunctionCallResponse", true
	case 'W':
		return "CopyBothResponse", true
	case 'Z':
		return "ReadyForQuery", true
	default:
		return "", false
	}
}

func diagnosticFieldNames(tag byte) (string, bool) {
	switch tag {
	case 'S':
		return "severity", true
	case 'V':
		return "severity_unlocalized", true
	case 'C':
		return "code", true
	case 'M':
		return "message", true
	case 'D':
		return "detail", true
	case 'H':
		return "hint", true
	case 'P':
		return "position", true
	case 'p':
		return "internal_position", true
	case 'q':
		return "internal_query", true
	case 'W':
		return "context", true
	case 's':
		return "schema", true
	case 't':
		return "table", true
	case 'c':
		return "column", true
	case 'd':
		return "data_type", true
	case 'n':
		return "constraint", true
	case 'F':
		return "file", true
	case 'L':
		return "line", true
	case 'R':
		return "routine", true
	default:
		return "", false
	}
}

// hexValue is the readable form of bytes that are binary or not printable text.
type hexValue struct {
	Hex string `json:"hex"`
}

// logReader consumes a backend body; any short read clears ok.
type logReader struct {
	body []byte
	ok   bool
}

func (r *logReader) next(n int) []byte {
	if !r.ok || n < 0 || n > len(r.body) {
		r.ok = false
		return nil
	}
	b := r.body[:n]
	r.body = r.body[n:]
	return b
}
func (r *logReader) int16() int16 {
	b := r.next(2)
	if b == nil {
		return 0
	}
	return int16(binary.BigEndian.Uint16(b))
}
func (r *logReader) uint32() uint32 {
	b := r.next(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}
func (r *logReader) cstring() string {
	end := bytes.IndexByte(r.body, 0)
	if !r.ok || end < 0 {
		r.ok = false
		return ""
	}
	return string(r.next(end + 1)[:end])
}

// decodeBackend returns a backend message's name and readable form, or a nil
// form for a malformed body. formats tracks the latest RowDescription so that
// later binary DataRow columns are shown as hex.
func decodeBackend(tag byte, body []byte, formats *[]int16) (string, any) {
	r := &logReader{body: body, ok: true}
	var decoded any
	switch tag {
	case 'T':
		count := int(r.int16())
		columns := make([]map[string]any, 0, max(count, 0))
		*formats = (*formats)[:0]
		for i := 0; i < count && r.ok; i++ {
			name := r.cstring()
			table, attnum := r.uint32(), r.int16()
			typ, length, typmod := r.uint32(), r.int16(), int32(r.uint32())
			format := r.int16()
			*formats = append(*formats, format)
			columns = append(columns, map[string]any{"name": name, "table_oid": table, "attnum": attnum, "type_oid": typ, "type_len": length, "typmod": typmod, "format": format})
		}
		decoded = map[string]any{"columns": columns}
	case 'D':
		count := int(r.int16())
		values := make([]any, 0, max(count, 0))
		for i := 0; i < count && r.ok; i++ {
			binaryFormat := i < len(*formats) && (*formats)[i] == 1
			values = append(values, r.value(binaryFormat))
		}
		decoded = map[string]any{"values": values}
	case 'V':
		decoded = map[string]any{"value": r.value(false)}
	case 'C':
		decoded = map[string]any{"tag": r.cstring()}
	case 'E', 'N':
		decoded = errorFields(body)
		r.body = nil
	case 'Z':
		decoded = map[string]any{"status": string(r.next(1))}
	case 'S':
		name := r.cstring()
		decoded = map[string]any{"name": name, "value": r.cstring()}
	case 'A':
		pid := r.uint32()
		channel := r.cstring()
		decoded = map[string]any{"pid": pid, "channel": channel, "payload": r.cstring()}
	case 't':
		count := int(r.int16())
		oids := make([]uint32, 0, max(count, 0))
		for i := 0; i < count && r.ok; i++ {
			oids = append(oids, r.uint32())
		}
		decoded = map[string]any{"type_oids": oids}
	case 'G', 'H', 'W':
		format := r.next(1)
		count := int(r.int16())
		columns := make([]int16, 0, max(count, 0))
		for i := 0; i < count && r.ok; i++ {
			columns = append(columns, r.int16())
		}
		if r.ok {
			decoded = map[string]any{"format": int(format[0]), "column_formats": columns}
		}
	case 'd':
		decoded = map[string]any{"data": readableValue(body, false)}
		r.body = nil
	default:
		decoded = map[string]any{}
	}
	if !r.ok || len(r.body) != 0 {
		decoded = nil
	}
	name, known := backendNames(tag)
	if !known {
		name = "Unknown"
	}
	return name, decoded
}

// value reads one length-prefixed field (-1 is NULL).
func (r *logReader) value(binaryFormat bool) any {
	length := int32(r.uint32())
	if !r.ok || length == -1 {
		return nil
	}
	return readableValue(r.next(int(length)), binaryFormat)
}

// readableValue keeps printable UTF-8 text as a string and shows anything
// else (including binary-format values) as hex.
func readableValue(b []byte, binaryFormat bool) any {
	if !binaryFormat && utf8.Valid(b) && bytes.IndexFunc(b, func(r rune) bool {
		return !unicode.IsPrint(r) && r != '\t' && r != '\n' && r != '\r'
	}) < 0 {
		return string(b)
	}
	return hexValue{Hex: hex.EncodeToString(b)}
}

// errorFields names ErrorResponse/NoticeResponse fields; unknown field tags
// keep their one-letter code.
func errorFields(body []byte) map[string]string {
	out := map[string]string{}
	r := &logReader{body: body, ok: true}
	for r.ok && len(r.body) > 0 && r.body[0] != 0 {
		tag := r.next(1)[0]
		value := r.cstring()
		name, known := diagnosticFieldNames(tag)
		if !known {
			name = string(tag)
		}
		if r.ok {
			out[name] = value
		}
	}
	return out
}

func trimCString(b []byte) []byte {
	if end := bytes.IndexByte(b, 0); end >= 0 {
		return b[:end]
	}
	return b
}
