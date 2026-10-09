package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypeNativeRoundTripSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,c VARCHAR(5),n INT DEFAULT 7 CONSTRAINT nn NOT NULL)", 0),
		referenceExec("INSERT INTO t VALUES(1,'abc',7),(2,'Z',8)", 2),
		referenceExec("COMMENT ON COLUMN t.n IS 'retained'", 0),
		referenceExec(`ALTER TABLE t ALTER c TYPE VARCHAR(2) COLLATE pg_catalog."C" USING c::varchar(2), ALTER n TYPE BIGINT USING n+1`, 0),
		referenceSelect("SELECT c,n FROM t ORDER BY id", []string{"c", "n"}, []string{"VARCHAR", "INT8"}, [][]any{{"ab", int64(8)}, {"Z", int64(9)}}),
	} {
		for _, b := range p.backends() {
			s.nonContext = !s.prepared
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
	}
	for _, b := range p.backends() {
		step := referenceExec("CREATE TABLE numeric_control(c NUMERIC(8,3) CONSTRAINT ck CHECK(c>0) NO INHERIT)", 0)
		step.nonContext = true
		referenceRunStep(t, p.ctx, b.db, b.recorder, step)
	}
	for _, b := range p.backends() {
		step := referenceExec("INSERT INTO numeric_control VALUES(1.235)", 1)
		step.nonContext = true
		referenceRunStep(t, p.ctx, b.db, b.recorder, step)
	}
	for _, b := range p.backends() {
		step := referenceExec("ALTER TABLE numeric_control ALTER c TYPE NUMERIC(5,2)", 0)
		step.nonContext = true
		referenceRunStep(t, p.ctx, b.db, b.recorder, step)
	}
	for _, b := range p.backends() {
		step := referenceSelect("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}})
		step.nonContext = true
		referenceRunStep(t, p.ctx, b.db, b.recorder, step)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{`COLLATE "pg_catalog"."C"`, "BIGINT", "retained", "nn"} {
		if !strings.Contains(dump.String(), literal) {
			t.Fatal(literal, dump.String())
		}
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err := p.memory.Query("SELECT c FROM numeric_control")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := rows.ColumnTypes()
	defer rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if precision, scale, ok := metadata[0].DecimalSize(); !ok || precision != 5 || scale != 2 {
		t.Fatal(precision, scale, ok)
	}
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	if err := stuntgres.Restore(ctx, p.native, &catalogControlCancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	retainedStep := referenceSelect("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}})
	retainedStep.nonContext = true
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, retainedStep)
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var retained string
	if err := rows.Scan(&retained); err != nil || retained != "1.24" {
		t.Fatal(retained, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String(), dump.String())
	}
	for _, s := range []referenceStep{
		referenceSelect("SELECT c,n FROM t ORDER BY id", []string{"c", "n"}, []string{"VARCHAR", "INT8"}, [][]any{{"ab", int64(8)}, {"Z", int64(9)}}),
		referenceSelect("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}}),
		referenceExec("INSERT INTO numeric_control VALUES(1.235)", 1),
		referenceSelect("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}, {"1.24"}}),
		referenceImplicitError("INSERT INTO numeric_control VALUES(-1)", referenceDiagnostic{Code: "23514", Message: `new row for relation "numeric_control" violates check constraint "ck"`, Detail: "Failing row contains (-1.00).", Schema: "public", Table: "numeric_control", Constraint: "ck"}),
		referenceExec("INSERT INTO t(id,c) VALUES(3,'new'::varchar(2))", 1),
		referenceSelect("SELECT n FROM t ORDER BY id", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}, {int64(7)}}),
		referenceImplicitError("INSERT INTO t VALUES(4,'x',NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (4, x, null).", Schema: "public", Table: "t", Column: "n"}),
	} {
		for _, b := range p.backends() {
			s.nonContext = !s.prepared
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
	}
}
