//go:build reference

package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceSerialControls(t *testing.T) {
	p := newReferencePair(t)
	for _,step := range []referenceStep{
		referenceExec(`CREATE TABLE called(id SERIAL,v TEXT)`,0),
		referenceExec(`CREATE TABLE untouched(a SMALLSERIAL,b BIGSERIAL)`,0),
		referenceExec(`INSERT INTO called VALUES(100,'duplicate'),(100,'duplicate'),(0,'zero'),(-1,'negative')`,4),
		referenceSelect(`SELECT setval('called_id_seq',7,true)`,[]string{"setval"},[]string{"INT8"},[][]any{{int64(7)}}),
		referenceExec(`ALTER SEQUENCE called_id_seq RENAME TO renamed`,0),
		referenceExec(`ALTER SEQUENCE renamed MAXVALUE 10000 CACHE 1`,0),
		referenceExec(`COMMENT ON SEQUENCE renamed IS 'serial generator'`,0),
	} { referenceRunStep(t,p.ctx,p.memory,p.memoryRecord,step) }
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx,p.native,&dump); err != nil { t.Fatal(err) }
	if _,err := p.postgres.ExecContext(p.ctx,dump.String()); err != nil { t.Fatal("pinned replay",err) }
	if err := stuntgres.Reset(p.ctx,p.native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.ctx,p.native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	for _,step := range []referenceStep{
		referenceSelect(`SELECT last_value,is_called FROM renamed`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(7),true}}),
		referenceSelect(`SELECT last_value,is_called FROM untouched_a_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}),
		referencePrepared(referenceExec(`INSERT INTO called(v) VALUES('future')`,1)),
		referenceExec(`INSERT INTO untouched VALUES(DEFAULT,DEFAULT)`,1),
		referenceSelect(`SELECT * FROM untouched`,[]string{"a","b"},[]string{"INT2","INT8"},[][]any{{int64(1),int64(1)}}),
		referenceSelect(`SELECT * FROM called ORDER BY id`,[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(-1),"negative"},{int64(0),"zero"},{int64(8),"future"},{int64(100),"duplicate"},{int64(100),"duplicate"}}),
		referenceImplicitError(`DROP SEQUENCE renamed RESTRICT`,referenceDiagnostic{Code:"2BP01",Message:`cannot drop sequence renamed because other objects depend on it`,Detail:`default value for column id of table called depends on sequence renamed`,Hint:`Use DROP ... CASCADE to drop the dependent objects too.`}),
		referenceExec(`DROP TABLE called RESTRICT`,0),
		referenceExec(`CREATE SEQUENCE renamed`,0),
	} { p.step(t,step) }
}
