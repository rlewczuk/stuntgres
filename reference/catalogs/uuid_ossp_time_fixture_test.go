package reference_test

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var uuidOsspTimeNames = []string{"uuid_generate_v1", "uuid_generate_v1mc"}

type uuidOsspTimeFields struct {
	ticks    uint64
	sequence uint16
	node     [6]byte
	instant  time.Time
}

// Independent RFC4122 decoder: never use the engine's packer or extractor here.
func uuidOsspTimeDecode(t *testing.T, value string, multicast bool) uuidOsspTimeFields {
	t.Helper()
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || strings.ToLower(value) != value {
		t.Fatalf("noncanonical UUID %q", value)
	}
	b, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(b) != 16 {
		t.Fatalf("UUID %q: %v", value, err)
	}
	if b[6]>>4 != 1 || b[8]&0xc0 != 0x80 {
		t.Fatalf("not version1/RFC variant: %q", value)
	}
	f := uuidOsspTimeFields{
		ticks:    uint64(binary.BigEndian.Uint32(b[:4])) | uint64(binary.BigEndian.Uint16(b[4:6]))<<32 | uint64(binary.BigEndian.Uint16(b[6:8])&0xfff)<<48,
		sequence: uint16(b[8]&0x3f)<<8 | uint16(b[9]),
	}
	copy(f.node[:], b[10:])
	if multicast && f.node[0]&3 != 3 {
		t.Fatalf("multicast/local node bits missing: %q", value)
	}
	const epoch = int64(122192928000000000)
	delta := int64(f.ticks) - epoch
	f.instant = time.Unix(delta/10000000, delta%10000000*100).UTC()
	return f
}

func uuidOsspTimeRead(t *testing.T, p *fixture.Pair, e fixture.Executor, r *fixture.Recorder, pg bool, query string, count, width int, multicast bool, nullable ...bool) []string {
	t.Helper()
	before := time.Now()
	rows, err := e.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, r), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	uuidOsspColumns(t, rows, pg, width, len(nullable) > 0 && nullable[0])
	var result []string
	n := 0
	for rows.Next() {
		n++
		values, ptrs := make([]any, width), make([]any, width)
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, raw := range values {
			var value string
			if pg {
				b, ok := raw.([]byte)
				if !ok {
					t.Fatalf("pq UUID raw %T", raw)
				}
				value = string(b)
			} else {
				var ok bool
				value, ok = raw.(string)
				if !ok {
					t.Fatalf("native UUID raw %T", raw)
				}
			}
			f := uuidOsspTimeDecode(t, value, multicast)
			if f.instant.Before(before.Add(-2*time.Second)) || f.instant.After(time.Now().Add(2*time.Second)) {
				t.Fatalf("UUID time %s outside demand bracket", f.instant)
			}
			result = append(result, value)
		}
	}
	if rows.Err() != nil || n != count {
		t.Fatalf("rows=%d want=%d err=%v", n, count, rows.Err())
	}
	return result
}

func uuidOsspTimeExtract(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, value string, multicast bool) {
	t.Helper()
	f := uuidOsspTimeDecode(t, value, multicast)
	query := "SELECT uuid_extract_version('" + value + "'::uuid) AS version,uuid_extract_timestamp('" + value + "'::uuid) AS instant"
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select(query, []string{"version", "instant"}, []string{"INT2", "TIMESTAMPTZ"}, [][]any{{int64(1), f.instant.Truncate(time.Microsecond)}}))
}

func uuidOsspTimeUnique(t *testing.T, seen map[string]bool, values []string) {
	t.Helper()
	for _, v := range values {
		if seen[v] {
			t.Fatalf("reused UUID %q", v)
		}
		seen[v] = true
	}
}
