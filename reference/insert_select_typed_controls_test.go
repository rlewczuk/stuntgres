//go:build reference

package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceInsertSelectTypedControls(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE s(n NUMERIC(5,2),stamp TIMESTAMP(3),b BIGINT,u UUID)", 0),
		referenceExec("INSERT INTO s VALUES('1.234',TIMESTAMP '2000-01-01 00:00:00.1234',2147483648,'A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11'),(NULL,NULL,NULL,NULL)", 2),
		referenceExec("CREATE TABLE t(n NUMERIC(5,2),stamp TIMESTAMP(3),b BIGINT,u UUID)", 0),
		referencePrepared(referenceExec("INSERT INTO t SELECT * FROM s", 2)),
	} {
		p.step(t, s)
	}
	expected := referenceSelect("SELECT * FROM t ORDER BY n", []string{"n", "stamp", "b", "u"}, []string{"NUMERIC", "TIMESTAMP", "INT8", "UUID"}, [][]any{{"1.23", time.Date(2000, 1, 1, 0, 0, 0, 123000000, time.UTC), int64(2147483648), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}, {nil, nil, nil, nil}})
	p.step(t, expected)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	p.step(t, expected)
}
