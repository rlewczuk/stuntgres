//go:build reference

package reference_test

import (
	"testing"
	"time"
)

func TestReferenceConditionalTypeMatrix(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE mixed(b INTEGER,e REAL)", 0))
	p.step(t, referenceExec("INSERT INTO mixed VALUES(1,2)", 1))
	mixed := referenceSelect("SELECT NULLIF(b,e),NULLIF(e,b) FROM mixed", []string{"nullif", "nullif"}, []string{"FLOAT8", "FLOAT4"}, [][]any{{float64(1), float64(2)}})
	p.step(t, mixed)
	p.step(t, referencePrepared(mixed))
	p.step(t, referenceExec("CREATE TABLE n(a SMALLINT,b INTEGER,c BIGINT,d NUMERIC,e REAL,f DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO n VALUES(1,2,3,4,5,6)", 1))
	for _, left := range []struct {
		name, typ string
		value     any
	}{{"a", "INT2", int64(1)}, {"b", "INT4", int64(2)}, {"c", "INT8", int64(3)}} {
		for _, right := range []string{"a", "b", "c"} {
			var want any = left.value
			if left.name == right {
				want = nil
			}
			step := referenceSelect("SELECT NULLIF("+left.name+","+right+") FROM n", []string{"nullif"}, []string{left.typ}, [][]any{{want}})
			p.step(t, step)
			p.step(t, referencePrepared(step))
		}
	}
	for _, tc := range []struct {
		expr, typ string
		want      any
	}{
		{"NULLIF(b,d)", "NUMERIC", "2"}, {"NULLIF(d,b)", "NUMERIC", "4"},
		{"NULLIF(e,f)", "FLOAT4", float64(5)}, {"NULLIF(f,e)", "FLOAT8", float64(6)},
		{"NULLIF(b,e)", "FLOAT8", float64(2)}, {"NULLIF(e,b)", "FLOAT4", float64(5)},
		{"COALESCE(NULL,a,b,c)", "INT8", int64(1)}, {"GREATEST(a,b,d)", "NUMERIC", "4"},
		{"LEAST(d,e,f)", "FLOAT8", float64(4)},
	} {
		name := "nullif"
		if tc.expr[0] == 'C' {
			name = "coalesce"
		}
		if tc.expr[0] == 'G' {
			name = "greatest"
		}
		if tc.expr[0] == 'L' {
			name = "least"
		}
		step := referenceSelect("SELECT "+tc.expr+" FROM n", []string{name}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE chars(a VARCHAR(3),b VARCHAR(3),c VARCHAR(4),p CHAR(3),q CHAR(3),s TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO chars VALUES('x','y','z','x','x ','z')", 1))
	for _, tc := range []struct {
		expr, typ string
		want      any
	}{
		{"COALESCE(a,b)", "VARCHAR", "x"}, {"COALESCE(a,c)", "VARCHAR", "x"}, {"NULLIF(a,b)", "TEXT", "x"},
		{"NULLIF(p,q)", "BPCHAR", nil}, {"GREATEST(p,q)", "BPCHAR", "x  "}, {"LEAST(a,s)", "VARCHAR", "x"},
	} {
		name := "nullif"
		switch tc.expr[0] {
		case 'C':
			name = "coalesce"
		case 'G':
			name = "greatest"
		case 'L':
			name = "least"
		}
		step := referenceSelect("SELECT "+tc.expr+" FROM chars", []string{name}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE temporal(d DATE,t TIMESTAMP,u TIME)", 0))
	p.step(t, referenceExec("INSERT INTO temporal VALUES('2000-01-01','2000-01-02','01:00:00')", 1))
	for _, tc := range []struct {
		expr, typ string
		want      any
	}{
		{"COALESCE(d,t)", "TIMESTAMP", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"GREATEST(d,t)", "TIMESTAMP", time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"NULLIF(t,d)", "TIMESTAMP", time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"CASE d WHEN t THEN TRUE ELSE FALSE END", "BOOL", false},
	} {
		name := "case"
		switch tc.expr[0] {
		case 'C':
			if tc.expr[1] == 'O' {
				name = "coalesce"
			}
		case 'G':
			name = "greatest"
		case 'N':
			name = "nullif"
		}
		step := referenceSelect("SELECT "+tc.expr+" FROM temporal", []string{name}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	// DATE adapters intentionally differ: assert the logical DATE result and
	// each public adapter's existing value contract independently.
	for _, backend := range []struct {
		db     referenceExecutor
		record *referenceRecorder
		want   any
	}{{p.memory, p.memoryRecord, "2000-01-01"}, {p.postgres, p.postgresRecord, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		for _, expr := range []string{"COALESCE(d,d)", "NULLIF(d,t)", "GREATEST(d,d)"} {
			name := "coalesce"
			if expr[0] == 'N' {
				name = "nullif"
			}
			if expr[0] == 'G' {
				name = "greatest"
			}
			step := referenceSelect("SELECT "+expr+" FROM temporal", []string{name}, []string{"DATE"}, [][]any{{backend.want}})
			referenceRunStep(t, p.ctx, backend.db, backend.record, step)
			referenceRunStep(t, p.ctx, backend.db, backend.record, referencePrepared(step))
		}
	}
	// TIME likewise has distinct public adapter representations. Keep exact
	// expected values for each backend rather than relaxing row comparison.
	for _, backend := range []struct {
		db     referenceExecutor
		record *referenceRecorder
		want   any
	}{
		{p.memory, p.memoryRecord, "01:00:00"},
		{p.postgres, p.postgresRecord, time.Date(0, time.January, 1, 1, 0, 0, 0, time.UTC)},
	} {
		for _, tc := range []struct{ expr, name string }{{"COALESCE(u,u)", "coalesce"}, {"NULLIF(u,NULL)", "nullif"}, {"GREATEST(u,u)", "greatest"}} {
			step := referenceSelect("SELECT "+tc.expr+" FROM temporal", []string{tc.name}, []string{"TIME"}, [][]any{{backend.want}})
			referenceRunStep(t, p.ctx, backend.db, backend.record, step)
			referenceRunStep(t, p.ctx, backend.db, backend.record, referencePrepared(step))
		}
	}
	p.step(t, referenceExec("CREATE TABLE bin(u UUID,b BYTEA)", 0))
	p.step(t, referenceExec("INSERT INTO bin VALUES('00000000-0000-0000-0000-000000000001','\\x01')", 1))
	for _, tc := range []struct {
		expr, typ string
		want      any
	}{{"COALESCE(u,u)", "UUID", "00000000-0000-0000-0000-000000000001"}, {"NULLIF(u,u)", "UUID", nil}, {"GREATEST(u,u)", "UUID", "00000000-0000-0000-0000-000000000001"}, {"COALESCE(b,b)", "BYTEA", string([]byte{1})}, {"NULLIF(b,b)", "BYTEA", nil}, {"LEAST(b,b)", "BYTEA", string([]byte{1})}} {
		name := "coalesce"
		switch tc.expr[0] {
		case 'N':
			name = "nullif"
		case 'G':
			name = "greatest"
		case 'L':
			name = "least"
		}
		step := referenceSelect("SELECT "+tc.expr+" FROM bin", []string{name}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, tc := range []struct{ q, message, pos string }{
		{"SELECT COALESCE(d,u) FROM temporal", "COALESCE could not convert type time without time zone to date", "19"},
		{"SELECT COALESCE(t,u) FROM temporal", "COALESCE could not convert type time without time zone to timestamp without time zone", "19"},
		{"SELECT COALESCE(u,b) FROM bin", "COALESCE could not convert type bytea to uuid", "19"},
	} {
		step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42846", Message: tc.message, Position: tc.pos})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
	preferred := referenceImplicitError("SELECT COALESCE(f,b,TRUE) FROM n", referenceDiagnostic{Code: "42804", Message: "COALESCE types double precision and boolean cannot be matched", Position: "21"})
	preferred.selectRows = true
	preferred.errorStage = "execute"
	p.step(t, preferred)
	preferred.errorStage = "prepare"
	p.step(t, referencePrepared(preferred))
}

func TestReferenceConditionalNaNAndTies(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE specials(n NUMERIC,f DOUBLE PRECISION,z DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO specials VALUES('NaN','NaN',0)", 1))
	for _, step := range []referenceStep{
		referenceSelect("SELECT NULLIF(n,n),NULLIF(f,f),GREATEST(n,1),LEAST(n,1),NULLIF(GREATEST(f,z),f),LEAST(f,z) FROM specials", []string{"nullif", "nullif", "greatest", "least", "nullif", "least"}, []string{"NUMERIC", "FLOAT8", "NUMERIC", "NUMERIC", "FLOAT8", "FLOAT8"}, [][]any{{nil, nil, "NaN", "1", nil, float64(0)}}),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
