package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6/contrib/spi/sql/refint.sql and expected/refint.out independently
// specify this recursive witness. State assertions additionally prove rollback.
func TestReferenceRefintNestedAtomicity(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		for _,q:=range []string{
			"CREATE TABLE pkeys(pkey1 int4 NOT NULL,pkey2 text NOT NULL)",
			"CREATE TABLE fkeys(fkey1 int4,fkey2 text,fkey3 int)",
			"CREATE TABLE fkeys2(fkey21 int4,fkey22 text,pkey23 int NOT NULL)",
			"CREATE INDEX fkeys_i ON fkeys(fkey1,fkey2)",
			"CREATE INDEX fkeys2_i ON fkeys2(fkey21,fkey22)",
			"CREATE INDEX fkeys2p_i ON fkeys2(pkey23)",
			"CREATE UNIQUE INDEX pkeys_i ON pkeys(pkey1,pkey2)",
			"CREATE TRIGGER check_fkeys_pkey_exist AFTER INSERT OR UPDATE ON fkeys FOR EACH ROW EXECUTE FUNCTION check_primary_key('fkey1','fkey2','pkeys','pkey1','pkey2')",
			"CREATE TRIGGER check_fkeys_pkey2_exist AFTER INSERT OR UPDATE ON fkeys FOR EACH ROW EXECUTE FUNCTION check_primary_key('fkey3','fkeys2','pkey23')",
			"CREATE TRIGGER check_fkeys2_pkey_exist AFTER INSERT OR UPDATE ON fkeys2 FOR EACH ROW EXECUTE FUNCTION check_primary_key('fkey21','fkey22','pkeys','pkey1','pkey2')",
			"CREATE TRIGGER check_pkeys_fkey_cascade AFTER DELETE OR UPDATE ON pkeys FOR EACH ROW EXECUTE FUNCTION check_foreign_key('2','cascade','pkey1','pkey2','fkeys','fkey1','fkey2','fkeys2','fkey21','fkey22')",
			"CREATE TRIGGER check_fkeys2_fkey_restrict AFTER DELETE OR UPDATE ON fkeys2 FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','restrict','pkey23','fkeys','fkey3')",
		} { run(fixture.Exec(q,0)) }
		run(fixture.Exec("INSERT INTO pkeys VALUES(10,'1'),(20,'2'),(30,'3'),(40,'4'),(50,'5'),(60,'6')",6))
		run(fixture.Exec("INSERT INTO fkeys2 VALUES(10,'1',1),(30,'3',2),(40,'4',5),(50,'5',3)",4))
		run(fixture.Exec("INSERT INTO fkeys VALUES(10,'1',2),(30,'3',3),(40,'4',2),(50,'5',2)",4))
		d:=refintError("09000",`"check_fkeys2_fkey_restrict": tuple is referenced in "fkeys"`)
		d.Context=`SQL statement "delete from fkeys2 where fkey21 = $1 and fkey22 = $2 "`
		run(fixture.ErrorDiagnostic("DELETE FROM pkeys WHERE pkey1=30 AND pkey2='3'",d))
		refintNotices(t,notices,"check_pkeys_fkey_cascade: 1 tuple(s) of fkeys are deleted")
		run(fixture.Select("SELECT pkey1,pkey2 FROM pkeys ORDER BY pkey1",[]string{"pkey1","pkey2"},[]string{"INT4","TEXT"},[][]any{{int64(10),"1"},{int64(20),"2"},{int64(30),"3"},{int64(40),"4"},{int64(50),"5"},{int64(60),"6"}}))
		run(fixture.Select("SELECT fkey1,fkey2,fkey3 FROM fkeys ORDER BY fkey1",[]string{"fkey1","fkey2","fkey3"},[]string{"INT4","TEXT","INT4"},[][]any{{int64(10),"1",int64(2)},{int64(30),"3",int64(3)},{int64(40),"4",int64(2)},{int64(50),"5",int64(2)}}))
		run(fixture.Select("SELECT fkey21,fkey22,pkey23 FROM fkeys2 ORDER BY fkey21",[]string{"fkey21","fkey22","pkey23"},[]string{"INT4","TEXT","INT4"},[][]any{{int64(10),"1",int64(1)},{int64(30),"3",int64(2)},{int64(40),"4",int64(5)},{int64(50),"5",int64(3)}}))
		run(fixture.Exec("DELETE FROM pkeys WHERE pkey1=40 AND pkey2='4'",1))
		refintNotices(t,notices,"check_pkeys_fkey_cascade: 1 tuple(s) of fkeys are deleted","check_pkeys_fkey_cascade: 1 tuple(s) of fkeys2 are deleted")
		run(fixture.Exec("UPDATE pkeys SET pkey1=7,pkey2='70' WHERE pkey1=50 AND pkey2='5'",1))
		refintNotices(t,notices,"check_pkeys_fkey_cascade: 1 tuple(s) of fkeys are updated","check_pkeys_fkey_cascade: 1 tuple(s) of fkeys2 are updated")
		run(fixture.Select("SELECT fkey1,fkey2,fkey3 FROM fkeys ORDER BY fkey1",[]string{"fkey1","fkey2","fkey3"},[]string{"INT4","TEXT","INT4"},[][]any{{int64(7),"70",int64(2)},{int64(10),"1",int64(2)},{int64(30),"3",int64(3)}}))
		run(fixture.Select("SELECT fkey21,fkey22,pkey23 FROM fkeys2 ORDER BY fkey21",[]string{"fkey21","fkey22","pkey23"},[]string{"INT4","TEXT","INT4"},[][]any{{int64(7),"70",int64(3)},{int64(10),"1",int64(1)},{int64(30),"3",int64(2)}}))
		unique := refintError("23505", `duplicate key value violates unique constraint "pkeys_i"`)
		unique.Detail, unique.Schema, unique.Table, unique.Constraint = "Key (pkey1, pkey2)=(7, 70) already exists.", "public", "pkeys", "pkeys_i"
		run(fixture.ErrorDiagnostic("UPDATE pkeys SET pkey1=7,pkey2='70' WHERE pkey1=10 AND pkey2='1'", unique))
		refintNotices(t,notices)
	})
}
