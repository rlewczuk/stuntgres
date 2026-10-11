package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringNumericBaseConsumers(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE snb_input(id integer,i integer,j bigint,s text,v varchar(4),p character(4))", 0))
		run(fixture.Exec("INSERT INTO snb_input VALUES(1,42,42,'abc','abc ','abc '),(2,-1,-1,'','abc','abc'),(3,42,42,'abc',NULL,NULL),(4,NULL,NULL,NULL,NULL,NULL)", 4))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id,to_bin(i),to_oct(j),to_hex(i),md5(s) FROM snb_input ORDER BY id", []string{"id", "to_bin", "to_oct", "to_hex", "md5"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
				{int64(1), "101010", "52", "2a", "900150983cd24fb0d6963f7d28e17f72"},
				{int64(2), "11111111111111111111111111111111", "1777777777777777777777", "ffffffff", "d41d8cd98f00b204e9800998ecf8427e"},
				{int64(3), "101010", "52", "2a", "900150983cd24fb0d6963f7d28e17f72"},
				{int64(4), nil, nil, nil, nil},
			}),
			fixture.Select("SELECT id FROM snb_input WHERE to_hex(i)='2a' AND md5(s)='900150983cd24fb0d6963f7d28e17f72' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}),
			fixture.Select("SELECT id FROM snb_input ORDER BY to_hex(i),id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(2)}, {int64(4)}}),
			fixture.Select("SELECT to_hex(i),count(*) FROM snb_input GROUP BY to_hex(i) ORDER BY to_hex(i)", []string{"to_hex", "count"}, []string{"TEXT", "INT8"}, [][]any{{"2a", int64(2)}, {"ffffffff", int64(1)}, {nil, int64(1)}}),
			stringNumericBaseText("SELECT CASE WHEN i=42 THEN md5(s) ELSE to_hex(j) END AS result FROM snb_input ORDER BY id", []string{"result"}, [][]any{{"900150983cd24fb0d6963f7d28e17f72"}, {"ffffffffffffffff"}, {"900150983cd24fb0d6963f7d28e17f72"}, {nil}}),
			stringNumericBaseText("SELECT md5(v),md5(p) FROM snb_input WHERE id=1", []string{"md5", "md5"}, [][]any{{"28a53e303da9f5742476fd6b62434540", "900150983cd24fb0d6963f7d28e17f72"}}),
		} {
			stringNumericBaseBoth(t, p, b, c, s)
		}
		run(fixture.Exec("CREATE TABLE snb_dst(id integer,d text)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO snb_dst VALUES($1,md5($2))", 1, int64(1), "abc")
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Select("SELECT id,d FROM snb_dst ORDER BY id", []string{"id", "d"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "900150983cd24fb0d6963f7d28e17f72"}, {int64(1), "900150983cd24fb0d6963f7d28e17f72"}}))
		stringNumericBaseBoth(t, p, b, c, fixture.Select("UPDATE snb_dst SET d=to_hex('-1'::bigint) WHERE id=1 RETURNING d", []string{"d"}, []string{"TEXT"}, [][]any{{"ffffffffffffffff"}, {"ffffffffffffffff"}}))
	})
}

func TestReferenceStringNumericBaseWriteAtomicity(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE snb_atomic(id integer,i integer,CONSTRAINT snb_allowed CHECK(to_hex(i)<>'2'))", 0))
		run(fixture.Exec("INSERT INTO snb_atomic VALUES(1,1)", 1))
		for _, prepared := range []bool{false, true} {
			d := stringNumericBaseDiagnostic("23514", `new row for relation "snb_atomic" violates check constraint "snb_allowed"`, "", "")
			d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, 2).", "public", "snb_atomic", "snb_allowed"
			s := fixture.ErrorDiagnostic("INSERT INTO snb_atomic VALUES(2,42),(3,$1::integer)", d, "2")
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(fixture.Select("SELECT id,i FROM snb_atomic ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		}
		run(fixture.Exec("CREATE TABLE snb_conversion_atomic(id integer,d text)", 0))
		run(fixture.Exec("INSERT INTO snb_conversion_atomic VALUES(1,md5('abc'))", 1))
		for _, prepared := range []bool{false, true} {
			d := stringNumericBaseDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, "", "")
			d.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic("INSERT INTO snb_conversion_atomic VALUES(2,to_hex(42)),(3,to_hex($1::integer))", d, "bad")
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(fixture.Select("SELECT id,d FROM snb_conversion_atomic ORDER BY id", []string{"id", "d"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "900150983cd24fb0d6963f7d28e17f72"}}))
		}
	})
}
