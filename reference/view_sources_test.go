//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceViewSourcesAndCatalogLinks(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("SET SESSION AUTHORIZATION postgres", 0),
		referenceExec("CREATE TABLE t(id INT,txt TEXT,visible BOOL)", 0),
		referenceExec("INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)", 4),
		referencePrepared(referenceExec("CREATE VIEW grouped(k,n,label) AS SELECT id,COUNT(*),COALESCE(txt,'x') FROM t WHERE visible GROUP BY id,txt HAVING COUNT(*)>1", 0)),
		referenceSelect("SELECT k,n,label FROM grouped", []string{"k", "n", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(3), int64(2), "x"}}),
		referenceExec("CREATE TABLE destination(k INT,n BIGINT,label TEXT)", 0),
		referencePrepared(referenceExec("INSERT INTO destination SELECT k,n,label FROM grouped", 1)),
		referenceSelect("SELECT k,n,label FROM destination", []string{"k", "n", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(3), int64(2), "x"}}),
		referenceExec("CREATE VIEW z AS SELECT FROM t", 0),
		referencePrepared(referenceSelect("SELECT FROM z", []string{}, []string{}, [][]any{{}, {}, {}, {}})),
		referenceExec("CREATE SEQUENCE s", 0), referenceExec("CREATE VIEW sequence_state AS SELECT last_value,is_called FROM s", 0),
		referencePrepared(referenceExec("CREATE VIEW sequence_call(n) AS SELECT nextval('s') FROM t", 0)),
		referenceSelect("SELECT last_value,is_called FROM sequence_state", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
		referenceSelect("SELECT n FROM sequence_call ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}),
		referenceExec("CREATE VIEW provider_view AS SELECT schemaname,viewname FROM pg_catalog.pg_views WHERE viewname='grouped'", 0),
		referenceSelect("SELECT schemaname,viewname FROM provider_view", []string{"schemaname", "viewname"}, []string{"NAME", "NAME"}, [][]any{{"public", "grouped"}}),
		referenceSelect("SELECT relkind,relnatts,relhasrules FROM pg_catalog.pg_class WHERE relname='grouped'", []string{"relkind", "relnatts", "relhasrules"}, []string{"CHAR", "INT2", "BOOL"}, [][]any{{"v", int64(3), true}}),
		referenceSelect("SELECT viewowner FROM pg_catalog.pg_views WHERE viewname='grouped'", []string{"viewowner"}, []string{"NAME"}, [][]any{{"postgres"}}),
	} {
		p.step(t, step)
	}
	for _, backend := range []struct {
		db       *sql.DB
		recorder *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		var oid, namespace, owner int64
		if err := backend.db.QueryRowContext(p.ctx, "SELECT oid,relnamespace,relowner FROM pg_catalog.pg_class WHERE relname='grouped'").Scan(&oid, &namespace, &owner); err != nil {
			t.Fatal(err)
		}
		if oid <= 0 || namespace <= 0 || owner <= 0 {
			t.Fatal("invalid live catalog identity", oid, namespace, owner)
		}
		step := referencePrepared(referenceSelect("SELECT rulename,ev_type,ev_enabled,is_instead FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "CHAR", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", "O", true}}, oid))
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
	}
}

func TestReferenceViewSequenceCatalogPrivateVisibility(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE SEQUENCE s", 0),
		referenceExec("CREATE VIEW sequence_view AS SELECT last_value FROM s", 0),
		referenceExec("BEGIN", 0),
		referenceExec("DROP SEQUENCE s CASCADE", 0),
		referenceSelect("SELECT viewname FROM pg_catalog.pg_views WHERE viewname='sequence_view'", []string{"viewname"}, []string{"NAME"}, nil),
		referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='sequence_view'", []string{"relname"}, []string{"NAME"}, nil),
		referenceExec("ROLLBACK", 0),
		referenceSelect("SELECT viewname FROM pg_catalog.pg_views WHERE viewname='sequence_view'", []string{"viewname"}, []string{"NAME"}, [][]any{{"sequence_view"}}),
	} {
		p.step(t, step)
	}
}
