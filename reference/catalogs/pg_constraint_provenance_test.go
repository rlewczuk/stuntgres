package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintProvenance(t *testing.T) {
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pc_prov_root(k INT CONSTRAINT pc_prov_nn NOT NULL,CONSTRAINT pc_prov_ck CHECK(k>0),CONSTRAINT pc_prov_u UNIQUE(k)) PARTITION BY RANGE(k)", "CREATE TABLE pc_prov_leaf PARTITION OF pc_prov_root(k CONSTRAINT pc_prov_nn NOT NULL) DEFAULT")
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT contype,conparentid=0::oid AS no_parent,conindid=0::oid AS no_index,conislocal,coninhcount,connoinherit,conkey::text FROM pg_constraint WHERE conrelid='pc_prov_root'::regclass::oid ORDER BY contype", []string{"contype", "no_parent", "no_index", "conislocal", "coninhcount", "connoinherit", "conkey"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "INT2", "BOOL", "TEXT"}, [][]any{{"c", true, true, true, int64(0), false, "{1}"}, {"n", true, true, true, int64(0), false, "{1}"}, {"u", true, false, true, int64(0), true, "{1}"}}))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT contype,conparentid=0::oid AS no_parent,conindid=0::oid AS no_index,conislocal,coninhcount,connoinherit,conkey::text FROM pg_constraint WHERE conrelid='pc_prov_leaf'::regclass::oid ORDER BY contype", []string{"contype", "no_parent", "no_index", "conislocal", "coninhcount", "connoinherit", "conkey"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "INT2", "BOOL", "TEXT"}, [][]any{{"c", true, true, false, int64(1), false, "{1}"}, {"n", true, true, true, int64(1), false, "{1}"}, {"u", false, false, false, int64(1), false, "{1}"}}))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT c.conparentid=p.oid AS parent,c.oid<>p.oid AS distinct_identity,c.conindid<>p.conindid AS distinct_index FROM pg_constraint c JOIN pg_constraint p ON c.conparentid=p.oid WHERE c.conrelid='pc_prov_leaf'::regclass::oid", []string{"parent", "distinct_identity", "distinct_index"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		pgConstraintCheck(t, p, b, b.DB, fixture.Select("SELECT relchecks FROM pg_class WHERE oid IN ('pc_prov_root'::regclass::oid,'pc_prov_leaf'::regclass::oid) ORDER BY relname", []string{"relchecks"}, []string{"INT2"}, [][]any{{int64(1)}, {int64(1)}}))
	})
}
