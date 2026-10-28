package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterAccessMethodAtomicity(t *testing.T) {
	for _,prepared:=range []bool{false,true} {t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
		accessMethodScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			persistenceExec(t,p,b,c,"CREATE TABLE t(id int,note text)")
			persistenceRun(t,p,b,c,fixture.Exec("INSERT INTO t VALUES(7,'keep')",1))
			selectSQL,insertSQL:="SELECT id,note FROM t ORDER BY id","INSERT INTO t(id,note) VALUES($1,$2)"
			selected,err:=c.PrepareContext(p.Ctx,selectSQL);if err!=nil {t.Fatal(err)};defer selected.Close()
			inserted,err:=c.PrepareContext(p.Ctx,insertSQL);if err!=nil {t.Fatal(err)};defer inserted.Close()
			want:=persistenceDiagnostic("23514",`check constraint "bad" of relation "t" is violated by some row`)
			want.Schema,want.Table,want.Constraint="public","t","bad"
			persistenceFailure(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD heap2, ADD COLUMN added int DEFAULT 9, ADD CONSTRAINT bad CHECK(id<0)",prepared,want)
			accessMethodProperty(t,p,b,c,"t","heap")
			persistenceRun(t,p,b,c,accessMethodRows([][]any{{int64(7),"keep"}}))
			persistenceRun(t,p,b,c,fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='t'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum",[]string{"attname"},[]string{"NAME"},[][]any{{"id"},{"note"}}))
			persistenceFailure(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD heap2, ADD COLUMN impossible int NOT NULL",prepared,persistenceDiagnosticNotNull())
			accessMethodProperty(t,p,b,c,"t","heap")
			persistenceRun(t,p,b,c,fixture.Select("SELECT * FROM t",[]string{"id","note"},[]string{"INT4","TEXT"},[][]any{{int64(7),"keep"}}))
			persistenceFailure(t,p,b,c,"ALTER TABLE t SET ACCESS METHOD heap2, SET ACCESS METHOD heap3",prepared,persistenceDiagnostic("0A000","cannot have multiple SET ACCESS METHOD subcommands"))
			accessMethodProperty(t,p,b,c,"t","heap")
			peer,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer peer.Close()
			tx,err:=c.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
			step:=fixture.Exec("ALTER TABLE t SET ACCESS METHOD heap2",0);if prepared {step=fixture.Prepared(step)}
			persistenceRun(t,p,b,tx,step)
			accessMethodProperty(t,p,b,tx,"t","heap2");accessMethodProperty(t,p,b,peer,"t","heap")
			if err=tx.Rollback();err!=nil {t.Fatal(err)}
			accessMethodProperty(t,p,b,c,"t","heap")
			persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:selected,SQL:selectSQL},accessMethodRows([][]any{{int64(7),"keep"}}))
			tx,err=c.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
			persistenceRun(t,p,b,tx,step)
			if err=tx.Commit();err!=nil {t.Fatal(err)}
			accessMethodProperty(t,p,b,peer,"t","heap2")
			tx,err=c.BeginTx(p.Ctx,nil);if err!=nil {t.Fatal(err)};defer tx.Rollback()
			for _,q:=range []string{"ALTER TABLE t ADD COLUMN outer_col int DEFAULT 4","SAVEPOINT keep","ALTER TABLE t SET ACCESS METHOD heap3","ROLLBACK TO keep"} {persistenceExec(t,p,b,tx,q)}
			accessMethodProperty(t,p,b,tx,"t","heap2")
			persistenceRun(t,p,b,tx,fixture.Select("SELECT outer_col FROM t",[]string{"outer_col"},[]string{"INT4"},[][]any{{int64(4)}}))
			persistenceExec(t,p,b,tx,"ALTER TABLE t SET ACCESS METHOD heap3")
			if err=tx.Commit();err!=nil {t.Fatal(err)}
			accessMethodProperty(t,p,b,peer,"t","heap3")
			persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:inserted,SQL:insertSQL},fixture.Exec(insertSQL,1,8,"new"))
			persistenceRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:selected,SQL:selectSQL},accessMethodRows([][]any{{int64(7),"keep"},{int64(8),"new"}}))
			persistenceRun(t,p,b,peer,fixture.Select("SELECT id,outer_col FROM t ORDER BY id",[]string{"id","outer_col"},[]string{"INT4","INT4"},[][]any{{int64(7),int64(4)},{int64(8),int64(4)}}))
		})
	})}
}

func persistenceDiagnosticNotNull() fixture.Diagnostic {
	want:=persistenceDiagnostic("23502",`column "impossible" of relation "t" contains null values`)
	want.Schema,want.Table,want.Column="public","t","impossible"
	return want
}
