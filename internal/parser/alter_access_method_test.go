package parser

import "testing"

func TestAlterAccessMethodGrammar(t *testing.T) {
	for _,q:=range []string{
		"ALTER TABLE t SET ACCESS METHOD heap2",
		"ALTER TABLE IF EXISTS ONLY t SET ACCESS METHOD heap2",
		"ALTER TABLE t * SET ACCESS METHOD heap2",
		"ALTER MATERIALIZED VIEW t SET ACCESS METHOD heap2",
		"ALTER MATERIALIZED VIEW IF EXISTS t SET ACCESS METHOD heap2",
	} {
		statements,err:=Parse(q);if err!=nil {t.Fatal(q,err)}
		alter,ok:=statements[0].(*AlterTable)
		if !ok || len(alter.Actions)!=1 || alter.Actions[0].Action!=SetAccessMethod || alter.Actions[0].Method.Text!="heap2" || alter.Actions[0].MethodDefault || alter.Actions[0].Pos.Offset<=0 {t.Fatal(q,statements)}
	}
	statements,err:=Parse(`ALTER TABLE t SET ACCESS METHOD "Heap 方法", ADD COLUMN added int, SET ACCESS METHOD DEFAULT`)
	if err!=nil {t.Fatal(err)}
	alter:=statements[0].(*AlterTable)
	if len(alter.Actions)!=3 || alter.Actions[0].Method.Text!="Heap 方法" || alter.Actions[1].Action!=AddColumn || !alter.Actions[2].MethodDefault || alter.Actions[2].Method.Text!="" || alter.Actions[0].Pos.Offset>=alter.Actions[2].Pos.Offset {t.Fatal(alter)}
	for _, q := range []string{
		"ALTER MATERIALIZED VIEW t SET ACCESS METHOD heap2, SET (fillfactor=80)",
		"ALTER MATERIALIZED VIEW t SET (fillfactor=80), SET ACCESS METHOD heap2",
		"ALTER MATERIALIZED VIEW t SET ACCESS METHOD heap2, SET TABLESPACE pg_default",
		"ALTER MATERIALIZED VIEW t SET TABLESPACE pg_default, SET ACCESS METHOD heap2",
		"ALTER MATERIALIZED VIEW t SET ACCESS METHOD heap2, SET ACCESS METHOD DEFAULT",
	} {
		statements, err := Parse(q)
		if err != nil { t.Fatal(q, err) }
		alter, ok := statements[0].(*AlterTable)
		if !ok || !alter.Materialized || len(alter.Actions) != 2 { t.Fatal(q, statements) }
	}
	for _, q := range []string{
		"ALTER MATERIALIZED VIEW t SET TABLESPACE pg_default",
		"ALTER MATERIALIZED VIEW t SET (fillfactor=80)",
		"ALTER MATERIALIZED VIEW t RESET (fillfactor)",
		"ALTER MATERIALIZED VIEW t SET ACCESS METHOD heap2, ADD COLUMN a int",
	} {
		if _, err := Parse(q); err == nil { t.Fatal("expanded unrelated materialized grammar", q) }
	}
	for _,q:=range []string{"ALTER TABLE t SET ACCESS METHOD","ALTER TABLE t SET ACCESS METHOD heap2 trailing","ALTER TABLE t SET ACCESS METHOD DEFAULT, SET","ALTER MATERIALIZED VIEW t SET ACCESS METHOD heap2 trailing"} {
		if _,err:=Parse(q);err==nil {t.Fatal("accepted malformed full input",q)}
	}
}
