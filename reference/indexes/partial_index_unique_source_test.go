package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartialIndexUniqueSourceSets(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0))
		run(fixture.Exec("CREATE TABLE source(id INT,k INT,active BOOL)", 0))
		run(fixture.Exec("INSERT INTO source VALUES(1,7,FALSE),(2,7,NULL),(3,7,TRUE)", 3))
		run(fixture.Prepared(fixture.Exec("INSERT INTO u SELECT id,k,active FROM source ORDER BY id", 3)))
		run(fixture.Exec("INSERT INTO u SELECT id+10,k,active FROM u WHERE active IS NOT TRUE ORDER BY id", 2))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 11, 12))
		dup := fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO u SELECT id+20,k,active FROM u ORDER BY id", dup)))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 11, 12))
		run(fixture.Exec("INSERT INTO u SELECT 20,8,TRUE UNION SELECT 20,8,TRUE", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO u SELECT 21,9,TRUE UNION ALL SELECT 22,9,TRUE", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(9) already exists.")))
		run(fixture.Exec("INSERT INTO u SELECT 21,9,FALSE UNION ALL SELECT 22,9,FALSE", 2))
		run(fixture.Exec("CREATE TABLE repeated(id INT,k INT,active BOOL)", 0))
		run(fixture.Exec("INSERT INTO repeated VALUES(23,10,TRUE),(23,10,TRUE)", 2))
		run(fixture.Prepared(fixture.Exec("INSERT INTO u SELECT DISTINCT id,k,active FROM repeated", 1)))
		run(fixture.PartialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 3, 20, 23))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 11, 12, 20, 21, 22, 23))
	})
}

func TestReferencePartialIndexUniqueSourcePrecedence(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SEQUENCE draws", 0))
		run(fixture.Exec("CREATE TABLE u(id INT,k INT,active BOOL,draw BIGINT DEFAULT nextval('draws'))", 0))
		run(fixture.Exec("INSERT INTO u VALUES(1,7,TRUE,0)", 1))
		run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0))
		run(fixture.Exec("CREATE TABLE source(id INT,k INT,n INT,active BOOL)", 0))
		run(fixture.Exec("INSERT INTO source VALUES(2,7,1,FALSE),(3,7,1,TRUE),(4,8,0,TRUE)", 3))
		q := "INSERT INTO u(id,k,active) SELECT id,k+0/n,active FROM source ORDER BY id"
		run(fixture.Prepared(fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})))
		run(fixture.Select("SELECT last_value,is_called FROM draws", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1))
		run(fixture.Exec("CREATE SEQUENCE source_draws", 0))
		volatile := "INSERT INTO u(id,k,active) SELECT id,k+0/n+nextval('source_draws')::INT*0,active FROM source ORDER BY id"
		run(fixture.Prepared(fixture.ErrorDiagnostic(volatile, fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists."))))
		run(fixture.Select("SELECT last_value,is_called FROM source_draws", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		run(fixture.Select("SELECT last_value,is_called FROM draws", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1))
		run(fixture.Exec("UPDATE source SET active=FALSE WHERE id=3 OR id=4", 2))
		run(fixture.Exec("ALTER SEQUENCE draws RESTART WITH 1", 0))
		run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		run(fixture.Select("SELECT last_value,is_called FROM draws", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		run(fixture.Select("SELECT id,k,active,draw FROM u", []string{"id", "k", "active", "draw"}, []string{"INT4", "INT4", "BOOL", "INT8"}, [][]any{{int64(1), int64(7), true, int64(0)}}))
		run(fixture.Exec("UPDATE source SET n=1 WHERE id=4", 1))
		run(fixture.Exec(q, 3))
		run(fixture.Select("SELECT id,k,active,draw FROM u ORDER BY id", []string{"id", "k", "active", "draw"}, []string{"INT4", "INT4", "BOOL", "INT8"}, [][]any{{int64(1), int64(7), true, int64(0)}, {int64(2), int64(7), false, int64(1)}, {int64(3), int64(7), false, int64(2)}, {int64(4), int64(8), false, int64(3)}}))
	})
}
