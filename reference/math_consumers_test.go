package reference_test

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"
)

func TestReferenceMathConsumers(t *testing.T) {
	if *referenceMode {
		t.Run("privileges", referenceMathPrivileges)
	}
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE m(id INT,n NUMERIC,d NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO m VALUES(1,1.25,4),(2,1.24,0),(3,-1.25,4)", 3))
	p.step(t, referenceExec("CREATE TABLE group_inputs(n NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO group_inputs VALUES(1.25),(1.26),(1.24),(-1.25)", 4))
	for _, prepared := range []bool{false, true} {
		run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
		grouped := referenceSelect("SELECT round(n,1),count(*) FROM group_inputs GROUP BY 1", []string{"round", "count"}, []string{"NUMERIC", "INT8"}, [][]any{{"-1.3", int64(1)}, {"1.2", int64(1)}, {"1.3", int64(2)}})
		grouped.bag = true
		run(grouped)
		run(referenceSelect("SELECT sign(count(*)),round(count(*),1) FROM m", []string{"sign", "round"}, []string{"FLOAT8", "NUMERIC"}, [][]any{{float64(1), "3.0"}}))
		run(referenceSelect("SELECT round(CASE WHEN true THEN n ELSE 0.0 END,1) FROM m ORDER BY id", []string{"round"}, []string{"NUMERIC"}, [][]any{{"1.3"}, {"1.2"}, {"-1.3"}}))
		run(referenceSelect("SELECT round(trim_scale(n),1) FROM m ORDER BY id", []string{"round"}, []string{"NUMERIC"}, [][]any{{"1.3"}, {"1.2"}, {"-1.3"}}))
		s := referenceImplicitError("UPDATE m SET n=mod(n,d)", referenceDiagnostic{Code: "22012", Message: "division by zero"})
		s.errorStage = "execute"
		run(s)
		run(referenceSelect("SELECT n FROM m ORDER BY id", []string{"n"}, []string{"NUMERIC"}, [][]any{{"1.25"}, {"1.24"}, {"-1.25"}}))
	}
	p.step(t, referenceExec("CREATE VIEW math_view AS SELECT id,round(n,1) AS rounded,trim_scale(n) AS trimmed,mod(n,4.0) AS remainder FROM m", 0))
	s := referenceSelect("SELECT rounded,trimmed,remainder FROM math_view ORDER BY id", []string{"rounded", "trimmed", "remainder"}, []string{"NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"1.3", "1.25", "1.25"}, {"1.2", "1.24", "1.24"}, {"-1.3", "-1.25", "-1.25"}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
	p.step(t, referenceExec("CREATE VIEW null_math_view AS SELECT round(NULL,mod(id,0)) AS result FROM m", 0))
	p.step(t, referenceExec("CREATE VIEW constant_null_math_view AS SELECT round(NULL,mod(1,0)) AS result FROM m", 0))
	for _, prepared := range []bool{false, true} {
		nullView := referenceSelect("SELECT result FROM null_math_view", []string{"result"}, []string{"NUMERIC"}, [][]any{{nil}, {nil}, {nil}})
		nullView.prepared = prepared
		p.step(t, nullView)
		constantView := referenceImplicitError("SELECT result FROM constant_null_math_view", referenceDiagnostic{Code: "22012", Message: "division by zero"})
		constantView.selectRows = true
		constantView.errorStage = "execute"
		constantView.prepared = prepared
		p.step(t, constantView)
	}
	p.step(t, referenceExec("UPDATE m SET n=round(n,1)", 3))
	p.step(t, referenceExec("CREATE TABLE materialized(n NUMERIC)", 0))
	p.step(t, referenceExec("INSERT INTO materialized SELECT trim_scale(n) FROM m", 3))
	p.step(t, referenceSelect("SELECT n FROM materialized ORDER BY n", []string{"n"}, []string{"NUMERIC"}, [][]any{{"-1.3"}, {"1.2"}, {"1.3"}}))
	p.step(t, referenceExec("CREATE TABLE nullable(n NUMERIC,i INT)", 0))
	p.step(t, referenceExec("INSERT INTO nullable VALUES(NULL,9)", 1))
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError("SELECT mod(n,mod(i,0)) FROM nullable", referenceDiagnostic{Code: "22012", Message: "division by zero"})
		s.selectRows = true
		s.errorStage = "execute"
		s.prepared = prepared
		p.step(t, s)
		execError := s
		execError.selectRows = false
		p.step(t, execError)
		s = referenceSelect("SELECT mod(n,0) FROM nullable", []string{"mod"}, []string{"NUMERIC"}, [][]any{{nil}})
		s.prepared = prepared
		p.step(t, s)
	}
}

// Feature-local IEEE assertions do not collapse NaN or the sign of zero in the
// common fixture. Each backend is asserted independently, including preparation.
func referenceMathFloat(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE math_float(id INT,d DOUBLE PRECISION,inf DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO math_float VALUES(1,'-0.5','Infinity'),(2,NULL,NULL)", 2))
	nullTrunc := referenceSelect("SELECT trunc(d) FROM math_float WHERE id=2", []string{"trunc"}, []string{"FLOAT8"}, [][]any{{nil}})
	p.step(t, nullTrunc)
	p.step(t, referencePrepared(nullTrunc))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, tc := range []struct {
				q    string
				want float64
			}{
				{"SELECT round('2.5')", 2}, {"SELECT round('-2.5')", -2}, {"SELECT round('3.5')", 4}, {"SELECT round('-3.5')", -4},
				{"SELECT round('-0.5')", math.Copysign(0, -1)}, {"SELECT trunc(d) FROM math_float WHERE id=1", math.Copysign(0, -1)},
				{"SELECT round('NaN')", math.NaN()}, {"SELECT trunc(inf) FROM math_float WHERE id=1", math.Inf(1)}, {"SELECT round('-Infinity')", math.Inf(-1)},
				{"SELECT sign('NaN')", 0}, {"SELECT sign('-0')", 0}, {"SELECT sign('Infinity')", 1}, {"SELECT sign('-Infinity')", -1},
			} {
				for _, prepared := range []bool{false, true} {
					var row *sql.Rows
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = backend.db.PrepareContext(p.ctx, tc.q)
						if err != nil {
							t.Fatal(err)
						}
						row, err = stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
						if err != nil {
							stmt.Close()
							t.Fatal(err)
						}
					} else {
						var err error
						row, err = backend.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), tc.q)
						if err != nil {
							t.Fatal(err)
						}
					}
					labels, err := row.Columns()
					name := strings.SplitN(strings.TrimPrefix(tc.q, "SELECT "), "(", 2)[0]
					meta, metaErr := row.ColumnTypes()
					if err != nil || len(labels) != 1 || labels[0] != name || metaErr != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "FLOAT8" {
						row.Close()
						t.Fatal("IEEE metadata", labels, meta, err, metaErr)
					}
					if !row.Next() {
						row.Close()
						t.Fatal("missing IEEE row", row.Err())
					}
					var got float64
					err = row.Scan(&got)
					if row.Next() || row.Err() != nil {
						row.Close()
						t.Fatal("IEEE end", row.Err())
					}
					if err := row.Close(); err != nil {
						t.Fatal(err)
					}
					if stmt != nil {
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					referenceArithmeticRecordedClosure(t, backend.recorder, tc.q, 1, 1)
					if err != nil || (math.IsNaN(tc.want) && !math.IsNaN(got)) || (!math.IsNaN(tc.want) && (got != tc.want || math.Signbit(got) != math.Signbit(tc.want))) {
						t.Fatal(tc.q, got, err)
					}
				}
			}
		})
	}
}

func referenceMathScaleLimits(t *testing.T) {
	p := newReferenceScenario(t)
	for _, name := range []string{"round", "trunc"} {
		q := "SELECT " + name + "(1.23,2147483647)"
		want := "1.23" + strings.Repeat("0", 16381)
		referenceArithmeticMetadata(t, p, referenceSelect(q, []string{name}, []string{"NUMERIC"}, [][]any{{want}}))
		step := referenceSelect("SELECT "+name+"(1.23,-2147483648)", []string{name}, []string{"NUMERIC"}, [][]any{{"0"}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, q := range []string{"SELECT round(5.5e131071,-131073)", "SELECT round(5.5e131071,-1000000)", "SELECT trunc(5.5e131071,-131072)"} {
		name := "round"
		if strings.Contains(q, "trunc") {
			name = "trunc"
		}
		step := referenceSelect(q, []string{name}, []string{"NUMERIC"}, [][]any{{"0"}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	s := referenceImplicitError("SELECT round(5.5e131071,-131072)", referenceDiagnostic{Code: "22003", Message: "value overflows numeric format"})
	s.errorStage = "execute"
	p.step(t, s)
	p.step(t, referencePrepared(s))
}
