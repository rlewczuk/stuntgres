package reference_test

import (
	"reflect"
	"testing"
)

func compareReferenceSetup(t *testing.T) *referencePair {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INTEGER,i INTEGER,j BIGINT,s TEXT,b BOOLEAN)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,2147483647,2147483648,'a',FALSE),(2,-2147483648,-2147483649,'z',TRUE),(3,NULL,NULL,NULL,NULL),(4,7,7,'a',FALSE)", 4))
	return p
}

func TestReferenceCompareOpsMatrix(t *testing.T) {
	p := compareReferenceSetup(t)
	for _, tc := range []struct {
		op               string
		forward, reverse []any
		ids, reverseIDs  []int64
	}{
		{"=", []any{false, false, nil, true}, []any{false, false, nil, true}, []int64{4}, []int64{4}},
		{"<>", []any{true, true, nil, false}, []any{true, true, nil, false}, []int64{1, 2}, []int64{1, 2}},
		{"!=", []any{true, true, nil, false}, []any{true, true, nil, false}, []int64{1, 2}, []int64{1, 2}},
		{"<", []any{true, false, nil, false}, []any{false, true, nil, false}, []int64{1}, []int64{2}},
		{"<=", []any{true, false, nil, true}, []any{false, true, nil, true}, []int64{1, 4}, []int64{2, 4}},
		{">", []any{false, true, nil, false}, []any{true, false, nil, false}, []int64{2}, []int64{1}},
		{">=", []any{false, true, nil, true}, []any{true, false, nil, true}, []int64{2, 4}, []int64{1, 4}},
	} {
		for direction, expr := range []string{"i" + tc.op + "j", "j" + tc.op + "i"} {
			values, ids := tc.forward, tc.ids
			if direction == 1 {
				values, ids = tc.reverse, tc.reverseIDs
			}
			want := [][]any{}
			for _, v := range values {
				want = append(want, []any{v})
			}
			projection := referenceSelect("SELECT "+expr+" FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, want)
			scalarStep(t, p, projection)
			scalarStep(t, p, referencePrepared(projection))
			want = [][]any{}
			for _, id := range ids {
				want = append(want, []any{id})
			}
			filter := referenceSelect("SELECT id FROM t WHERE "+expr+" ORDER BY id", []string{"id"}, []string{"INT4"}, want)
			scalarStep(t, p, filter)
			scalarStep(t, p, referencePrepared(filter))
		}
		for _, expr := range []string{"NULL" + tc.op + "i", "i" + tc.op + "NULL", "NULL" + tc.op + "NULL"} {
			step := referenceSelect("SELECT "+expr+" FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}})
			step.bag = true
			scalarStep(t, p, step)
			scalarStep(t, p, referencePrepared(step))
			step = referenceSelect("SELECT "+expr+" FROM t WHERE "+expr, []string{"?column?"}, []string{"BOOL"}, nil)
			scalarStep(t, p, step)
			scalarStep(t, p, referencePrepared(step))
		}
	}
	scalarStep(t, p, referenceExec("CREATE TABLE empty(i INTEGER,j BIGINT)", 0))
	step := referenceSelect("SELECT i<j FROM empty", []string{"?column?"}, []string{"BOOL"}, nil)
	scalarStep(t, p, step)
	scalarStep(t, p, referencePrepared(step))
}

func TestReferenceCompareOpsBoundariesAndParameters(t *testing.T) {
	p := compareReferenceSetup(t)
	for _, expr := range []string{"-9223372036854775808<-2147483648", "-2147483648>-9223372036854775808", "9223372036854775807>2147483647", "2147483647<9223372036854775807", "9007199254740993>9007199254740992", "9223372036854775807=9223372036854775807", "''<'a'", "'a'<'z'", "'z'<'é'", "FALSE<TRUE", "FALSE<=FALSE", "TRUE>=TRUE", "'a'<'b'"} {
		step := referenceSelect("SELECT "+expr+" FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}})
		step.bag = true
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
	}
	for _, step := range []referenceStep{
		referenceSelect("SELECT TRUE<FALSE FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}}),
		referenceSelect("SELECT id FROM t WHERE i<2147483648 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(4)}}),
		referenceSelect("SELECT s='a' FROM t WHERE s='a'", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}}),
		referenceSelect("SELECT s<'z',b<TRUE FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {false, false}, {nil, nil}, {true, true}}),
		referenceSelect("SELECT NULL<'x' FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}}),
		referenceSelect("SELECT $1<$2 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, "10", "2"),
		referenceSelect("SELECT $1=$1 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, "x"),
		referenceSelect("SELECT $1=$1 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}}, nil),
		referenceSelect("SELECT $1<i FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {false}}, 7),
		referenceSelect("SELECT i>$1 FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {false}}, 7),
		referenceSelect("SELECT $1<2147483648 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, int64(2147483647)),
		referenceSelect("SELECT $1<2147483648 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}}, int64(2147483648)),
		referenceSelect("SELECT i=$1,$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "INT4"}, [][]any{{false, int64(7)}, {false, int64(7)}, {nil, int64(7)}, {true, int64(7)}}, 7),
		referenceSelect("SELECT i=$1,j=$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}, {false, false}, {nil, nil}, {true, true}}, 7),
		referenceSelect("SELECT j=$1,i=$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}, {false, false}, {nil, nil}, {true, true}}, 7),
	} {
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
	}
}

func TestReferenceCompareOpsDiagnostics(t *testing.T) {
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated", true: "empty"}[empty], func(t *testing.T) {
			p := newReferenceScenario(t)
			scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,i INT,j BIGINT,s TEXT,b BOOL)", 0))
			if !empty {
				scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,NULL,NULL,NULL,NULL)", 1))
			}
			for _, tc := range []struct {
				q    string
				d    referenceDiagnostic
				args []any
			}{
				{"SELECT i<TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer < boolean", Position: "9", Hint: hint}, nil},
				{"SELECT s!=b FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: text <> boolean", Position: "9", Hint: hint}, nil},
				{"SELECT id FROM t WHERE i<s", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer < text", Position: "25", Hint: hint}, nil},
				{"SELECT /*é*/ i<TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer < boolean", Position: "15", Hint: hint}, nil},
				{"SELECT i<'oops' FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "10"}, nil},
				{"SELECT i<'2147483648' FROM t", referenceDiagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "10"}, nil},
				{"SELECT $1,i=$1 FROM t", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Position: "8", Detail: "integer versus text"}, []any{7}},
				{"SELECT i=$1,b=$1 FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: boolean = integer", Position: "14", Hint: hint}, []any{7}},
				{"SELECT $1=$2 FROM t WHERE i=$1", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = text", Position: "28", Hint: hint}, []any{"7", "7"}},
			} {
				step := referenceImplicitError(tc.q, tc.d, tc.args...)
				step.selectRows = true
				step.errorStage = "execute"
				scalarStep(t, p, step)
				step.errorStage = "prepare"
				scalarStep(t, p, referencePrepared(step))
			}
			bad := referenceImplicitError("SELECT $1<i FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, "oops")
			bad.selectRows = true
			bad.errorStage = "execute"
			pg := *bad.diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			bad.diagnostic = &pg
			scalarStep(t, p, bad)
			scalarStep(t, p, referencePrepared(bad))
		})
	}
}

func TestReferenceCompareOpsHeldPrepared(t *testing.T) {
	p := compareReferenceSetup(t)
	var observed [][][][]any
	for _, backend := range p.backends() {
		db := backend.db
		stmt, err := db.PrepareContext(p.ctx, "SELECT $1<i FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		executions := [][][]any{}
		for _, arg := range []any{7, nil, 7} {
			rows, err := stmt.QueryContext(p.ctx, arg)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 1 || meta[0].Name() != "?column?" || meta[0].DatabaseTypeName() != "BOOL" {
				t.Fatal(meta, err)
			}
			scalarAssertMetadata(t, backend.pg, meta)
			got := [][]any{}
			for rows.Next() {
				var v any
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				got = append(got, []any{v})
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			want := [][]any{{true}, {false}, {nil}, {false}}
			if arg == nil {
				want = [][]any{{nil}, {nil}, {nil}, {nil}}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("independent held expectation", got, want)
			}
			executions = append(executions, got)
		}
		stmt.Close()
		observed = append(observed, executions)
	}
	if len(observed) == 2 && !reflect.DeepEqual(observed[0], observed[1]) {
		t.Fatal("held backend parity", observed)
	}
}

func TestReferenceCompareOpsExtendedProviders(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE extended(i SMALLINT,j BIGINT,f REAL,g DOUBLE PRECISION,n NUMERIC,m NUMERIC,u UUID,v UUID,c TIME,d TIME,x TIMESTAMP,y TIMESTAMP)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO extended VALUES(-32768,2147483648,'NaN','Infinity',9007199254740993,9007199254740992,'7fffffffffffffffffffffffffffffff','80000000000000000000000000000000','01:00','02:00','2000-01-01','2000-01-02'),(NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL)", 2))
	step := referenceSelect("SELECT i<j,f>g,n>m,u<v,c<d,x<y FROM extended", []string{"?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}, {nil, nil, nil, nil, nil, nil}})
	step.bag = true
	scalarStep(t, p, step)
	scalarStep(t, p, referencePrepared(step))
	for _, step := range []referenceStep{
		referenceSelect("SELECT f=f,n=n,u=u,c=c,x=x FROM extended", []string{"?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true}, {nil, nil, nil, nil, nil}}),
		referenceSelect("SELECT c=NULL,NULL<x FROM extended", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{nil, nil}, {nil, nil}}),
	} {
		step.bag = true
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
	}

}

func TestReferenceCompareOpsTemporalPreparedCaptures(t *testing.T) {
	p := compareReferenceSetup(t)
	for _, query := range []string{
		"SELECT '04:05:06.123'=TIME(3) '04:05:06.123' FROM t",
		"SELECT '2000-01-01 04:05:06.123'=TIMESTAMP(3) '2000-01-01 04:05:06.123' FROM t",
	} {
		var observed [][][][]any
		for _, backend := range p.backends() {
			db := backend.db
			stmt, err := db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			executions := [][][]any{}
			for range 3 {
				rows, err := stmt.QueryContext(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 1 || meta[0].Name() != "?column?" || meta[0].DatabaseTypeName() != "BOOL" {
					t.Fatal(meta, err)
				}
				scalarAssertMetadata(t, backend.pg, meta)
				got := [][]any{}
				for rows.Next() {
					var v any
					if err := rows.Scan(&v); err != nil {
						t.Fatal(err)
					}
					got = append(got, []any{v})
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if !reflect.DeepEqual(got, [][]any{{true}, {true}, {true}, {true}}) {
					t.Fatal("independent temporal captures", got)
				}
				executions = append(executions, got)
			}
			stmt.Close()
			observed = append(observed, executions)
		}
		if len(observed) == 2 && !reflect.DeepEqual(observed[0], observed[1]) {
			t.Fatal("temporal prepared parity", observed)
		}
	}
}
