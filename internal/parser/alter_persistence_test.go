package parser

import (
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestAlterPersistenceGrammar(t *testing.T) {
	for _,q:=range []string{
		"CREATE UNLOGGED TABLE t(id int)",
		"CREATE UNLOGGED TABLE IF NOT EXISTS t(id int)",
		"CREATE UNLOGGED TABLE t OF pair",
		"CREATE UNLOGGED TABLE IF NOT EXISTS t OF pair",
		"CREATE UNLOGGED TABLE t PARTITION OF p FOR VALUES FROM(0) TO(10)",
		"CREATE UNLOGGED TABLE IF NOT EXISTS t PARTITION OF p FOR VALUES FROM(0) TO(10)",
	} {
		statements,err:=Parse(q)
		if err!=nil || len(statements)!=1 {t.Fatal(q,statements,err)}
		create,ok:=statements[0].(*CreateTable)
		if !ok || create.Persistence!="unlogged" || create.Pos.Offset!=0 {t.Fatal(q,create)}
	}
	for _,q:=range []string{
		"CREATE UNLOGGED TABLE t AS SELECT 1 AS i",
		"CREATE UNLOGGED TABLE t(i) AS SELECT 1 AS i",
		"CREATE UNLOGGED TABLE IF NOT EXISTS t AS SELECT 1 AS i",
		"CREATE UNLOGGED TABLE IF NOT EXISTS t(i) AS SELECT 1 AS i",
	} {
		statements,err:=Parse(q)
		diagnostic,ok:=err.(*sqlstate.Error)
		if len(statements)!=0 || !ok || diagnostic.Code!="0A000" || diagnostic.Message!="CREATE syntax is unsupported" || diagnostic.Position!="8" {
			t.Fatal("query-derived persistence boundary",q,statements,err)
		}
	}
	for _,q:=range []string{"ALTER TABLE t SET LOGGED, SET UNLOGGED","ALTER TABLE ONLY(t) SET LOGGED, SET UNLOGGED","ALTER TABLE IF EXISTS t SET LOGGED, SET UNLOGGED"} {
		statements,err:=Parse(q);if err!=nil || len(statements)!=1 {t.Fatal(q,err)}
		alter:=statements[0].(*AlterTable)
		if len(alter.Actions)!=2 || alter.Actions[0].Action!=SetLogged || alter.Actions[1].Action!=SetUnlogged || alter.Actions[0].Pos.Offset>=alter.Actions[1].Pos.Offset {t.Fatal(alter)}
	}
	for _,q:=range []string{"CREATE UNLOGGED TABLE t(id int) garbage","ALTER TABLE t SET UNLOGGED garbage","ALTER TABLE t SET LOGGED, SET"} {
		if _,err:=Parse(q);err==nil {t.Fatal("accepted incomplete/full-input violation",q)}
	}
}
