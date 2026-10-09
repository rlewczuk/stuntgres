package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

// Independently specified logical id order, intentionally unlike insertion order.
func stringAggSetup(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE sa(k INT,id INT,v TEXT,d TEXT,b BYTEA,s BYTEA)", 0))
	run(referenceExec(`INSERT INTO sa VALUES(1,5,'c','/','\x80','\x5c'),(1,2,NULL,'SKIP',NULL,'\xdead'),(1,4,'b',NULL,'\xff',NULL),(1,1,'a','FIRST','\x00','\xff'),(1,6,NULL,'SKIP2',NULL,NULL),(1,3,'','!','\x','\x00')`, 6))
	run(referenceExec("CREATE TABLE empty_sa(k INT,id INT,v TEXT,d TEXT,b BYTEA,s BYTEA)", 0))
}

func stringAggMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			var scan reflect.Type
			switch c.DatabaseTypeName() {
			case "TEXT":
				scan = reflect.TypeFor[string]()
			case "BYTEA":
				scan = reflect.TypeFor[[]byte]()
			default:
				t.Fatalf("unexpected aggregate descriptor: %#v", c)
			}
			if c.ScanType() != scan {
				t.Fatalf("%s ScanType=%v want %v", c.Name(), c.ScanType(), scan)
			}
			n, known := c.Nullable()
			if pg {
				if n || known {
					t.Fatalf("pq nullable=%v,%v", n, known)
				}
			} else if !n || !known {
				t.Fatalf("native nullable=%v,%v", n, known)
			}
			length, known := c.Length()
			if pg || c.DatabaseTypeName() == "TEXT" {
				if !known || length != math.MaxInt64 {
					t.Fatalf("%s unbounded length=%v,%v", c.DatabaseTypeName(), length, known)
				}
			} else if known || length != 0 {
				t.Fatalf("native BYTEA length=%v,%v", length, known)
			}
			if p, s, known := c.DecimalSize(); known {
				t.Fatalf("decimal=%d,%d,%v", p, s, known)
			}
		}
	}
}

func TestReferenceStringAggValues(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			stringAggSetup(run)
			for _, s := range []referenceStep{
				referenceSelect("SELECT string_agg(v,d ORDER BY id),string_agg(v,d ORDER BY id DESC) AS reverse FROM sa", []string{"string_agg", "reverse"}, []string{"TEXT", "TEXT"}, [][]any{{"a!b/c", "cb!FIRSTa"}}),
				referenceSelect("SELECT string_agg(v,NULL ORDER BY id),string_agg(v,'' ORDER BY id) FROM sa", []string{"string_agg", "string_agg"}, []string{"TEXT", "TEXT"}, [][]any{{"abc", "abc"}}),
				referenceSelect("SELECT string_agg(v,d),string_agg(b,s) FROM empty_sa", []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, [][]any{{nil, nil}}),
				referenceSelect("SELECT string_agg(v,d),string_agg(b,s) FROM sa WHERE v IS NULL", []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, [][]any{{nil, nil}}),
				referenceSelect("SELECT string_agg(v,d),string_agg(b,s) FROM empty_sa GROUP BY k", []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, nil),
				referenceSelect("SELECT string_agg(v,d),string_agg(b,s) FROM sa WHERE id=3", []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, [][]any{{"", ""}}),
				referenceSelect("SELECT string_agg(v,',' ORDER BY id) FROM sa WHERE id=3 OR id=4", []string{"string_agg"}, []string{"TEXT"}, [][]any{{",b"}}),
				referenceSelect("SELECT string_agg('x',',') FROM sa WHERE id=1", []string{"string_agg"}, []string{"TEXT"}, [][]any{{"x"}}),
			} {
				s.checkMetadata = stringAggMetadata(backend.pg)
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE VIEW sa_source AS SELECT id,v,d,b,s FROM sa", 0))
			view := referenceSelect("SELECT string_agg(v,d ORDER BY id),string_agg(b,s ORDER BY id DESC) FROM sa_source", []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, [][]any{{"a!b/c", string([]byte{128, 255, 0, 255, 0})}})
			view.checkMetadata = stringAggMetadata(backend.pg)
			run(view)
			run(referencePrepared(view))
			run(referenceExec("INSERT INTO sa VALUES(NULL,7,NULL,'skip',NULL,NULL),(2,8,'z','!',NULL,NULL),(NULL,9,NULL,NULL,NULL,NULL)", 3))
			run(referenceSelect("SELECT k,string_agg(v,d ORDER BY id) FROM sa GROUP BY k ORDER BY k", []string{"k", "string_agg"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a!b/c"}, {int64(2), "z"}, {nil, nil}}))
		})
	}
}

func TestReferenceStringAggBinary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			stringAggSetup(run)
			s := referenceSelect("SELECT string_agg(b,s ORDER BY id),string_agg(b,s ORDER BY id DESC) FROM sa", []string{"string_agg", "string_agg"}, []string{"BYTEA", "BYTEA"}, [][]any{{string([]byte{0, 0, 255, 92, 128}), string([]byte{128, 255, 0, 255, 0})}})
			s.checkMetadata = stringAggMetadata(backend.pg)
			s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var a, b any
				if err := rows.Scan(&a, &b); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(a, []byte{0, 0, 255, 92, 128}) || !reflect.DeepEqual(b, []byte{128, 255, 0, 255, 0}) {
					t.Fatalf("raw ordered bytes=%#v %#v", a, b)
				}
			}
			run(s)
			run(referencePrepared(s))
		})
	}
}

func TestReferenceStringAggOrdering(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE ord(id INT,k INT,v TEXT)", 0))
			run(referenceExec("INSERT INTO ord VALUES(4,NULL,'d'),(2,1,'b'),(3,2,'c'),(1,NULL,'a')", 4))
			for _, s := range []referenceStep{
				referenceSelect("SELECT string_agg(v,',' ORDER BY k ASC NULLS FIRST,id DESC),string_agg(v,',' ORDER BY k DESC NULLS LAST,id) FROM ord", []string{"string_agg", "string_agg"}, []string{"TEXT", "TEXT"}, [][]any{{"d,a,b,c", "c,b,a,d"}}),
				referenceSelect("SELECT string_agg(v,',' ORDER BY 1,id DESC) FROM ord", []string{"string_agg"}, []string{"TEXT"}, [][]any{{"d,c,b,a"}}),
				referenceSelect("SELECT string_agg(v,',' ORDER BY -id) FROM ord", []string{"string_agg"}, []string{"TEXT"}, [][]any{{"d,c,b,a"}}),
				referenceSelect("SELECT k FROM ord GROUP BY k HAVING string_agg(v,'' ORDER BY id)='ad' ORDER BY string_agg(v,'' ORDER BY id DESC)", []string{"k"}, []string{"INT4"}, [][]any{{nil}}),
			} {
				run(s)
				run(referencePrepared(s))
			}
			stringAggSetup(run)
			for _, s := range []referenceStep{
				referenceSelect("SELECT string_agg(v,d ORDER BY v,id),string_agg(v,d ORDER BY d NULLS FIRST,id) FROM sa", []string{"string_agg", "string_agg"}, []string{"TEXT", "TEXT"}, [][]any{{"FIRSTab/c", "b!/cFIRSTa"}}),
				referenceSelect("SELECT string_agg(v,d ORDER BY b,id),string_agg(v,d ORDER BY b DESC,id) FROM sa", []string{"string_agg", "string_agg"}, []string{"TEXT", "TEXT"}, [][]any{{"FIRSTa/cb", "b/cFIRSTa!"}}),
			} {
				run(s)
				run(referencePrepared(s))
			}
		})
	}
}
