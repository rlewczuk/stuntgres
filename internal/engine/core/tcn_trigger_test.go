package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTcnTriggerAdmissionUnwind(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION tcn")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE tcn_unit(id int)")
	read := e.CatalogRead()
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	relation := read.MustRelation(read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "tcn_unit"}))
	ext := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tcn"}))
	d := read.MustProcedure(ext.Members[0])
	definition := triggerUnitDefinition(t, read, relation, d, "tcn_before", stored.TriggerInsert)
	relation = triggerUnitInstall(t, e, relation, definition)
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Finish(nil)
	triggerUnitAdmit(t, s, relation)
	integer, _ := read.LookupType(sqltype.Int4)
	row := []sqltype.Value{triggerUnitInt(t, integer, 1)}
	_, err = s.WriteHost().BeforeRow(dml.BeforeRow{Relation: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Event: dml.BeforeInsert, New: row})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "39P01" || diagnostic.Message != "triggered_change_notification: must be called after the change" {
		t.Fatal("native admission/dispatch", err)
	}
	depth, depthErr := s.WriteHost().TriggerDepthValue(integer)
	n, _ := depth.Integer()
	if depthErr != nil || n != 0 || core.ScopeActiveSQLRoutineDepth(s) != 0 {
		t.Fatal("body boundary leaked", n, depthErr)
	}
	if len(core.ScopeNotificationEvents(s)) != 0 {
		t.Fatal("failed body staged event")
	}
	if value, _ := row[0].Integer(); value != 1 {
		t.Fatal("borrowed row mutated")
	}
}
