package sqltype

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
)

func pgpUnitNoMDCBodyEOF(t *testing.T) {
	ctx := context.Background()
	payload := []byte{'t', 0, 0, 0, 0, 0, 'X'}
	for _, form := range []string{"old1", "old2", "old4", "new1", "new5"} {
		for _, tag := range []byte{9, 11, 18, 3, 60} {
			var wire []byte
			switch form {
			case "old1":
				if tag > 15 {
					continue
				}
				wire = []byte{128 | tag<<2, 8}
			case "old2":
				if tag > 15 {
					continue
				}
				wire = []byte{129 | tag<<2, 0, 8}
			case "old4":
				if tag > 15 {
					continue
				}
				wire = []byte{130 | tag<<2, 0, 0, 0, 8}
			case "new1":
				wire = []byte{192 | tag, 8}
			case "new5":
				wire = []byte{192 | tag, 255, 0, 0, 0, 8}
			}
			wire = append(wire, payload...)
			for _, framing := range []pgpFraming{pgpOuter, pgpDecryptedInner, pgpNoMDCOuter, pgpNoMDCInner} {
				p, tail, err := pgpReadPacket(ctx, pgpInput{data: wire}, framing)
				allowed := framing == pgpNoMDCOuter && tag == 9 || framing == pgpNoMDCInner && tag == 11
				if !allowed {
					pgpUnitError(t, err, "Wrong key or corrupt data")
					continue
				}
				if err != nil || p.size != len(payload) || !p.bodyEOF || tail.len() != 0 {
					t.Fatal("scoped definite EOF", form, tag, framing, err)
				}
				got := make([]byte, p.size)
				if err = p.copyTo(ctx, got); err != nil || !bytes.Equal(got, payload) {
					t.Fatal("EOF actual bytes", err)
				}
			}
		}
	}
	for _, format := range []byte{'t', 'b'} {
		for _, n := range []int{5, 6, 7} {
			body := []byte{format, 0, 0, 0, 0, 0, 'X'}[:n]
			wire := append([]byte{203, 8}, body...)
			got, err := pgpReadInner(ctx, wire, format == 'b', false, true)
			if n < 6 {
				pgpUnitError(t, err, "Wrong key or corrupt data")
			} else if err != nil || got != string(body[6:]) {
				t.Fatal("literal metadata EOF", n, err)
			}
		}
	}
	// The current partial chunk may be outstanding at EOF; an exhausted
	// chunk must still be followed by a complete continuation/terminal length.
	for _, tag := range []byte{9, 11} {
		framing := pgpNoMDCOuter
		if tag == 11 {
			framing = pgpNoMDCInner
		}
		for _, n := range []int{63, 512} {
			wire := append([]byte{192 | tag, 233}, bytes.Repeat([]byte{'P'}, n)...)
			p, tail, err := pgpReadPacket(ctx, pgpInput{data: wire}, framing)
			if n == 512 {
				pgpUnitError(t, err, "Wrong key or corrupt data")
				continue
			}
			if err != nil || !p.partial || !p.bodyEOF || p.size != 63 || tail.len() != 0 {
				t.Fatal("outstanding partial EOF", tag, err)
			}
			got := make([]byte, p.size)
			if err = p.copyTo(ctx, got); err != nil || !bytes.Equal(got, bytes.Repeat([]byte{'P'}, 63)) {
				t.Fatal("partial EOF copying", err)
			}
		}
		for _, suffix := range [][]byte{{192}, {255, 0}, {249}} {
			wire := append([]byte{192 | tag, 233}, bytes.Repeat([]byte{'P'}, 512)...)
			wire = append(wire, suffix...)
			_, _, err := pgpReadPacket(ctx, pgpInput{data: wire}, framing)
			pgpUnitError(t, err, "Wrong key or corrupt data")
		}
		oversized := binary.BigEndian.AppendUint32([]byte{192 | tag, 255}, 16777217)
		oversized = append(oversized, 'P')
		_, _, err := pgpReadPacket(ctx, pgpInput{data: oversized}, framing)
		pgpUnitError(t, err, "Wrong key or corrupt data")
	}
	// Complete outer plaintext with a partially supplied literal chunk. Legal
	// metadata consumes six bytes, leaving exactly57 supplied payload bytes.
	inner := append([]byte{203, 233}, []byte{'t', 0, 0, 0, 0, 0}...)
	inner = append(inner, bytes.Repeat([]byte{'P'}, 57)...)
	got, err := pgpReadInner(ctx, inner, false, false, true)
	if err != nil || got != string(bytes.Repeat([]byte{'P'}, 57)) {
		t.Fatal("literal partial payload EOF", err)
	}
	complete := append([]byte{203, 233}, []byte{'t', 0, 0, 0, 0, 0}...)
	complete = append(complete, bytes.Repeat([]byte{'P'}, 506)...)
	_, err = pgpReadInner(ctx, complete, false, false, true)
	pgpUnitError(t, err, "Wrong key or corrupt data")
}
