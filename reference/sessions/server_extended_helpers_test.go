//go:build !noservertests

package reference_test

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func serverCString(b []byte, s string) []byte { return append(append(b, s...), 0) }
func server16(b []byte, n int) []byte         { return binary.BigEndian.AppendUint16(b, uint16(n)) }
func serverParse(name, sql string, oids ...uint32) []byte {
	b := serverCString(serverCString(nil, name), sql)
	b = server16(b, len(oids))
	for _, oid := range oids {
		b = serverPut32(b, oid)
	}
	return serverPacket('P', b)
}
func serverBind(portal, statement string, formats []int, args [][]byte, results ...int) []byte {
	b := serverCString(serverCString(nil, portal), statement)
	b = server16(b, len(formats))
	for _, v := range formats {
		b = server16(b, v)
	}
	b = server16(b, len(args))
	for _, v := range args {
		if v == nil {
			b = serverPut32(b, ^uint32(0))
		} else {
			b = serverPut32(b, uint32(len(v)))
			b = append(b, v...)
		}
	}
	b = server16(b, len(results))
	for _, v := range results {
		b = server16(b, v)
	}
	return serverPacket('B', b)
}
func serverDescribe(kind byte, name string) []byte {
	return serverPacket('D', serverCString([]byte{kind}, name))
}
func serverClose(kind byte, name string) []byte {
	return serverPacket('C', serverCString([]byte{kind}, name))
}
func serverExecute(name string, n uint32) []byte {
	return serverPacket('E', serverPut32(serverCString(nil, name), n))
}
func serverCycle(t *testing.T, w *serverWire, packets ...[]byte) []serverFrame {
	t.Helper()
	for _, p := range packets {
		w.send(t, p)
	}
	w.send(t, serverPacket('S', nil))
	return w.ready(t)
}
func serverThrough(t *testing.T, w *serverWire, tag byte) []serverFrame {
	t.Helper()
	var out []serverFrame
	for {
		f := w.frame(t)
		out = append(out, f)
		if f.tag == tag {
			return out
		}
	}
}
func serverRawRows(t *testing.T, frames []serverFrame) [][][]byte {
	t.Helper()
	var out [][][]byte
	for _, f := range frames {
		if f.tag != 'D' {
			continue
		}
		b := f.body
		if len(b) < 2 {
			t.Fatal("short row")
		}
		n := int(binary.BigEndian.Uint16(b))
		b = b[2:]
		row := make([][]byte, n)
		for i := range row {
			if len(b) < 4 {
				t.Fatal("short length")
			}
			n := int32(binary.BigEndian.Uint32(b))
			b = b[4:]
			if n == -1 {
				continue
			}
			if n < 0 || int(n) > len(b) {
				t.Fatal("invalid value length")
			}
			row[i] = make([]byte, int(n))
			copy(row[i], b[:n])
			b = b[n:]
		}
		if len(b) != 0 {
			t.Fatal("row trailing bytes")
		}
		out = append(out, row)
	}
	return out
}
func serverColumns(t *testing.T, frames []serverFrame) []serverColumn {
	t.Helper()
	descriptions := serverDescriptions(t, frames)
	if len(descriptions) == 0 {
		t.Fatal("missing RowDescription")
	}
	return descriptions[0]
}
func serverWantRaw(t *testing.T, f []serverFrame, want [][][]byte) {
	t.Helper()
	serverWantCode(t, f, "")
	if got := serverRawRows(t, f); !reflect.DeepEqual(got, want) {
		t.Fatalf("raw rows %x want %x (%s)", got, want, serverFrameSummary(f))
	}
}
func serverWantTags(t *testing.T, f []serverFrame, want string) {
	t.Helper()
	if got := serverTags(f); got != want {
		t.Fatalf("tags %s want %s fields=%v", got, want, f)
	}
}
