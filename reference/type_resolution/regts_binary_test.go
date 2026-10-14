package reference_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

// regtsBinaryPayload asserts actual four-byte alias fields and NULL markers,
// independently of CopyFrom, which could otherwise share a codec defect.
func regtsBinaryPayload(t *testing.T, payload []byte, oid uint32) {
	t.Helper()
	r := bytes.NewReader(payload)
	header := make([]byte, 19)
	if _, err := r.Read(header); err != nil || !bytes.Equal(header, []byte{'P', 'G', 'C', 'O', 'P', 'Y', '\n', 255, '\r', '\n', 0, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("binary COPY header=%x error=%v", header, err)
	}
	want := map[string]int{fmt.Sprintf("%d/0", oid): 1, "0/4294967295": 1, "4294967295/8": 1, "NULL/NULL": 1}
	for {
		var fields int16
		if err := binary.Read(r, binary.BigEndian, &fields); err != nil {
			t.Fatal("binary tuple header", err)
		}
		if fields == -1 {
			break
		}
		if fields != 2 {
			t.Fatalf("binary field count %d", fields)
		}
		var values [2]string
		for i := range values {
			var size int32
			if err := binary.Read(r, binary.BigEndian, &size); err != nil {
				t.Fatal(err)
			}
			if size == -1 {
				values[i] = "NULL"
				continue
			}
			if size != 4 {
				t.Fatalf("alias binary field has size %d, want 4", size)
			}
			var value uint32
			if err := binary.Read(r, binary.BigEndian, &value); err != nil {
				t.Fatal(err)
			}
			values[i] = fmt.Sprint(value)
		}
		key := values[0] + "/" + values[1]
		if want[key] == 0 {
			t.Fatalf("unexpected binary row %s", key)
		}
		want[key]--
	}
	if r.Len() != 0 {
		t.Fatal("bytes after binary COPY trailer")
	}
	for key, count := range want {
		if count != 0 {
			t.Fatalf("missing binary row %s", key)
		}
	}
}
