package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func logicalReferenceSetup(t *testing.T) *referencePair {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,a BOOL,b BOOL,s TEXT)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,TRUE,TRUE,'yes'),(2,TRUE,FALSE,'yes'),(3,TRUE,NULL,'yes'),(4,FALSE,TRUE,'yes'),(5,FALSE,FALSE,'yes'),(6,FALSE,NULL,'yes'),(7,NULL,TRUE,'yes'),(8,NULL,FALSE,'yes'),(9,NULL,NULL,'yes')", 9))
	scalarStep(t, p, referenceExec("CREATE TABLE one(id INT)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO one VALUES(1)", 1))
	scalarStep(t, p, referenceExec("CREATE TABLE empty(id INT,a BOOL,b BOOL,s TEXT)", 0))
	return p
}

func TestReferenceLogicalTruth(t *testing.T) {
	p := logicalReferenceSetup(t)
	both := func(s referenceStep) { t.Helper(); scalarStep(t, p, s); scalarStep(t, p, referencePrepared(s)) }
	for _, v := range []struct {
		a, b    string
		and, or any
	}{
		{"TRUE", "TRUE", true, true}, {"TRUE", "FALSE", false, true}, {"TRUE", "NULL", nil, true},
		{"FALSE", "TRUE", false, true}, {"FALSE", "FALSE", false, false}, {"FALSE", "NULL", false, nil},
		{"NULL", "TRUE", nil, true}, {"NULL", "FALSE", false, nil}, {"NULL", "NULL", nil, nil},
	} {
		for _, op := range []struct {
			name  string
			value any
		}{{"AND", v.and}, {"OR", v.or}} {
			expr := v.a + " " + op.name + " " + v.b
			both(referenceSelect("SELECT "+expr+" FROM one", []string{"?column?"}, []string{"BOOL"}, [][]any{{op.value}}))
			var want [][]any
			if op.value == true {
				want = [][]any{{int64(1)}}
			}
			both(referenceSelect("SELECT id FROM one WHERE "+expr, []string{"id"}, []string{"INT4"}, want))
		}
	}
	for _, v := range []struct {
		input string
		value any
	}{{"TRUE", false}, {"FALSE", true}, {"NULL", nil}} {
		both(referenceSelect("SELECT NOT "+v.input+" FROM one", []string{"?column?"}, []string{"BOOL"}, [][]any{{v.value}}))
		var want [][]any
		if v.value == true {
			want = [][]any{{int64(1)}}
		}
		both(referenceSelect("SELECT id FROM one WHERE NOT "+v.input, []string{"id"}, []string{"INT4"}, want))
	}
	both(referenceSelect("SELECT id,a AND b,a OR b,NOT a FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{
		{int64(1), true, true, false}, {int64(2), false, true, false}, {int64(3), nil, true, false}, {int64(4), false, true, true}, {int64(5), false, false, true}, {int64(6), false, nil, true}, {int64(7), nil, true, nil}, {int64(8), false, nil, nil}, {int64(9), nil, nil, nil},
	}))
	for _, v := range []struct {
		expr string
		ids  []int64
	}{{"a AND b", []int64{1}}, {"a OR b", []int64{1, 2, 3, 4, 7}}, {"NOT a", []int64{4, 5, 6}}, {"NOT id=1 AND (a OR b)", []int64{2, 3, 4, 7}}} {
		want := [][]any{}
		for _, id := range v.ids {
			want = append(want, []any{id})
		}
		both(referenceSelect("SELECT id FROM t WHERE "+v.expr+" ORDER BY id", []string{"id"}, []string{"INT4"}, want))
	}
	for _, v := range []struct {
		expr  string
		value any
	}{{"TRUE OR FALSE AND FALSE", true}, {"(TRUE OR FALSE) AND FALSE", false}, {"NOT TRUE AND FALSE", false}, {"NOT (TRUE AND FALSE)", true}, {"NOT NOT NULL", nil}, {"(NULL OR FALSE) AND TRUE", nil}, {"'yes' AND 'no'", false}, {"NOT 'yes'", false}} {
		both(referenceSelect("SELECT "+v.expr+" FROM one", []string{"?column?"}, []string{"BOOL"}, [][]any{{v.value}}))
	}
	both(referenceSelect("SELECT a AND b FROM empty", []string{"?column?"}, []string{"BOOL"}, nil))
	both(referenceSelect("SELECT $1 AND TRUE,$1 FROM one", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{nil, nil}}, nil))
	both(referenceSelect("SELECT $1 AND $1,NOT $1 OR $1 FROM one", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}, false))
	scalarStep(t, p, referenceExec("CREATE TABLE bag(a BOOL,b BOOL)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO bag VALUES(TRUE,TRUE),(TRUE,TRUE),(NULL,FALSE),(NULL,NULL)", 4))
	s := referenceSelect("SELECT a AND b,a OR b FROM bag", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}, {false, nil}, {nil, nil}})
	s.bag = true
	both(s)
	s = referenceSelect("SELECT a AND b FROM bag WHERE a OR b", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}})
	s.bag = true
	both(s)
}

func TestReferenceLogicalDiagnostics(t *testing.T) {
	p := logicalReferenceSetup(t)
	for _, tc := range []struct {
		q    string
		d    referenceDiagnostic
		args []any
	}{
		{"SELECT TRUE AND 1 FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "17"}, nil},
		{"SELECT TRUE OR 1 FROM t", referenceDiagnostic{Code: "42804", Message: "argument of OR must be type boolean, not type integer", Position: "16"}, nil},
		{"SELECT NOT 1 FROM t", referenceDiagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type integer", Position: "12"}, nil},
		{"SELECT TRUE AND s FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type text", Position: "17"}, nil},
		{"SELECT 1 AND TRUE FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "8"}, nil},
		{"SELECT TRUE AND (1) FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "18"}, nil},
		{"SELECT NOT (s) FROM t", referenceDiagnostic{Code: "42804", Message: "argument of NOT must be type boolean, not type text", Position: "13"}, nil},
		{"SELECT FALSE AND missing FROM t", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}, nil},
		{"SELECT id FROM t WHERE TRUE AND 1", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "33"}, nil},
		{"SELECT /*é*/ TRUE AND 1 FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "23"}, nil},
		{"SELECT TRUE AND 'truth' FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "17"}, nil},
		{"SELECT FALSE AND 1 FROM empty", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "18"}, nil},
		{"SELECT TRUE OR 'truth' FROM empty", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "16"}, nil},
		{"SELECT NOT FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "12"}, nil},
		{"SELECT id FROM t WHERE TRUE AND", referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: "32"}, nil},
		{"SELECT (TRUE OR FALSE FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "23"}, nil},
		{"SELECT () FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "9"}, nil},
		{"SELECT TRUE AND AND FALSE FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "AND"`, Position: "17"}, nil},
		{"SELECT $1,$1 AND TRUE FROM one", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}, []any{true}},
		{"SELECT $1 FROM t WHERE a AND $1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}, []any{true}},
		{"SELECT id FROM t WHERE id=$1 AND $1", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "34"}, []any{1}},
	} {
		s := referenceImplicitError(tc.q, tc.d, tc.args...)
		s.selectRows = true
		s.errorStage = "execute"
		scalarStep(t, p, s)
		s.errorStage = "prepare"
		scalarStep(t, p, referencePrepared(s))
	}
	s := referenceImplicitError("SELECT NOT $1 FROM one", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`}, "truth")
	s.selectRows = true
	s.errorStage = "execute"
	pg := *s.diagnostic
	pg.Context = "unnamed portal parameter $1 = '...'"
	s.diagnostic = &pg
	scalarStep(t, p, s)
	scalarStep(t, p, referencePrepared(s))
}

// PostgreSQL accepts AND/OR here as bare target aliases. The native dialect
// excludes aliases and instead parses these keywords as logical continuations.
// Assert each backend's independent contract rather than false error parity.
func TestReferenceLogicalKeywordAliasBoundary(t *testing.T) {
	p := logicalReferenceSetup(t)
	for _, tc := range []struct{ q, label, position string }{
		{"SELECT TRUE AND FROM t", "and", "17"},
		{"SELECT TRUE OR FROM t", "or", "16"},
	} {
		native := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "FROM"`, Position: tc.position})
		native.selectRows = true
		native.errorStage = "execute"
		scalarRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
		native.errorStage = "prepare"
		scalarRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(native))
		want := [][]any{{true}, {true}, {true}, {true}, {true}, {true}, {true}, {true}, {true}}
		postgres := referenceSelect(tc.q, []string{tc.label}, []string{"BOOL"}, want)
		postgres.bag = true
		if p.postgres != nil {
			scalarRunStep(t, p.ctx, p.postgres, p.postgresRecord, postgres)
		}
		if p.postgres != nil {
			scalarRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(postgres))
		}
	}
}

func TestReferenceLogicalHeldFrames(t *testing.T) {
	p := logicalReferenceSetup(t)
	pairs := []struct{ a, b, and, or, not any }{{true, true, true, true, false}, {true, false, false, true, false}, {true, nil, nil, true, false}, {false, true, false, true, true}, {false, false, false, false, true}, {false, nil, false, nil, true}, {nil, true, nil, true, nil}, {nil, false, false, nil, nil}, {nil, nil, nil, nil, nil}}
	for _, backend := range p.backends() {
		db := backend.db
		stmt, e := db.PrepareContext(p.ctx, "SELECT $1 AND $2,$1 OR $2,NOT $1 FROM one")
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		filters := make([]*sql.Stmt, 3)
		for i, expr := range []string{"$1 AND $2", "$1 OR $2", "NOT $1"} {
			filters[i], e = db.PrepareContext(p.ctx, "SELECT id FROM one WHERE "+expr)
			if e != nil {
				t.Fatal(e)
			}
			defer func(held *sql.Stmt) {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}(filters[i])
		}
		check := func(rows *sql.Rows, e error, names, types []string, want [][]any) {
			t.Helper()
			if e != nil {
				t.Fatal(e)
			}
			defer rows.Close()
			gotNames, e := rows.Columns()
			if e != nil || !reflect.DeepEqual(gotNames, names) {
				t.Fatal(gotNames, e)
			}
			meta, e := rows.ColumnTypes()
			if e != nil {
				t.Fatal(e)
			}
			for i, v := range types {
				if meta[i].DatabaseTypeName() != v {
					t.Fatal(meta[i])
				}
			}
			scalarAssertMetadata(t, backend.pg, meta)
			got := [][]any{}
			for rows.Next() {
				values := make([]any, len(names))
				dst := make([]any, len(names))
				for i := range dst {
					dst[i] = &values[i]
				}
				if e := rows.Scan(dst...); e != nil {
					t.Fatal(e)
				}
				got = append(got, values)
			}
			if e := rows.Err(); e != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(got, want, e)
			}
		}
		for _, v := range pairs {
			rows, e := stmt.QueryContext(p.ctx, v.a, v.b)
			check(rows, e, []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{v.and, v.or, v.not}})
			for i, result := range []any{v.and, v.or, v.not} {
				args := []any{v.a, v.b}
				if i == 2 {
					args = args[:1]
				}
				rows, e := filters[i].QueryContext(p.ctx, args...)
				want := [][]any{}
				if result == true {
					want = [][]any{{int64(1)}}
				}
				check(rows, e, []string{"id"}, []string{"INT4"}, want)
			}
		}
		stmt.Close()
		for _, filter := range filters {
			filter.Close()
		}
	}
}
