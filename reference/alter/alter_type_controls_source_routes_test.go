package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypeNativeRoundTripSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(id INT,c VARCHAR(5),n INT DEFAULT 7 CONSTRAINT nn NOT NULL)", 0),
		fixture.Exec("INSERT INTO t VALUES(1,'abc',7),(2,'Z',8)", 2),
		fixture.Exec("COMMENT ON COLUMN t.n IS 'retained'", 0),
		fixture.Exec(`ALTER TABLE t ALTER c TYPE VARCHAR(2) COLLATE pg_catalog."C" USING c::varchar(2), ALTER n TYPE BIGINT USING n+1`, 0),
		fixture.Select("SELECT c,n FROM t ORDER BY id", []string{"c", "n"}, []string{"VARCHAR", "INT8"}, [][]any{{"ab", int64(8)}, {"Z", int64(9)}}),
	} {
		for _, b := range p.Backends() {
			s.NonContext = !s.Prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
	for _, b := range p.Backends() {
		step := fixture.Exec("CREATE TABLE numeric_control(c NUMERIC(8,3) CONSTRAINT ck CHECK(c>0) NO INHERIT)", 0)
		step.NonContext = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
	}
	for _, b := range p.Backends() {
		step := fixture.Exec("INSERT INTO numeric_control VALUES(1.235)", 1)
		step.NonContext = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
	}
	for _, b := range p.Backends() {
		step := fixture.Exec("ALTER TABLE numeric_control ALTER c TYPE NUMERIC(5,2)", 0)
		step.NonContext = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
	}
	for _, b := range p.Backends() {
		step := fixture.Select("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}})
		step.NonContext = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{`COLLATE "pg_catalog"."C"`, "BIGINT", "retained", "nn"} {
		if !strings.Contains(dump.String(), literal) {
			t.Fatal(literal, dump.String())
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err := p.Memory.Query("SELECT c FROM numeric_control")
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
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	if err := stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	retainedStep := fixture.Select("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}})
	retainedStep.NonContext = true
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, retainedStep)
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
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String(), dump.String())
	}
	for _, s := range []fixture.Step{
		fixture.Select("SELECT c,n FROM t ORDER BY id", []string{"c", "n"}, []string{"VARCHAR", "INT8"}, [][]any{{"ab", int64(8)}, {"Z", int64(9)}}),
		fixture.Select("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}}),
		fixture.Exec("INSERT INTO numeric_control VALUES(1.235)", 1),
		fixture.Select("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}, {"1.24"}}),
		fixture.ImplicitError("INSERT INTO numeric_control VALUES(-1)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "numeric_control" violates check constraint "ck"`, Detail: "Failing row contains (-1.00).", Schema: "public", Table: "numeric_control", Constraint: "ck"}),
		fixture.Exec("INSERT INTO t(id,c) VALUES(3,'new'::varchar(2))", 1),
		fixture.Select("SELECT n FROM t ORDER BY id", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}, {int64(7)}}),
		fixture.ImplicitError("INSERT INTO t VALUES(4,'x',NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (4, x, null).", Schema: "public", Table: "t", Column: "n"}),
	} {
		for _, b := range p.Backends() {
			s.NonContext = !s.Prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
}
