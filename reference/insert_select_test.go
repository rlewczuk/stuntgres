//go:build reference

package reference_test

import "testing"

func TestReferenceInsertSelectCardinality(t *testing.T) {
	for _, mode := range []string{"direct", "prepared"} {
		t.Run(mode, func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE s(i INT,v TEXT,b BOOL)", 0))
			p.step(t, referenceExec("INSERT INTO s VALUES(1,'a',TRUE),(1,'a',TRUE),(2,NULL,FALSE),(3,'c',NULL)", 4))
			p.step(t, referenceExec("CREATE TABLE t(v TEXT,i BIGINT,b BOOL)", 0))
			want := [][]any{}
			for _, tc := range []struct {
				arg   any
				count int64
				added [][]any
			}{
				{nil, 0, nil},
				{true, 2, [][]any{{"a", int64(1), nil}, {"a", int64(1), nil}}},
				{false, 1, [][]any{{nil, int64(2), nil}}},
				{nil, 0, nil},
			} {
				step := referenceExec("INSERT INTO t(i,v) SELECT i,v FROM s WHERE b=$1 ORDER BY i", tc.count, tc.arg)
				if mode == "prepared" {
					step = referencePrepared(step)
				}
				p.step(t, step)
				want = append(want, tc.added...)
				p.step(t, referenceSelect("SELECT * FROM t ORDER BY i", []string{"v", "i", "b"}, []string{"TEXT", "INT8", "BOOL"}, want))
			}
			step := referenceExec("INSERT INTO t SELECT v,i FROM s", 4)
			if mode == "prepared" {
				step = referencePrepared(step)
			}
			p.step(t, step)
			want = append(want, []any{"a", int64(1), nil}, []any{"a", int64(1), nil}, []any{nil, int64(2), nil}, []any{"c", int64(3), nil})
			rows := referenceSelect("SELECT * FROM t", []string{"v", "i", "b"}, []string{"TEXT", "INT8", "BOOL"}, want)
			rows.bag = true
			p.step(t, rows)
		})
	}
}

func TestReferenceInsertSelectValuesTyping(t *testing.T) {
	for _, mode := range []string{"direct", "prepared"} {
		t.Run(mode, func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(i INT)", 0))
			for _, step := range []referenceStep{
				referenceExec("INSERT INTO t(i) VALUES ('1')", 1),
				referenceExec("INSERT INTO t(i) (VALUES ('1'))", 1),
				referenceExec("INSERT INTO t(i) SELECT '1'", 1),
				referenceExec("INSERT INTO t(i) SELECT NULL", 1),
				referenceExec("INSERT INTO t(i) SELECT $1", 1, "7"),
				referenceExec("INSERT INTO t(i) SELECT $1", 1, int64(8)),
				referenceExec("INSERT INTO t(i) SELECT $1", 1, nil),
				referenceExec("INSERT INTO t(i) VALUES (2),(3) ORDER BY 1 DESC", 2),
			} {
				if mode == "prepared" {
					step = referencePrepared(step)
				}
				p.step(t, step)
			}
			p.step(t, referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}, {int64(7)}, {int64(8)}, {nil}, {nil}}))
			p.step(t, referenceExec("CREATE TABLE text_values(v TEXT)", 0))
			p.step(t, referenceExec("CREATE TABLE big_values(i BIGINT)", 0))
			for _, step := range []referenceStep{
				referenceExec("INSERT INTO text_values(v) VALUES ('x'),(NULL),('x') ORDER BY 1", 3),
				referenceExec("INSERT INTO big_values(i) VALUES (1),(2147483648),(NULL) ORDER BY column1", 3),
			} {
				if mode == "prepared" {
					step = referencePrepared(step)
				}
				p.step(t, step)
			}
			p.step(t, referenceSelect("SELECT v FROM text_values ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {nil}}))
			p.step(t, referenceSelect("SELECT i FROM big_values ORDER BY i", []string{"i"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2147483648)}, {nil}}))
		})
	}
}

func TestReferenceInsertSelectDiagnostics(t *testing.T) {
	// Expectations are written independently of native output. Omitted fields
	// must be empty; referenceImplicitError supplies ERROR/ERROR severity. The
	// fixture checks every logical diagnostic field and native C-field absence.
	for _, tc := range []struct {
		name, query string
		want        referenceDiagnostic
		runtime     bool
	}{
		{"target_relation", "INSERT INTO missing SELECT 1", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "13"}, false},
		{"target_column", "INSERT INTO t(missing) SELECT 1", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "15"}, false},
		{"duplicate_target", "INSERT INTO t(i,i) SELECT 1,2", referenceDiagnostic{Code: "42701", Message: `column "i" specified more than once`, Position: "17"}, false},
		{"wide_source", "INSERT INTO t(i) SELECT 1,2", referenceDiagnostic{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "27"}, false},
		{"short_source", "INSERT INTO t(i,j) SELECT 1", referenceDiagnostic{Code: "42601", Message: "INSERT has more target columns than expressions", Position: "17"}, false},
		{"literal_input", "INSERT INTO t(i) SELECT 'bad'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "25"}, false},
		{"utf8_literal_input", "/*é*/ INSERT INTO t(i) SELECT 'bad'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "31"}, false},
		{"source_relation", "INSERT INTO t(i) SELECT i FROM missing", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "32"}, false},
		{"source_column", "INSERT INTO t(i) SELECT missing FROM s", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "25"}, false},
		{"empty_known_text", "INSERT INTO t(i) SELECT v FROM empty", referenceDiagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "25"}, false},
		{"decorated_unknown", "INSERT INTO t(i) VALUES ('1') ORDER BY 1", referenceDiagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression."}, false},
		{"late_narrowing", "INSERT INTO t(i) SELECT i FROM s ORDER BY i", referenceDiagnostic{Code: "22003", Message: "integer out of range"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(i INT,j INT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(9,9)", 1))
			p.step(t, referenceExec("CREATE TABLE s(i BIGINT)", 0))
			p.step(t, referenceExec("INSERT INTO s VALUES(1),(2147483648)", 2))
			p.step(t, referenceExec("CREATE TABLE empty(v TEXT)", 0))
			step := referenceImplicitError(tc.query, tc.want)
			step.errorStage = "execute"
			p.step(t, step)
			p.step(t, referenceSelect("SELECT * FROM t", []string{"i", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(9)}}))
			if !tc.runtime {
				step.errorStage = "prepare"
			}
			p.step(t, referencePrepared(step))
			p.step(t, referenceSelect("SELECT * FROM t", []string{"i", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(9)}}))
		})
	}
}
