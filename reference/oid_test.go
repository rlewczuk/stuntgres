package reference_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceOIDValuesMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE o(id INT,v OID)", 0))
	p.step(t, referenceExec("INSERT INTO o VALUES(1,'0'),(2,'1'),(3,'2147483647'),(4,'2147483648'),(5,'4294967295'),(6,NULL)", 6))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			q := "INSERT INTO o VALUES($1,$2)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for i, arg := range []any{int64(-1), []byte("2147483648"), "0", nil} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, i+10, arg))
			}
			for _, step := range []referenceStep{
				referenceSelect("SELECT id,v FROM o WHERE id<7 ORDER BY id", []string{"id", "v"}, []string{"INT4", "OID"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(1)}, {int64(3), int64(2147483647)}, {int64(4), int64(2147483648)}, {int64(5), int64(4294967295)}, {int64(6), nil}}),
				referenceSelect("SELECT v FROM o WHERE id<7 ORDER BY v", []string{"v"}, []string{"OID"}, [][]any{{int64(0)}, {int64(1)}, {int64(2147483647)}, {int64(2147483648)}, {int64(4294967295)}, {nil}}),
				referenceSelect("SELECT v FROM o WHERE id<7 ORDER BY v DESC", []string{"v"}, []string{"OID"}, [][]any{{nil}, {int64(4294967295)}, {int64(2147483648)}, {int64(2147483647)}, {int64(1)}, {int64(0)}}),
				referenceSelect("SELECT v FROM o WHERE id=10", []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}}),
				referenceSelect("SELECT v FROM o WHERE id=11", []string{"v"}, []string{"OID"}, [][]any{{int64(2147483648)}}),
				referenceSelect("SELECT v FROM o WHERE id=12", []string{"v"}, []string{"OID"}, [][]any{{int64(0)}}),
				referenceSelect("SELECT v FROM o WHERE id=13", []string{"v"}, []string{"OID"}, [][]any{{nil}}),
				referenceSelect("SELECT v FROM o WHERE id=99", []string{"v"}, []string{"OID"}, nil),
			} {
				oidReferenceRows(t, p.ctx, backend, backend.db, step)
				if len(step.types) == 1 {
					rows, err := backend.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), step.query)
					if err != nil {
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil {
						rows.Close()
						t.Fatal(err)
					}
					nullable, known := types[0].Nullable()
					if known != !backend.pg || nullable != !backend.pg {
						t.Fatal(nullable, known)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestReferenceOIDCastsComparisons(t *testing.T) {
	nameReferenceSteps(t, []referenceStep{
		referenceExec("CREATE TABLE one(s SMALLINT,i INT,b BIGINT,v OID)", 0), referenceExec("INSERT INTO one VALUES(-1,-1,4294967295,'4294967295')", 1),
		referenceSelect("SELECT v::int4,v::int8,v::text,s::oid::int8,i::oid::int8,CAST(b AS oid)::int8 FROM one", []string{"v", "v", "v", "s", "i", "b"}, []string{"INT4", "INT8", "TEXT", "INT8", "INT8", "INT8"}, [][]any{{int64(-1), int64(4294967295), "4294967295", int64(4294967295), int64(4294967295), int64(4294967295)}}),
		referenceSelect("SELECT v=i,i=v,v=s,s=v,v=b,b=v FROM one", []string{"?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}}),
		referenceSelect("SELECT v>0,v>=0,v<0,v<=0,v<>0,v!=0,v=NULL FROM one", []string{"?column?", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false, false, true, true, nil}}),
		referenceSelect("SELECT CASE WHEN TRUE THEN v ELSE i END::int8,COALESCE(NULL::oid,v)::int8,NULLIF(v,i)::int8 FROM one", []string{"i", "coalesce", "nullif"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(4294967295), int64(4294967295), nil}}),
		referenceExec("CREATE TABLE dest(i INT,b BIGINT,v OID)", 0), referenceExec("INSERT INTO dest SELECT v,v,i FROM one", 1), referenceSelect("SELECT i,b,v::int8 FROM dest", []string{"i", "b", "v"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(-1), int64(4294967295), int64(4294967295)}}),
		referenceExec("CREATE TABLE grouped(v OID)", 0), referenceExec("INSERT INTO grouped VALUES('0'),('4294967295'),('4294967295'),(NULL),(NULL)", 5),
		referenceSelect("SELECT v::int8,count(*) FROM grouped GROUP BY v ORDER BY v", []string{"v", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(4294967295), int64(2)}, {nil, int64(2)}}),
	})
}

func TestReferenceOIDInputDiagnostics(t *testing.T) {
	steps := []referenceStep{referenceExec("CREATE TABLE one(id INT)", 0), referenceExec("INSERT INTO one VALUES(1)", 1), referenceExec("CREATE TABLE o(v OID)", 0), referenceExec("INSERT INTO o VALUES('7')", 1)}
	for _, tc := range []struct {
		in   string
		want int64
	}{{"-1", 4294967295}, {"-2147483648", 2147483648}, {"-1040", 4294966256}, {"0xffffffff", 4294967295}, {"0377", 255}, {"-18446744073709551615", 1}, {"18446744073709551615", 4294967295}, {"18446744071562067968", 2147483648}, {"\t\n\r\v\f +0007 \t", 7}} {
		steps = append(steps, referenceSelect("SELECT '"+tc.in+"'::oid::int8 FROM one", []string{"int8"}, []string{"INT8"}, [][]any{{tc.want}}))
	}
	for _, tc := range []struct{ in, code string }{{"", "22P02"}, {" \t\r\v\f\n", "22P02"}, {"+", "22P02"}, {"-", "22P02"}, {"+ 1", "22P02"}, {"- 1", "22P02"}, {"x", "22P02"}, {"08", "22P02"}, {"0x", "22P02"}, {"0b10", "22P02"}, {"0o10", "22P02"}, {"1_000", "22P02"}, {"\u00a01", "22P02"}, {"1\u00a0", "22P02"}, {"4294967296x", "22P02"}, {"4294967296", "22003"}, {"-2147483649", "22003"}, {"18446744071562067967", "22003"}, {"18446744073709551616", "22003"}, {"18446744073709551616x", "22003"}, {"-18446744073709551616", "22003"}} {
		message := `invalid input syntax for type oid: "` + tc.in + `"`
		if tc.code == "22003" {
			message = `value "` + tc.in + `" is out of range for type oid`
		}
		q := `/*é*/ INSERT INTO o VALUES('1'),('` + tc.in + `')`
		pos := utf8.RuneCountInString(q[:strings.LastIndex(q, "('")]) + 2
		s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: message, Position: strconv.Itoa(pos)})
		steps = append(steps, s)
		s = referencePrepared(s)
		s.errorStage = "prepare"
		steps = append(steps, s)
		// Both typed import paths retain the actual failing parameter context.
		s = referenceImplicitError("INSERT INTO o VALUES($1)", referenceDiagnostic{Code: tc.code, Message: message}, tc.in)
		pg := *s.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		if tc.in == "" {
			pg.Context = "unnamed portal parameter $1 = ''"
		}
		s.diagnostic = &pg
		s.errorStage = "execute"
		steps = append(steps, s)
		s = referencePrepared(s)
		steps = append(steps, s)
	}
	for _, q := range []string{"SELECT (-1)::int8::oid FROM one", "SELECT '4294967295'::oid=(-1)::int8 FROM one", "SELECT (-1)::int8='4294967295'::oid FROM one", "INSERT INTO o VALUES(4294967296::int8)"} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "OID out of range"})
		steps = append(steps, s)
	}
	for _, q := range []string{"SELECT '1'::oid::smallint FROM one", "SELECT NULL::oid::smallint FROM one"} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42846", Message: "cannot cast type oid to smallint", Position: strconv.Itoa(strings.LastIndex(q, "::") + 1)})
		steps = append(steps, s)
		s = referencePrepared(s)
		s.errorStage = "prepare"
		steps = append(steps, s)
	}
	steps = append(steps, referenceSelect("SELECT v::int8 FROM o", []string{"v"}, []string{"INT8"}, [][]any{{int64(7)}}))
	nameReferenceSteps(t, steps)
}
