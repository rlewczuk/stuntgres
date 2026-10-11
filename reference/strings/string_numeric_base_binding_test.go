package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringNumericBaseBinding(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, typ := range []string{"integer", "bigint"} {
			t.Run(typ, func(t *testing.T) {
				q := "SELECT to_bin($1::" + typ + "),to_oct($1::" + typ + "),to_hex($1::" + typ + ")"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				names := []string{"to_bin", "to_oct", "to_hex"}
				negative := []any{"11111111111111111111101100101110", "37777775456", "fffffb2e"}
				rangeInput := "2147483648"
				if typ == "bigint" {
					negative = []any{"1111111111111111111111111111111111111111111111111111101100101110", "1777777777777777775456", "fffffffffffffb2e"}
					rangeInput = "9223372036854775808"
				}
				valid := stringNumericBaseText(q, names, [][]any{{"101010", "52", "2a"}}, int64(42))
				for _, s := range []fixture.Step{valid, stringNumericBaseText(q, names, [][]any{negative}, int64(-1234)), stringNumericBaseText(q, names, [][]any{{nil, nil, nil}}, nil)} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					stringNumericBaseBoth(t, p, b, c, s)
				}
				for _, tc := range []struct{ arg, code, message string }{
					{"bad", "22P02", "invalid input syntax for type " + typ + ": \"bad\""},
					{rangeInput, "22003", "value \"" + rangeInput + "\" is out of range for type " + typ},
				} {
					d := stringNumericBaseDiagnostic(tc.code, tc.message, "", "")
					d.Context = "unnamed portal parameter $1 = '...'"
					bad := fixture.ErrorDiagnostic(q, d, tc.arg)
					bad.SelectRows, bad.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, valid)
					// Bound-input errors occur on Execute, never actual Prepare.
					fixture.RunStep(t, p.Ctx, c, b.Recorder, bad)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(bad))
				}
			})
		}
		for _, tc := range []struct{ q, want string }{
			{"SELECT $1::integer AS n,to_hex($1)", "ffffffff"},
			{"SELECT $1::bigint AS n,to_hex($1)", "ffffffffffffffff"},
		} {
			s := fixture.Select(tc.q, []string{"n", "to_hex"}, []string{"INT4", "TEXT"}, [][]any{{int64(-1), tc.want}}, int64(-1))
			if tc.want == "ffffffffffffffff" {
				s.Types[0] = "INT8"
			}
			stringNumericBaseBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN snb_int4 AS integer", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN snb_int8 AS bigint", 0))
		stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT to_hex('-1'::snb_int4),to_hex('-1'::snb_int8)", []string{"to_hex", "to_hex"}, [][]any{{"ffffffff", "ffffffffffffffff"}}))
	})
}

func TestReferenceStringNumericBaseMD5Binding(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT md5($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		valid := stringNumericBaseText(q, []string{"md5"}, [][]any{{"900150983cd24fb0d6963f7d28e17f72"}}, "abc")
		for _, tc := range []struct{ arg, want any }{
			{"abc", "900150983cd24fb0d6963f7d28e17f72"},
			{"", "d41d8cd98f00b204e9800998ecf8427e"}, {nil, nil},
			{"é", "66ddcd97cfdeabb2f6fb8a999b4bc76f"},
			{[]byte("abc"), "900150983cd24fb0d6963f7d28e17f72"},
			{[]byte(`\x0`), "d8e6a5626b620a84d69e8052951a080a"},
			{[]byte{}, "d41d8cd98f00b204e9800998ecf8427e"},
		} {
			s := stringNumericBaseText(q, []string{"md5"}, [][]any{{tc.want}}, tc.arg)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			stringNumericBaseBoth(t, p, b, c, s)
		}
		// pq sends typed nil bytes as SQL NULL. The inherited native text-input
		// boundary instead imports them as empty TEXT, without selecting BYTEA.
		var nilBytesDigest any = "d41d8cd98f00b204e9800998ecf8427e"
		if b.PG {
			nilBytesDigest = nil
		}
		nilBytes := stringNumericBaseText(q, []string{"md5"}, [][]any{{nilBytesDigest}}, []byte(nil))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, nilBytes)
		stringNumericBaseBoth(t, p, b, c, nilBytes)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, valid)
		for _, tc := range []struct {
			arg   any
			bytes string
		}{
			{"A\x00", "0x00"}, {"A\xff", "0xff"}, {[]byte{0}, "0x00"}, {[]byte{0xff}, "0xff"},
		} {
			d := stringNumericBaseDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+tc.bytes, "", "")
			d.Context = "unnamed portal parameter $1"
			s := fixture.ErrorDiagnostic(q, d, tc.arg)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, valid)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, valid)
	})
}

func TestReferenceStringNumericBaseMetadata(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE snb_meta(i integer,j bigint,s text)", 0))
		names := []string{"to_bin", "to_oct", "to_hex", "md5"}
		q := "SELECT to_bin(i),to_oct(j),to_hex(i),md5(s) FROM snb_meta"
		stringNumericBaseBoth(t, p, b, c, stringNumericBaseText(q, names, nil))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO snb_meta VALUES(NULL,NULL,NULL)", 1))
		stringNumericBaseBoth(t, p, b, c, stringNumericBaseText(q, names, [][]any{{nil, nil, nil, nil}}))
		q = "SELECT to_bin($1::integer),to_oct($1::bigint),to_hex($1::integer),md5($2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var retained []*sql.ColumnType
		var retainedValue string
		for _, arg := range []any{int64(42), nil, int64(-1), int64(42)} {
			rows, err := stmt.QueryContext(p.Ctx, arg, "abc")
			if err != nil {
				t.Fatal(err)
			}
			ct, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			stringNumericBaseColumns(t, ct, names)
			if !rows.Next() {
				rows.Close()
				t.Fatal("missing row", rows.Err())
			}
			var bin, oct, hex any
			var digest string
			if err := rows.Scan(&bin, &oct, &hex, &digest); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			want := []any{"101010", "52", "2a"}
			if arg == nil {
				want = []any{nil, nil, nil}
			} else if arg == int64(-1) {
				want = []any{"11111111111111111111111111111111", "1777777777777777777777", "ffffffff"}
			}
			if bin != want[0] || oct != want[1] || hex != want[2] || digest != "900150983cd24fb0d6963f7d28e17f72" {
				rows.Close()
				t.Fatalf("raw row %T %v / %T %v / %T %v / %s", bin, bin, oct, oct, hex, hex, digest)
			}
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal("extra row or row error", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if retained == nil {
				retained, retainedValue = ct, digest
			}
			stringNumericBaseColumns(t, ct, names)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		stringNumericBaseColumns(t, retained, names)
		if retainedValue != "900150983cd24fb0d6963f7d28e17f72" {
			t.Fatal("retained result changed", retainedValue)
		}
	})
}
