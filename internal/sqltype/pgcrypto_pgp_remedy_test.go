package sqltype

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

func TestPgcryptoPGPInnerGrammar(t *testing.T) {
	t.Run("packet_context_policy", func(t *testing.T) {
		for _, wire := range [][]byte{{175}, {175, 't'}} {
			_, _, err := pgpReadPacket(context.Background(), pgpInput{data: wire}, pgpOuter)
			pgpUnitError(t, err, "Wrong key or corrupt data")
			packet, tail, err := pgpReadPacket(context.Background(), pgpInput{data: wire}, pgpDecryptedInner)
			if err != nil || packet.tag != pgpLiteral || packet.size != len(wire)-1 || tail.len() != 0 {
				t.Fatal("one-octet context header", err)
			}
		}
	})
	for _, tc := range []struct {
		name  string
		inner []byte
		want  string
	}{
		{"old_context", []byte{175, 't', 0, 0, 0, 0, 0, 'a'}, "a"},
		{"multiple_definite", []byte{203, 7, 't', 0, 0, 0, 0, 0, 'a', 172, 7, 'u', 0, 0, 0, 0, 0, 'b'}, "ab"},
		{"definite_then_context", []byte{203, 7, 't', 0, 0, 0, 0, 0, 'a', 175, 't', 0, 0, 0, 0, 0, 'b'}, "ab"},
	} {
		for _, badMDC := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bad_mdc_%t", tc.name, badMDC), func(t *testing.T) {
				// Existing test-only independent AES/SHA1 author, not production
				// serialization; all fixtures fit its one-octet outer length.
				text, err := pgpDecrypt(context.Background(), PgcryptoPGPInvocation{input: pgpUnitAuthored(tc.inner, badMDC), password: "foobar"})
				if badMDC {
					if text != "" {
						t.Fatal("damaged MDC published literal text")
					}
					pgpUnitError(t, err, "Wrong key or corrupt data")
				} else if err != nil || text != tc.want {
					t.Fatal("authenticated literal sequence", err)
				}
			})
		}
	}
	t.Run("cancel_after_first_literal", func(t *testing.T) {
		for _, format := range []byte{'t', 'x'} {
			scratch := []byte{203, 7, format, 0, 0, 0, 0, 0, 'a', 203, 7, 't', 0, 0, 0, 0, 0, 'b'}
			collector := &pgpLiteralCollector{}
			defer collector.output.clear()
			ctx := &pgpLiteralCancelContext{Context: context.Background(), collector: collector}
			err := collector.read(ctx, scratch, false, false)
			if !errors.Is(err, context.Canceled) || string(collector.output.data) != "a" {
				t.Fatal("literal walker cancellation missed owned first payload", err)
			}
		}
	})
	t.Run("publication_cancel_points", func(t *testing.T) {
		wire := []byte{203, 7, 't', 0, 0, 0, 0, 0, 'a', 203, 7, 't', 0, 0, 0, 0, 0, 'b'}
		healthy := &pgpCancelContext{Context: context.Background(), at: int(^uint(0) >> 1)}
		text, err := pgpReadInner(healthy, append([]byte(nil), wire...), false, false, false)
		if err != nil || text != "ab" {
			t.Fatal("healthy literal sequence", err)
		}
		for at := 1; at <= healthy.polls; at++ {
			ctx := &pgpCancelContext{Context: context.Background(), at: at}
			text, err := pgpReadInner(ctx, append([]byte(nil), wire...), false, false, false)
			if !errors.Is(err, context.Canceled) || text != "" {
				t.Fatal("canceled literal sequence published text", at, err)
			}
		}
	})
}

// Cancel deterministically only after the first literal's actual owned payload,
// rather than racing a timer or relying on a synthetic replacement buffer.
type pgpLiteralCancelContext struct {
	context.Context
	collector *pgpLiteralCollector
}

func (c *pgpLiteralCancelContext) Err() error {
	if string(c.collector.output.data) == "a" {
		return context.Canceled
	}
	return nil
}

// All lengths below have complete supplied bodies. REL_18_6's MAX_CHUNK
// applies to each old/new definite or partial chunk, not their aggregate.
// Keep this independent of production writer limits and length helpers.
func TestPgcryptoPGPPacketChunkLimits(t *testing.T) {
	const limit = 16 * 1024 * 1024
	// Illegal partial lengths fail in the length decoder itself, without a
	// nonexistent body accidentally supplying the same corruption error.
	for octet := byte(249); octet <= 254; octet++ {
		t.Run(fmt.Sprintf("illegal_partial_%d", octet), func(t *testing.T) {
			_, _, _, err := pgpReadLength(pgpInput{data: []byte{octet}})
			pgpUnitError(t, err, "Wrong key or corrupt data")
			// The same decoder rejects an illegal continuation after a
			// completely supplied small partial chunk.
			wire := append([]byte{203, 234}, bytes.Repeat([]byte{91}, 1024)...)
			wire = append(wire, octet)
			_, _, _, err = pgpReadLength(pgpInput{data: wire[1026:]})
			pgpUnitError(t, err, "Wrong key or corrupt data")
			_, _, err = pgpReadPacket(context.Background(), pgpInput{data: wire}, pgpDecryptedInner)
			pgpUnitError(t, err, "Wrong key or corrupt data")
		})
	}
	for _, tc := range []struct {
		name   string
		form   string
		size   int
		reject bool
	}{
		{"old_exact", "old", limit, false},
		{"old_above", "old", limit + 1, true},
		{"new_exact", "new", limit, false},
		{"new_above", "new", limit + 1, true},
		{"partial_exact", "partial24", limit, false},
		{"partial_large_aggregate", "partial24tail", limit + 1, false},
		{"terminal_exact", "terminal", limit + 1024, false},
		{"terminal_above", "terminal", limit + 1 + 1024, true},
		{"small_partial_large_aggregate", "small", limit + 1, false},
		{"context_large", "context", limit + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Repeat([]byte{91}, tc.size)
			var wire []byte
			appendDefinite := func(body []byte) {
				wire = append(wire, 255)
				wire = binary.BigEndian.AppendUint32(wire, uint32(len(body)))
				wire = append(wire, body...)
			}
			switch tc.form {
			case "context":
				wire = append([]byte{175}, body...)
			case "old":
				wire = binary.BigEndian.AppendUint32([]byte{174}, uint32(len(body)))
				wire = append(wire, body...)
			case "new":
				wire = []byte{203}
				appendDefinite(body)
			case "partial24", "partial24tail":
				wire = append([]byte{203, 248}, body[:limit]...)
				if tc.form == "partial24tail" {
					appendDefinite(body[limit:])
					break
				}
				wire = append(wire, 0)
			case "terminal":
				wire = append([]byte{203, 234}, body[:1024]...)
				appendDefinite(body[1024:])
			case "small":
				wire = []byte{203}
				remaining := body
				for len(remaining) >= 16384 {
					wire = append(wire, 238)
					wire = append(wire, remaining[:16384]...)
					remaining = remaining[16384:]
				}
				appendDefinite(remaining)
			}
			packet, rest, err := pgpReadPacket(context.Background(), pgpInput{data: wire}, pgpDecryptedInner)
			if tc.reject {
				pgpUnitError(t, err, "Wrong key or corrupt data")
				return
			}
			if err != nil || packet.tag != pgpLiteral || packet.size != tc.size || rest.len() != 0 {
				t.Fatal("complete chunk boundary", packet.size, err)
			}
			got := wire[:packet.size]
			if err := packet.copyTo(context.Background(), got); err != nil || !bytes.Equal(got, body) {
				t.Fatal("complete chunk body differs", err)
			}
		})
	}
}
