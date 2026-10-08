//go:build reference

package reference_test

import "testing"

func TestReferenceQueryLimitInsertAssignment(t *testing.T) {
	for _,prepared:=range []bool{false,true} {
		t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T){
			p:=referenceLimitFixture(t)
			p.step(t,referenceExec("CREATE TABLE dst(id SMALLINT)",0))
			execute:=func(step referenceStep){t.Helper();if prepared{step=referencePrepared(step)};p.step(t,step)}
			execute(referenceExec("INSERT INTO dst VALUES(1),(32768) LIMIT 1",1))
			execute(referenceExec("INSERT INTO dst VALUES(32768) LIMIT 0",0))
			execute(referenceExec("INSERT INTO dst VALUES(2),(32768),(3) OFFSET 2 LIMIT 1",1))
			step:=referenceImplicitError("INSERT INTO dst VALUES(2),(32768) LIMIT 2",referenceDiagnostic{Code:"22003",Message:"smallint out of range"})
			step.errorStage="execute"
			execute(step)
			execute(referenceSelect("SELECT id FROM dst ORDER BY id",[]string{"id"},[]string{"INT2"},referenceLimitIDs(1,3)))
			p.step(t,referenceExec("CREATE SEQUENCE s START WITH 1",0))
			p.step(t,referenceExec("CREATE TABLE defaults(id INT,n BIGINT DEFAULT nextval('s'))",0))
			execute(referenceExec("INSERT INTO defaults(id) SELECT id FROM t ORDER BY id OFFSET 1 LIMIT 1",1))
			execute(referenceExec("INSERT INTO defaults(id) SELECT id FROM t LIMIT 0",0))
			execute(referenceSelect("SELECT n FROM defaults",[]string{"n"},[]string{"INT8"},referenceLimitIDs(1)))
			execute(referenceSelect("SELECT nextval('s')",[]string{"nextval"},[]string{"INT8"},referenceLimitIDs(2)))
			execute(referenceExec("INSERT INTO t SELECT id,v FROM t ORDER BY id LIMIT 1",1))
			execute(referenceSelect("SELECT id FROM t ORDER BY id",[]string{"id"},[]string{"INT4"},referenceLimitIDs(1,1,2,3,4)))
		})
	}
}
