package engine

import (
	"context"
	"testing"
)

func TestSerialCoherentCreateCandidate(t *testing.T) {
	e := New()
	s := sessionStart(t,e,nil)
	before := e.catalog.State
	_,err := testExecute(e,context.Background(),statement(t,`CREATE TABLE bad(id SERIAL,v INT DEFAULT missing_column)`),nil)
	state(t,err,"0A000")
	if e.catalog.State != before || len(e.storage.Sequences) != 0 { t.Fatal("default cooking failure leaked serial candidate") }
	sessionSQL(t,s,`CREATE TABLE t(id SERIAL,CHECK(nextval('t_id_seq')>0))`)
	sequence := roleTableRef(t,e,"t_id_seq").Address
	if e.storage.Sequences[sequence].Called { t.Fatal("CHECK/default binding evaluated candidate") }
	sessionSQL(t,s,`INSERT INTO t DEFAULT VALUES`)
	sequenceScalar(t,s,`SELECT id FROM t`,1)
	sequenceScalar(t,s,`SELECT currval('t_id_seq')`,2)
	sessionSQL(t,s,`DROP TABLE t RESTRICT`)
	if e.catalog.State.Get(sequence) != nil { t.Fatal("AUTO/NORMAL/CHECK cycle blocked owned cleanup") }
}
