package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestSerialCoherentCreateCandidate(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	before := e.Catalog.State
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, `CREATE TABLE bad(id SERIAL,v INT DEFAULT missing_column)`), nil)
	enginetest.State(t, err, "0A000")
	if e.Catalog.State != before || len(e.Storage.Sequences) != 0 {
		t.Fatal("default cooking failure leaked serial candidate")
	}
	enginetest.SessionSQL(t, s, `CREATE TABLE t(id SERIAL,CHECK(nextval('t_id_seq')>0))`)
	sequence := enginetest.RoleTableRef(t, e, "t_id_seq").Address
	if e.Storage.Sequences[sequence].Called {
		t.Fatal("CHECK/default binding evaluated candidate")
	}
	enginetest.SessionSQL(t, s, `INSERT INTO t DEFAULT VALUES`)
	sequenceScalar(t, s, `SELECT id FROM t`, 1)
	sequenceScalar(t, s, `SELECT currval('t_id_seq')`, 2)
	enginetest.SessionSQL(t, s, `DROP TABLE t RESTRICT`)
	if e.Catalog.State.Get(sequence) != nil {
		t.Fatal("AUTO/NORMAL/CHECK cycle blocked owned cleanup")
	}
}
