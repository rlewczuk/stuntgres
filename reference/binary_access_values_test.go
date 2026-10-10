package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

var binaryAccessNames = []string{"bit_count", "get_bit", "get_byte", "set_bit", "set_byte", "reverse"}
var binaryAccessTypes = []string{"INT8", "INT4", "INT4", "BYTEA", "BYTEA", "BYTEA"}

const binaryAccessProjection = "bit_count(v),get_bit(v,15::bigint),get_byte(v,0),set_bit(v,15::bigint,0),set_byte(v,0,256),reverse(v)"

func binaryAccessScenario(t *testing.T, f func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			f(t, p, b, c)
		})
	}
}

func binaryAccessBoth(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, c, b.recorder, s)
	referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
}

func TestReferenceBinaryAccessValues(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// PostgreSQL 18 manual examples; outputs are raw octets, not hex text.
		q := `SELECT bit_count('\x1234567890'::bytea),get_bit('\x1234567890'::bytea,30),get_byte('\x1234567890'::bytea,4),set_bit('\x1234567890'::bytea,30,0),set_byte('\x1234567890'::bytea,4,64),reverse('\xabcd'::bytea)`
		binaryAccessBoth(t, p, b, c, referenceSelect(q, binaryAccessNames, binaryAccessTypes, [][]any{{int64(15), int64(1), int64(144), "\x12\x34\x56\x38\x90", "\x12\x34\x56\x78\x40", "\xcd\xab"}}))
		for _, tc := range []struct {
			expr, typ string
			value     any
		}{
			{`get_bit('\x1234567890abcdef00'::bytea,43)`, "INT4", int64(1)},
			{`get_byte('\x1234567890abcdef00'::bytea,3)`, "INT4", int64(120)},
			{`set_bit('\x1234567890abcdef00'::bytea,43,0)`, "BYTEA", "\x12\x34\x56\x78\x90\xa3\xcd\xef\x00"},
			{`set_byte('\x1234567890abcdef00'::bytea,7,11)`, "BYTEA", "\x12\x34\x56\x78\x90\xab\xcd\x0b\x00"},
			{`get_bit('\x0180'::bytea,15::smallint)`, "INT4", int64(1)},
			{`get_byte('\x0180'::bytea,1::smallint)`, "INT4", int64(128)},
			{`set_byte('\x0180'::bytea,0::smallint,1::smallint)`, "BYTEA", "\x01\x80"},
			{`get_byte('\x41'::bytea,0)`, "INT4", int64(65)},
		} {
			binaryAccessBoth(t, p, b, c, referenceSelect("SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}}))
		}
		for i, expr := range []string{"bit_count(NULL::bytea)", "get_bit(NULL::bytea,0)", "get_byte(NULL::bytea,0)", "set_bit(NULL::bytea,0,1)", "set_byte(NULL::bytea,0,1)", "reverse(NULL::bytea)"} {
			binaryAccessBoth(t, p, b, c, referenceSelect("SELECT "+expr, binaryAccessNames[i:i+1], binaryAccessTypes[i:i+1], [][]any{{nil}}))
		}
		for _, expr := range []string{`get_bit('\x00'::bytea,NULL::bigint)`, `get_byte('\x00'::bytea,NULL::integer)`, `set_bit('\x00'::bytea,NULL::bigint,2)`, `set_bit('\x00'::bytea,8,NULL::integer)`, `set_byte('\x00'::bytea,NULL::integer,256)`, `set_byte('\x00'::bytea,1,NULL::integer)`, `set_bit(NULL::bytea,9223372036854775807,2)`} {
			typ := "BYTEA"
			if expr[:3] == "get" {
				typ = "INT4"
			}
			binaryAccessBoth(t, p, b, c, referenceSelect("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{nil}}))
		}
	})
}

func TestReferenceBinaryAccessOrientation(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			n       int
			bit     int64
			changed string
		}{{0, 1, "\x00\x80"}, {7, 0, "\x81\x80"}, {8, 0, "\x01\x81"}, {15, 1, "\x01\x00"}} {
			q := fmt.Sprintf(`SELECT get_bit('\x0180'::bytea,%d),set_bit('\x0180'::bytea,%d,%d)`, tc.n, tc.n, 1-tc.bit)
			binaryAccessBoth(t, p, b, c, referenceSelect(q, []string{"get_bit", "set_bit"}, []string{"INT4", "BYTEA"}, [][]any{{tc.bit, tc.changed}}))
		}
		for _, tc := range []struct {
			hex   string
			count int64
		}{{"", 0}, {"00", 0}, {"ff", 8}, {"00ff8041", 11}, {"0180aa55ff00", 18}} {
			binaryAccessBoth(t, p, b, c, referenceSelect(`SELECT bit_count('\x`+tc.hex+`'::bytea)`, []string{"bit_count"}, []string{"INT8"}, [][]any{{tc.count}}))
		}
		for _, tc := range []struct {
			n    int64
			want string
		}{{-1, "\xff\x80"}, {-257, "\xff\x80"}, {2147483647, "\xff\x80"}, {256, "\x00\x80"}, {-2147483648, "\x00\x80"}, {257, "\x01\x80"}} {
			binaryAccessBoth(t, p, b, c, referenceSelect(fmt.Sprintf(`SELECT set_byte('\x0180'::bytea,0,%d)`, tc.n), []string{"set_byte"}, []string{"BYTEA"}, [][]any{{tc.want}}))
		}
		binaryAccessBoth(t, p, b, c, referenceSelect(`SELECT get_byte(set_byte('\x0180'::bytea,1,-1),1),get_bit(set_bit('\x0180'::bytea,8,1),8),reverse('\xc3a9'::bytea),reverse(reverse('\x00ff8041'::bytea)),set_bit('\x0180'::bytea,0,1)`, []string{"get_byte", "get_bit", "reverse", "reverse", "set_bit"}, []string{"INT4", "INT4", "BYTEA", "BYTEA", "BYTEA"}, [][]any{{int64(255), int64(1), "\xa9\xc3", "\x00\xff\x80\x41", "\x01\x80"}}))
		binaryAccessBoth(t, p, b, c, referenceSelect("SELECT get_byte($1::bytea,0)", []string{"get_byte"}, []string{"INT4"}, [][]any{{int64(92)}}, []byte(`\x41`)))
	})
}
