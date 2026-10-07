//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func TestReferenceOIDValuesMetadata(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			for _, q := range []string{"CREATE TABLE o(id INT,v OID)", "INSERT INTO o VALUES(1,'0'),(2,'1'),(3,'2147483647'),(4,'2147483648'),(5,'4294967295'),(6,NULL)"} {
				if _, err := backend.db.ExecContext(p.ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			stmt, err := backend.db.PrepareContext(p.ctx, "INSERT INTO o VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for i, arg := range []any{int64(-1), []byte("2147483648"), "0", nil} {
				result, err := stmt.ExecContext(p.ctx, i+10, arg)
				if err != nil {
					t.Fatal(err)
				}
				n, err := result.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			for _, tc := range []struct {
				q    string
				want []any
			}{
				{"SELECT v FROM o WHERE id<7 ORDER BY v", []any{int64(0), int64(1), int64(2147483647), int64(2147483648), int64(4294967295), nil}},
				{"SELECT v FROM o WHERE id<7 ORDER BY v DESC", []any{nil, int64(4294967295), int64(2147483648), int64(2147483647), int64(1), int64(0)}},
				{"SELECT v FROM o WHERE id=10", []any{int64(4294967295)}}, {"SELECT v FROM o WHERE id=11", []any{int64(2147483648)}}, {"SELECT v FROM o WHERE id=12", []any{int64(0)}}, {"SELECT v FROM o WHERE id=13", []any{nil}}, {"SELECT v FROM o WHERE id=99", []any{}},
			} {
				rows, err := backend.db.QueryContext(p.ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				ct := types[0]
				scan := reflect.TypeFor[int64]()
				if backend.name == "postgres" {
					scan = reflect.TypeFor[any]()
				}
				if ct.DatabaseTypeName() != "OID" || ct.ScanType() != scan {
					t.Fatal(ct)
				}
				if _, ok := ct.Length(); ok {
					t.Fatal("length")
				}
				if _, _, ok := ct.DecimalSize(); ok {
					t.Fatal("precision")
				}
				nullable, known := ct.Nullable()
				if known != (backend.name == "native") || known && !nullable {
					t.Fatal(nullable, known)
				}
				got := make([]any, 0)
				for rows.Next() {
					var raw any
					if err = rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if raw != nil {
						if backend.name == "postgres" {
							b, ok := raw.([]byte)
							if !ok {
								t.Fatalf("raw OID %T", raw)
							}
							n, err := strconv.ParseUint(string(b), 10, 32)
							if err != nil {
								t.Fatal(err)
							}
							raw = int64(n)
						} else if _, ok := raw.(int64); !ok {
							t.Fatalf("raw OID %T", raw)
						}
					}
					got = append(got, raw)
				}
				if err = rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatal(tc.q, got, tc.want)
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
		// pq's text bind has a portal context; native has no wire portal.
		s = referenceImplicitError("INSERT INTO o VALUES($1)", referenceDiagnostic{Code: tc.code, Message: message}, tc.in)
		pg := *s.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		if tc.in == "" {
			pg.Context = "unnamed portal parameter $1 = ''"
		}
		s.postgresDiagnostic = &pg
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

func TestReferenceOIDDumpReplay(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{"CREATE TABLE o(v OID DEFAULT '4294967295'::oid)", "INSERT INTO o VALUES('0'),('2147483648'),('4294967295'),(NULL)", "CREATE TABLE empty(v OID)"} {
		if _, err := p.memory.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.postgres, p.postgresRecord}, {p.memory, p.memoryRecord}} {
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT v::int8 FROM o ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}, {int64(2147483648)}, {int64(4294967295)}, {nil}}))
	}
}
