package reference_test

import "testing"

func TestReferenceQueryFromValuesTypes(t *testing.T) {
	for _, tc := range []struct {
		query, typ string
		rows [][]any
	}{
		{"VALUES(NULL),(NULL),(1)", "INT4", [][]any{{nil}, {nil}, {int64(1)}}},
		{"VALUES(NULL),(NULL)", "TEXT", [][]any{{nil}, {nil}}},
		{"VALUES('1'),(2)", "INT4", [][]any{{int64(1)}, {int64(2)}}},
		{"VALUES(1),(2147483648)", "INT8", [][]any{{int64(1)}, {int64(2147483648)}}},
		{"VALUES('1')", "TEXT", [][]any{{"1"}}},
		{"VALUES('a'::text),('b '::char(2))", "TEXT", [][]any{{"a"}, {"b"}}},
		{"VALUES('a '::char(2)),('b'::text)", "BPCHAR", [][]any{{"a "}, {"b"}}},
		{"VALUES('a'::varchar(2)),('b'::text)", "VARCHAR", [][]any{{"a"}, {"b"}}},
		{"VALUES('a'::text),('b'::varchar(2))", "TEXT", [][]any{{"a"}, {"b"}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				for _, prepared := range []bool{false, true} {
					s := referenceSelect(tc.query, []string{"column1"}, []string{tc.typ}, tc.rows)
					s.prepared, s.bag = prepared, true
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
}

func TestReferenceQueryFromValuesMetadata(t *testing.T) {
	for _, tc := range []struct {
		query, typ string
		length int64
		rows [][]any
	}{
		{"VALUES('a'::char(2)),('b'::char(2))", "BPCHAR", 2, [][]any{{"a "}, {"b "}}},
		{"VALUES('a'::varchar(2)),('b'::varchar(2))", "VARCHAR", 2, [][]any{{"a"}, {"b"}}},
		{"VALUES('a'::char(2)),('b'::char(3))", "BPCHAR", -1, [][]any{{"a "}, {"b  "}}},
		{"VALUES('a'::varchar(2)),('b'::varchar(3))", "VARCHAR", -1, [][]any{{"a"}, {"b"}}},
		{"VALUES(NULL),('a'::char(2))", "BPCHAR", -1, [][]any{{nil}, {"a "}}},
		{"VALUES('a'::char(2)),('b'::varchar(2))", "BPCHAR", -1, [][]any{{"a "}, {"b"}}},
		{`VALUES ('a' COLLATE "C"),('b' COLLATE "C")`, "TEXT", -1, [][]any{{"a"}, {"b"}}},
		{`VALUES ('a'),('b' COLLATE "C")`, "TEXT", -1, [][]any{{"a"}, {"b"}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				for _, prepared := range []bool{false, true} {
					referenceCharacterMetadata(t, p, backend.db, tc.query, prepared, !backend.pg, []string{"column1"}, []string{tc.typ}, []int64{tc.length}, []referenceCharacterNullability{{}})
					s := referenceSelect(tc.query, []string{"column1"}, []string{tc.typ}, tc.rows)
					s.prepared, s.bag = prepared, true
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			for _, s := range []referenceStep{
				referenceSelect("VALUES(1),(1.2)", []string{"column1"}, []string{"NUMERIC"}, [][]any{{"1"}, {"1.2"}}),
				referenceSelect("VALUES(1,NULL),('2','x')", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "x"}}),
			} {
				s.prepared, s.bag = prepared, true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
		}
	}
}

func TestReferenceQueryFromValuesCollationDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		query string
		want referenceDiagnostic
	}{
		{`VALUES ('a' COLLATE "C"),('b' COLLATE "POSIX")`, referenceDiagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: "31"}},
		{`SELECT v.n FROM (VALUES('a' COLLATE "C"),('b' COLLATE "POSIX")) v(n) ORDER BY n`, referenceDiagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: "47"}},
		{`SELECT n FROM (VALUES(t.c),(t.p)) v(n)`, referenceDiagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "t"`, Position: "23"}},
		{`SELECT * FROM t CROSS JOIN (VALUES(1)) v ORDER BY COALESCE(t.c,t.p)`, referenceDiagnostic{Code: "42P21", Message: `collation mismatch between implicit collations "C" and "POSIX"`, Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "64"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(`CREATE TABLE t(c TEXT COLLATE "C",p TEXT COLLATE "POSIX")`, 0))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES('a','b')", 1))
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.query, tc.want)
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					if prepared { s.errorStage = "prepare" }
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
}
