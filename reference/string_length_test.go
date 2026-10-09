package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func stringLengthScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func stringLengthBoth(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, c, b.recorder, s)
	referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
}

func TestReferenceStringLengthCounts(t *testing.T) {
	stringLengthScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			input               string
			chars, octets, bits any
		}{
			{"''::text", int64(0), int64(0), int64(0)},
			{"'josé'::text", int64(4), int64(5), int64(40)},
			{"'é🙂'::text", int64(2), int64(6), int64(48)},
			{"'é'::text", int64(2), int64(3), int64(24)},
			{"'é🙂  '::text", int64(4), int64(8), int64(64)},
			{"'é🙂  '::varchar", int64(4), int64(8), int64(64)},
			{"'é🙂'::char(4)", int64(2), int64(8), int64(48)},
			{"''::char(4)", int64(0), int64(4), int64(0)},
			{"'    '::char(4)", int64(0), int64(4), int64(0)},
			{"E'a\\t'::char(4)", int64(2), int64(4), int64(16)},
			{"'a '::char(4)", int64(2), int64(5), int64(24)},
			{"NULL::text", nil, nil, nil}, {"NULL::varchar", nil, nil, nil}, {"NULL::char(4)", nil, nil, nil},
			{"'é🙂'::name", int64(2), int64(6), int64(48)},
		} {
			t.Run(tc.input, func(t *testing.T) {
				q := "SELECT length(" + tc.input + ") AS len,char_length(" + tc.input + ") AS chars,character_length(" + tc.input + ") AS alias,octet_length(" + tc.input + ") AS octets,bit_length(" + tc.input + ") AS bits"
				stringLengthBoth(t, p, b, c, referenceSelect(q, []string{"len", "chars", "alias", "octets", "bits"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{tc.chars, tc.chars, tc.chars, tc.octets, tc.bits}}))
			})
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE sl_fixed(c char(4))", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO sl_fixed VALUES('a')", 1))
		stringLengthBoth(t, p, b, c, referenceSelect("SELECT octet_length(c) AS physical,octet_length(c::text) AS trimmed,bit_length(c) AS bits FROM sl_fixed", []string{"physical", "trimmed", "bits"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(4), int64(1), int64(8)}}))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT c FROM sl_fixed", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a   "}}))
	})
}

func TestReferenceStringLengthSearch(t *testing.T) {
	stringLengthScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			source, needle string
			want           any
		}{
			{"'é🙂é'", "'🙂'", int64(2)}, {"'é🙂é'", "'é'", int64(1)},
			{"'ababa'", "'aba'", int64(1)}, {"'aaaa'", "'aa'", int64(1)},
			{"'abc'", "'z'", int64(0)}, {"''", "'x'", int64(0)},
			{"'abc'", "''", int64(1)}, {"''", "''", int64(1)},
			{"'éx'", "'x'", int64(3)}, {"'é'", "'é'", int64(0)},
			{"NULL::text", "''", nil}, {"''", "NULL::text", nil}, {"NULL::text", "NULL::text", nil},
			{"'a'::char(4)", "' '", int64(0)}, {"'a  '::varchar", "' '", int64(2)},
			{"'a  '::text", "' '", int64(2)}, {"'ba '::text", "'a'::char(3)", int64(2)},
			{"'abc'", "'   '::char(3)", int64(1)}, {"'    '::char(4)", "' '", int64(0)},
			{"'    '::char(4)", "''", int64(1)},
		} {
			t.Run(tc.source+"/"+tc.needle, func(t *testing.T) {
				q := "SELECT strpos(" + tc.source + "," + tc.needle + ") AS ordinary,position(" + tc.needle + " IN " + tc.source + ") AS special,pg_catalog.position(" + tc.source + "," + tc.needle + ") AS qualified,\"position\"(" + tc.source + "," + tc.needle + ") AS quoted"
				stringLengthBoth(t, p, b, c, referenceSelect(q, []string{"ordinary", "special", "qualified", "quoted"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{tc.want, tc.want, tc.want, tc.want}}))
			})
		}
	})
}

func TestReferenceStringLengthPreparedMetadata(t *testing.T) {
	stringLengthScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		metadata := func(t *testing.T, rows *sql.Rows) {
			ct, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, col := range ct {
				scan := reflect.TypeFor[int64]()
				if b.pg {
					scan = reflect.TypeFor[int32]()
				}
				n, nullableKnown := col.Nullable()
				length, lengthKnown := col.Length()
				precision, scale, decimalKnown := col.DecimalSize()
				// Both observed adapters return Nullable(false,false) for these
				// computed INT4 results; their ScanTypes differ independently.
				if col.DatabaseTypeName() != "INT4" || col.ScanType() != scan || lengthKnown || decimalKnown || n || nullableKnown {
					t.Fatalf("INT4 metadata %s scan=%v nullable=%v/%v length=%v/%v decimal=%v/%v/%v", col.Name(), col.ScanType(), n, nullableKnown, length, lengthKnown, precision, scale, decimalKnown)
				}
			}
		}
		q := "SELECT char_length($1) AS chars,octet_length($1) AS octets,strpos($1,'🙂') AS found"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			arg  any
			want []any
		}{
			{"é🙂", []any{int64(2), int64(6), int64(2)}}, {"", []any{int64(0), int64(0), int64(0)}}, {nil, []any{nil, nil, nil}}, {"é🙂", []any{int64(2), int64(6), int64(2)}},
		} {
			s := referenceSelect(q, []string{"chars", "octets", "found"}, []string{"INT4", "INT4", "INT4"}, [][]any{tc.want}, tc.arg)
			s.checkMetadata = metadata
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		stringLengthBoth(t, p, b, c, referenceSelect("SELECT octet_length($1::char(4)) AS fixed,octet_length($1::text) AS textbytes,char_length($1::char(4)) AS chars", []string{"fixed", "textbytes", "chars"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(8), int64(6), int64(2)}}, "é🙂"))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE sl_empty(v text)", 0))
		s := referenceSelect("SELECT char_length(v) AS chars,octet_length(v) AS octets,strpos(v,'🙂') AS found FROM sl_empty", []string{"chars", "octets", "found"}, []string{"INT4", "INT4", "INT4"}, nil)
		s.checkMetadata = metadata
		stringLengthBoth(t, p, b, c, s)
	})
}
