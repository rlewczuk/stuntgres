package reference_test

import (
	"fmt"
	"testing"
)

func uniqueReferenceDuplicate(table, constraint, detail string) referenceDiagnostic {
	d := controlsReferenceDiagnostic("23505", fmt.Sprintf("duplicate key value violates unique constraint %q", constraint))
	d.Schema, d.Table, d.Constraint, d.Detail = "public", table, constraint, detail
	return d
}

func TestReferenceUniqueFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT t_ab_u UNIQUE NULLS NOT DISTINCT(a,b))", 0))
			run(referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2)", 1, int64(1), "x")))
			run(referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2)", 1, nil, "n")))
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic("INSERT INTO t VALUES($1,$2)", uniqueReferenceDuplicate("t", "t_ab_u", "Key (a, b)=(1, x) already exists."), int64(1), "x")
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(NULL,'n')", uniqueReferenceDuplicate("t", "t_ab_u", "Key (a, b)=(null, n) already exists.")))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(2,'y'),(1,'x')", uniqueReferenceDuplicate("t", "t_ab_u", "Key (a, b)=(1, x) already exists.")))
			want := referenceSelect("SELECT a,b FROM t ORDER BY a NULLS LAST", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {nil, "n"}})
			run(want)
			run(referencePrepared(want))
			run(referenceExec("UPDATE t SET a=a WHERE a=1", 1))
			run(referenceErrorDiagnostic("UPDATE t SET a=1,b='x' WHERE a IS NULL", uniqueReferenceDuplicate("t", "t_ab_u", "Key (a, b)=(1, x) already exists.")))
			run(want)
			run(referenceExec("CREATE TABLE distinct_t(a INT,b TEXT,UNIQUE(a,b))", 0))
			run(referenceExec("INSERT INTO distinct_t VALUES(NULL,'n'),(NULL,'n')", 2))
			run(referenceSelect("SELECT a,b FROM distinct_t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "n"}, {nil, "n"}}))
		})
	}
}

func TestReferenceUniqueValues(t *testing.T) {
	for _, mode := range []string{"", " NULLS DISTINCT", " NULLS NOT DISTINCT"} {
		t.Run(fmt.Sprintf("mode%q", mode), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT,CONSTRAINT u UNIQUE"+mode+"(a,b))", 0))
					run(referenceExec("INSERT INTO t VALUES(1,1),(1,2),(NULL,1),(1,NULL),(NULL,NULL)", 5))
					run(referenceErrorDiagnostic("INSERT INTO t VALUES(1,1)", uniqueReferenceDuplicate("t", "u", "Key (a, b)=(1, 1) already exists.")))
					rows := [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {nil, int64(1)}, {int64(1), nil}, {nil, nil}}
					for _, key := range []struct {
						sql, detail string
						row         []any
					}{
						{"NULL,1", "null, 1", []any{nil, int64(1)}}, {"1,NULL", "1, null", []any{int64(1), nil}}, {"NULL,NULL", "null, null", []any{nil, nil}},
					} {
						q := "INSERT INTO t VALUES(" + key.sql + ")"
						if mode == " NULLS NOT DISTINCT" {
							run(referenceErrorDiagnostic(q, uniqueReferenceDuplicate("t", "u", "Key (a, b)=("+key.detail+") already exists.")))
						} else {
							run(referencePrepared(referenceExec(q, 1)))
							rows = append(rows, key.row)
						}
					}
					run(referenceExec("INSERT INTO t VALUES(NULL,2),(2,NULL)", 2))
					rows = append(rows, []any{nil, int64(2)}, []any{int64(2), nil})
					s := referenceSelect("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, rows)
					s.bag = true
					run(s)
				})
			}
		})
	}
}

func TestReferenceUniqueTypedEquality(t *testing.T) {
	cases := []struct {
		name, typ, first, second string
		collide                  bool
	}{
		{"numeric_scale", "NUMERIC", "1.0", "1.00", true},
		{"numeric_rounding", "NUMERIC(4,1)", "1.04", "1.03", true},
		{"float_zero", "DOUBLE PRECISION", "'0'", "'-0'", true},
		{"float_nan", "DOUBLE PRECISION", "'NaN'", "'NaN'", true},
		{"real_rounding", "REAL", "16777216", "16777217", true},
		{"bpchar", "CHAR(3)", "'x'", "'x '", true},
		{"text", "TEXT", "'x'", "'x '", false},
		{"bytea", "BYTEA", "'\\x6162'", "E'\\\\141\\\\142'", true},
		{"uuid", "UUID", "'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'", "'A0EEBC999C0B4EF8BB6D6BB9BD380A11'", true},
		{"timestamptz", "TIMESTAMPTZ", "'2024-01-01 00:00:00+00'", "'2023-12-31 19:00:00-05'", true},
		{"interval", "INTERVAL", "'1 mon'", "'30 days'", true},
		{"timetz_offset", "TIMETZ", "'12:00:00+00'", "'13:00:00+01'", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE t(k "+c.typ+" CONSTRAINT u UNIQUE)", 0))
					run(referenceExec("INSERT INTO t VALUES("+c.first+")", 1))
					q := "INSERT INTO t VALUES(" + c.second + ")"
					count := int64(2)
					if c.collide {
						output := map[string]string{
							"numeric_scale": "1.00", "numeric_rounding": "1.0",
							"float_zero": "-0", "float_nan": "NaN",
							"real_rounding": "1.6777216e+07", "bpchar": "x  ",
							"bytea": "\\x6162", "uuid": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
							"timestamptz": "2024-01-01 00:00:00+00", "interval": "30 days",
						}[c.name]
						s := referencePrepared(referenceErrorDiagnostic(q, uniqueReferenceDuplicate("t", "u", "Key (k)=("+output+") already exists.")))
						s.errorStage = "execute"
						run(s)
						count = 1
					} else {
						run(referencePrepared(referenceExec(q, 1)))
					}
					run(referenceSelect("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
				})
			}
		})
	}
}
