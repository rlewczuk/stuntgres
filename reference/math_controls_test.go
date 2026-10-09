package reference_test

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
)

func referenceMathStoredViewWitnesses(t *testing.T, p *referencePair) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		null := referenceSelect("SELECT result FROM null_math_view", []string{"result"}, []string{"NUMERIC"}, [][]any{{nil}, {nil}, {nil}})
		null.prepared = prepared
		p.step(t, null)
		failure := referenceImplicitError("SELECT result FROM constant_null_math_view", referenceDiagnostic{Code: "22012", Message: "division by zero"})
		failure.prepared, failure.errorStage = prepared, "execute"
		p.step(t, failure)
	}
}

func TestReferenceMathControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE source(id INT,n NUMERIC,d DOUBLE PRECISION)", 0),
		referenceExec("INSERT INTO source VALUES(1,1.23,'-0.5'),(2,8.4100,'Infinity'),(3,NULL,NULL)", 3),
		referenceExec("CREATE TABLE materialized(id INT,padded NUMERIC,trimmed NUMERIC,remainder NUMERIC,sgn NUMERIC,d DOUBLE PRECISION)", 0),
		referenceExec("INSERT INTO materialized SELECT id,round(n,4),trim_scale(n),mod(n,4.0),sign(n),trunc(d) FROM source", 3),
		referenceExec("CREATE VIEW math_view AS SELECT id,round(n,1) AS rounded,trim_scale(n) AS trimmed,mod(n,4.0) AS remainder FROM source", 0),
		referenceExec("CREATE VIEW null_math_view AS SELECT round(NULL,mod(id,0)) AS result FROM source", 0),
		referenceExec("CREATE VIEW constant_null_math_view AS SELECT round(NULL,mod(1,0)) AS result FROM source", 0),
	} {
		p.step(t, step)
	}
	referenceMathStoredViewWitnesses(t, p)
	view := referenceSelect("SELECT rounded,trimmed,remainder FROM math_view ORDER BY id", []string{"rounded", "trimmed", "remainder"}, []string{"NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"1.2", "1.23", "1.23"}, {"8.4", "8.41", "0.4100"}, {nil, nil, nil}})
	p.step(t, view)
	p.step(t, referencePrepared(view))
	held, err := p.memory.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), "SELECT padded,trimmed FROM materialized ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if p.postgres != nil {
		for _, name := range []string{"math_view", "null_math_view", "constant_null_math_view"} {
			if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW "+name); err != nil {
				t.Fatal(err)
			}
		}
	}
	referenceArithmeticRoundtrip(t, p, "materialized", "source")
	materialized := referenceSelect("SELECT padded,trimmed,remainder,sgn FROM materialized ORDER BY id", []string{"padded", "trimmed", "remainder", "sgn"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"1.2300", "1.23", "1.23", "1"}, {"8.4100", "8.41", "0.4100", "1"}, {nil, nil, nil, nil}})
	p.step(t, materialized)
	p.step(t, referencePrepared(materialized))
	p.step(t, view)
	p.step(t, referencePrepared(view))
	referenceMathStoredViewWitnesses(t, p)
	for _, backend := range p.backends() {
		var zero float64
		if err := backend.db.QueryRowContext(p.ctx, "SELECT d FROM materialized WHERE id=1").Scan(&zero); err != nil || zero != 0 || !math.Signbit(zero) {
			t.Fatalf("%s negative zero=%v error=%v", backend.name, zero, err)
		}
	}
	meta, err := held.ColumnTypes()
	if err != nil || len(meta) != 2 || meta[0].DatabaseTypeName() != "NUMERIC" || meta[1].DatabaseTypeName() != "NUMERIC" {
		t.Fatal(meta, err)
	}
	for _, want := range [][2]string{{"1.2300", "1.23"}, {"8.4100", "8.41"}} {
		if !held.Next() {
			t.Fatal(held.Err())
		}
		var a, b string
		if err := held.Scan(&a, &b); err != nil || a != want[0] || b != want[1] {
			t.Fatal(a, b, err)
		}
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var a, b any
	if err := held.Scan(&a, &b); err != nil || a != nil || b != nil {
		t.Fatal(a, b, err)
	}
	if held.Next() || held.Err() != nil {
		t.Fatal("held math end", held.Err())
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	referenceArithmeticRecordedClosure(t, p.memoryRecord, "SELECT padded,trimmed FROM materialized ORDER BY id", 2, 3)
	const bad = "CREATE TABLE staged(n NUMERIC); INSERT INTO staged VALUES(1),('oops')"
	failure := referenceImplicitError(bad, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: strconv.Itoa(strings.Index(bad, "'oops'") + 1)})
	referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)), failure.diagnostic)
	remaining := referenceSelect("SELECT padded FROM materialized ORDER BY id", []string{"padded"}, []string{"NUMERIC"}, [][]any{{"1.2300"}, {"8.4100"}, {nil}})
	p.step(t, remaining)
	p.step(t, referencePrepared(remaining))
}
