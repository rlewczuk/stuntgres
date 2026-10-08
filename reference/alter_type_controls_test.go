//go:build reference

package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterTypeNativeRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,c VARCHAR(5),n INT DEFAULT 7 CONSTRAINT nn NOT NULL)", 0),
		referenceExec("INSERT INTO t VALUES(1,'abc',7),(2,'Z',8)", 2),
		referenceExec("COMMENT ON COLUMN t.n IS 'retained'", 0),
		referenceExec(`ALTER TABLE t ALTER c TYPE VARCHAR(2) COLLATE pg_catalog."C" USING c::varchar(2), ALTER n TYPE BIGINT USING n+1`, 0),
		referenceSelect("SELECT c,n FROM t ORDER BY id", []string{"c", "n"}, []string{"VARCHAR", "INT8"}, [][]any{{"ab", int64(8)}, {"Z", int64(9)}}),
	} {
		p.step(t, s)
	}
	p.step(t, referenceExec("CREATE TABLE numeric_control(c NUMERIC(8,3) CONSTRAINT ck CHECK(c>0) NO INHERIT)", 0))
	p.step(t, referenceExec("INSERT INTO numeric_control VALUES(1.235)", 1))
	p.step(t, referenceExec("ALTER TABLE numeric_control ALTER c TYPE NUMERIC(5,2)", 0))
	p.step(t, referenceSelect("SELECT c FROM numeric_control", []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}}))
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
	rows, err := p.memory.QueryContext(p.ctx, "SELECT c FROM numeric_control")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := rows.ColumnTypes()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if precision, scale, ok := metadata[0].DecimalSize(); !ok || precision != 5 || scale != 2 {
		t.Fatal(precision, scale, ok)
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
		p.step(t, s)
	}
}
