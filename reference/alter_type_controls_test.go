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
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String(), dump.String())
	}
	for _, s := range []referenceStep{
		referenceSelect("SELECT c,n FROM t ORDER BY id", []string{"c", "n"}, []string{"VARCHAR", "INT8"}, [][]any{{"ab", int64(8)}, {"Z", int64(9)}}),
		referenceExec("INSERT INTO t(id,c) VALUES(3,'new'::varchar(2))", 1),
		referenceSelect("SELECT n FROM t ORDER BY id", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}, {int64(7)}}),
		referenceImplicitError("INSERT INTO t VALUES(4,'x',NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (4, x, null).", Schema: "public", Table: "t", Column: "n"}),
	} {
		p.step(t, s)
	}
}
