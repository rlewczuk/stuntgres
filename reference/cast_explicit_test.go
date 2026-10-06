//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func castReferenceSetup(t *testing.T) *referencePair {
	t.Helper()
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT4,i INT4,n INT8,s TEXT,b BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2,2,'  +42 ',TRUE),(2,0,0,'no',FALSE),(3,NULL,NULL,NULL,NULL)", 3))
	return p
}
func castReferenceExpr(source, target string, syntax int) string {
	switch syntax {
	case 0:
		return "CAST(" + source + " AS " + target + ")"
	case 1:
		return source + "::" + target
	default:
		return target + "(" + source + ")"
	}
}
func castReferenceError(q string, d referenceDiagnostic, analysis bool, args ...any) referenceStep {
	step := referenceImplicitError(q, d, args...)
	step.selectRows = true
	step.errorStage = "execute"
	if analysis {
		step.errorStage = "prepare"
	}
	return step
}

// Run each independent expectation in its own subtest: a native failure must
// never prevent PostgreSQL from exercising this or a later scenario.
func castReferenceBoth(t *testing.T, p *referencePair, s referenceStep) {
	t.Helper()
	var pg, native referenceObserved
	pgOK := t.Run("postgres", func(t *testing.T) { pg = referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) })
	nativeOK := t.Run("native", func(t *testing.T) { native = referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s) })
	if pgOK && nativeOK && !reflect.DeepEqual(pg, native) {
		t.Errorf("parity postgres=%#v native=%#v", pg, native)
	}
}
func TestReferenceCastExplicitMatrix(t *testing.T) {
	p := castReferenceSetup(t)
	p.step(t, referenceExec("CREATE TABLE v(id INT,s TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO v VALUES(1,'  +42 '),(2,'-2'),(3,NULL)", 3))
	p.step(t, referenceExec("CREATE TABLE vb(id INT,s TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO vb VALUES(1,' ye '),(2,'of'),(3,NULL)", 3))
	values := [][][]any{{{int64(2), int64(0), nil}, {int64(2), int64(0), nil}, {"2", "0", nil}, {true, false, nil}}, {{int64(2), int64(0), nil}, {int64(2), int64(0), nil}, {"2", "0", nil}, nil}, {{int64(42), int64(-2), nil}, {int64(42), int64(-2), nil}, {"  +42 ", "no", nil}, {true, false, nil}}, {{int64(1), int64(0), nil}, nil, {"true", "false", nil}, {true, false, nil}}}
	for si, source := range []string{"i", "n", "s", "b"} {
		for ti, target := range []string{"int4", "int8", "text", "bool"} {
			for syntax := range 3 {
				t.Run(fmt.Sprintf("%s-%s-%d", source, target, syntax), func(t *testing.T) {
					expr := castReferenceExpr(source, target, syntax)
					q := "SELECT " + expr + " FROM t ORDER BY id"
					var step referenceStep
					if values[si][ti] == nil {
						d := referenceDiagnostic{Code: "42846", Message: "cannot cast type bigint to boolean", Position: "8"}
						if si == 3 {
							d.Message = "cannot cast type boolean to bigint"
						}
						if syntax == 1 {
							d.Position = "9"
						}
						if syntax == 2 {
							typ := "bigint"
							if si == 3 {
								typ = "boolean"
							}
							d.Code = "42883"
							d.Message = "function " + target + "(" + typ + ") does not exist"
							d.Position = "8"
							d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						}
						step = castReferenceError(q, d, false)
					} else {
						if si == 2 && ti != 2 {
							table := "v"
							if ti == 3 {
								table = "vb"
							}
							q = "SELECT " + expr + " FROM " + table + " ORDER BY id"
						}
						rows := [][]any{}
						for _, v := range values[si][ti] {
							rows = append(rows, []any{v})
						}
						name := source
						if syntax == 2 {
							name = target
						}
						step = referenceSelect(q, []string{name}, []string{strings.ToUpper(target)}, rows)
					}
					castReferenceBoth(t, p, step)
					if step.diagnostic != nil {
						step.errorStage = "prepare"
					}
					castReferenceBoth(t, p, referencePrepared(step))
				})
			}
		}
	}
}
func TestReferenceCastExplicitSyntaxMetadataConsumers(t *testing.T) {
	p := castReferenceSetup(t)
	for _, target := range []string{"int", "integer", "int4", "bigint", "int8", "bool", "boolean", "text", "\"int4\"", "\"int8\"", "\"bool\"", "\"text\""} {
		for syntax := range 2 {
			t.Run(target+strconv.Itoa(syntax), func(t *testing.T) {
				typ := "INT4"
				name := "int4"
				if strings.Contains(target, "bigint") || strings.Contains(target, "int8") {
					typ = "INT8"
					name = "int8"
				} else if strings.Contains(target, "bool") {
					typ = "BOOL"
					name = "bool"
				} else if strings.Contains(target, "text") {
					typ = "TEXT"
					name = "text"
				}
				castReferenceBoth(t, p, referenceSelect("SELECT "+castReferenceExpr("NULL", target, syntax)+" FROM t ORDER BY id", []string{name}, []string{typ}, [][]any{{nil}, {nil}, {nil}}))
			})
		}
	}
	for _, target := range []string{"int4", "int8", "text", "bool"} {
		castReferenceBoth(t, p, referenceSelect("SELECT \""+target+"\"(NULL) FROM t ORDER BY id", []string{target}, []string{strings.ToUpper(target)}, [][]any{{nil}, {nil}, {nil}}))
	}
	q := "SELECT CAST(i AS bigint),i::int4,CAST(1 AS integer),1::bigint,1::boolean::text,int8(i),text(i),NULL::bool,int4(i),CAST(int8(i) AS text) FROM t ORDER BY id"
	names := []string{"i", "i", "int4", "int8", "text", "int8", "text", "bool", "int4", "int8"}
	types := []string{"INT8", "INT4", "INT4", "INT8", "TEXT", "INT8", "TEXT", "BOOL", "INT4", "TEXT"}
	castReferenceBoth(t, p, referenceSelect(q, names, types, [][]any{{int64(2), int64(2), int64(1), int64(1), "true", int64(2), "2", nil, int64(2), "2"}, {int64(0), int64(0), int64(1), int64(1), "true", int64(0), "0", nil, int64(0), "0"}, {nil, nil, int64(1), int64(1), "true", nil, nil, nil, nil, nil}}))
	castReferenceBoth(t, p, referenceSelect(strings.Replace(q, " ORDER BY id", " WHERE FALSE", 1), names, types, nil))
	castReferenceBoth(t, p, referenceSelect("SELECT '  +42 '::int4,CAST(' ye ' AS bool),int8('9223372036854775807'),'-9223372036854775808'::int8,'-2147483648'::int4,'2147483647'::int4,(-2)::bool FROM t WHERE id=1", []string{"int4", "bool", "int8", "int8", "int4", "int4", "bool"}, []string{"INT4", "BOOL", "INT8", "INT8", "INT4", "INT4", "BOOL"}, [][]any{{int64(42), true, int64(9223372036854775807), int64(-9223372036854775808), int64(-2147483648), int64(2147483647), true}}))
	for _, predicate := range []string{"i=CAST('2' AS int4)", "i='2'::int4", "i=int4('2')", "CAST(i AS bool)", "i::bool", "bool(i)"} {
		castReferenceBoth(t, p, referenceSelect("SELECT id FROM t WHERE "+predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
	p.step(t, referenceExec("CREATE TABLE a(i INT4,b BOOL,s TEXT,n INT8)", 0))
	// A failure on native INSERT cannot prevent the independent PG stored-row assertion.
	for _, backend := range []struct {
		name string
		db   *sql.DB
		rec  *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"native", p.memory, p.memoryRecord}} {
		t.Run(backend.name+"-insert", func(t *testing.T) {
			for _, q := range []string{"INSERT INTO a VALUES(TRUE::int4,2::bool,TRUE::text,'42'::int8)", "INSERT INTO a VALUES(CAST(FALSE AS int4),CAST(0 AS bool),CAST(FALSE AS text),CAST('43' AS int8))", "INSERT INTO a VALUES(int4(TRUE),bool(-2),text(TRUE),int8('44'))", "INSERT INTO a VALUES(NULL::int4,NULL::bool,NULL::text,NULL::int8)"} {
				referenceRunStep(t, p.ctx, backend.db, backend.rec, referenceExec(q, 1))
			}
			referenceRunStep(t, p.ctx, backend.db, backend.rec, referenceSelect("SELECT i,b,s,n FROM a ORDER BY n", []string{"i", "b", "s", "n"}, []string{"INT4", "BOOL", "TEXT", "INT8"}, [][]any{{int64(1), true, "true", int64(42)}, {int64(0), false, "false", int64(43)}, {int64(1), true, "true", int64(44)}, {nil, nil, nil, nil}}))
		})
	}
	for _, tc := range []struct{ q, message string }{{"INSERT INTO a(i) VALUES(TRUE)", `column "i" is of type integer but expression is of type boolean`}, {"INSERT INTO a(b) VALUES(1)", `column "b" is of type boolean but expression is of type integer`}, {"INSERT INTO a(i) VALUES('2'::text)", `column "i" is of type integer but expression is of type text`}} {
		step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42804", Message: tc.message, Position: "25", Hint: "You will need to rewrite or cast the expression."})
		castReferenceBoth(t, p, step)
	}
	castReferenceBoth(t, p, referenceExec("INSERT INTO a(i) VALUES('2')", 1))
}
