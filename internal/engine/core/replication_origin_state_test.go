package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func originStateUnitHost(t *testing.T, e *core.Engine) (exec.Host, func(error)) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	scope, err := e.StartStatement(op)
	if err != nil {
		done()
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		done()
		t.Fatal(err)
	}
	closed := false
	finish := func(cause error) {
		if closed {
			return
		}
		closed = true
		if err := scope.Finish(cause); cause == nil && err != nil {
			t.Error(err)
		}
		done()
	}
	t.Cleanup(func() { finish(context.Canceled) })
	return core.HostOf(scope), finish
}
func originStateUnitCall(t *testing.T, e *core.Engine, h exec.Host, kind functions.ReplicationOriginKind, name string, position uint64, flush bool) (sqltype.Value, error) {
	t.Helper()
	read := e.CatalogRead()
	typ := func(id ids.OID) *sqltype.Descriptor {
		return read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}).SQLType
	}
	values := [2]sqltype.Value{sqltype.String(typ(25), name)}
	result := typ(26)
	switch kind {
	case functions.ReplicationOriginDrop:
		result = typ(2278)
	case functions.ReplicationOriginAdvance:
		result = typ(2278)
		var err error
		values[1], err = sqltype.PGLSNValue(typ(3220), position)
		if err != nil {
			t.Fatal(err)
		}
	case functions.ReplicationOriginProgress:
		result = typ(3220)
		values[1] = sqltype.Boolean(typ(16), flush)
	}
	return h.ReplicationOrigin(kind, values, result)
}
func TestReplicationOriginStateRegistration(t *testing.T) {
	e := engine.New()
	h, finish := originStateUnitHost(t, e)
	value, err := originStateUnitCall(t, e, h, functions.ReplicationOriginCreate, "", 0, false)
	if err != nil || value.IsNull() {
		t.Fatal(value, err)
	}
	// Publication must retain the genuine immutable descriptor/index backing.
	finish(nil)
	read := e.CatalogRead()
	address := read.NameAddress(catalog.ReplicationOriginNameKey(""))
	d, ok := read.Get(address).(*catalog.ReplicationOrigin)
	if !ok || address.ObjectID == 0 || address.ObjectID >= 65535 || catalog.ValidateReplicationOrigins(read) != nil {
		t.Fatal(address, d)
	}
	next := catalog.CloneState(read.State())
	next.UnbindName(catalog.DescriptorNameKey(d))
	next.RemoveDescriptor(address)
	if read.Get(address) != d || read.NameAddress(catalog.ReplicationOriginNameKey("")) != address || e.Catalog.Read(next).Get(address) != nil {
		t.Fatal("registration clone mutated retained authority")
	}
	h, finish = originStateUnitHost(t, e)
	_, err = originStateUnitCall(t, e, h, functions.ReplicationOriginCreate, "", 0, false)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Constraint != "pg_replication_origin_roname_index" {
		t.Fatal(err)
	}
	finish(err)
}
func TestReplicationOriginStateProgress(t *testing.T) {
	e := engine.New()
	h, finish := originStateUnitHost(t, e)
	_, err := originStateUnitCall(t, e, h, functions.ReplicationOriginCreate, "state_unit", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	finish(nil)
	for _, position := range []uint64{0x8000000000000000, ^uint64(0), 1, 0} {
		h, finish = originStateUnitHost(t, e)
		value, err := originStateUnitCall(t, e, h, functions.ReplicationOriginAdvance, "state_unit", position, false)
		if err != nil || value.IsNull() || value.Type().ID() != sqltype.Void {
			t.Fatal(value, err)
		}
		finish(context.Canceled)
		h, finish = originStateUnitHost(t, e)
		value, err = originStateUnitCall(t, e, h, functions.ReplicationOriginProgress, "state_unit", 0, false)
		if err != nil || value.IsNull() != (position == 0) {
			t.Fatal(value, err)
		}
		if position != 0 {
			actual, err := value.PGLSN()
			if err != nil || actual != position {
				t.Fatal(actual, err)
			}
		}
		_, err = originStateUnitCall(t, e, h, functions.ReplicationOriginProgress, "state_unit", 0, true)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal(err)
		}
		finish(nil)
	}
	h, finish = originStateUnitHost(t, e)
	_, err = originStateUnitCall(t, e, h, functions.ReplicationOriginAdvance, "state_unit", 99, false)
	if err != nil {
		t.Fatal(err)
	}
	finish(nil)
	h, finish = originStateUnitHost(t, e)
	_, err = originStateUnitCall(t, e, h, functions.ReplicationOriginDrop, "state_unit", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	finish(context.Canceled)
	h, finish = originStateUnitHost(t, e)
	value, err := originStateUnitCall(t, e, h, functions.ReplicationOriginProgress, "state_unit", 0, false)
	if err != nil || !value.IsNull() {
		t.Fatal("drop rollback restored progress", value, err)
	}
	finish(nil)
	// Exercise the initialized startup SettingsStore owner, not a new setting or
	// a forged capability. Identity operations remain usable at capacity zero.
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	op.Session.Settings.Mu.Lock()
	op.Session.Settings.Values["max_active_replication_origins"] = "0"
	op.Session.Settings.Mu.Unlock()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	for _, kind := range []functions.ReplicationOriginKind{functions.ReplicationOriginAdvance, functions.ReplicationOriginProgress} {
		_, err := originStateUnitCall(t, e, core.HostOf(scope), kind, "missing", 0, false)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || diagnostic.Message != `cannot query or manipulate replication origin when "max_active_replication_origins" is 0` {
			t.Fatal(err)
		}
	}
	if _, err := originStateUnitCall(t, e, core.HostOf(scope), functions.ReplicationOriginOID, "state_unit", 0, false); err != nil {
		t.Fatal(err)
	}
}
func TestReplicationOriginStateReplacement(t *testing.T) {
	source, target := engine.New(), engine.New()
	h, finish := originStateUnitHost(t, source)
	if _, err := originStateUnitCall(t, source, h, functions.ReplicationOriginCreate, "state_replacement", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := originStateUnitCall(t, source, h, functions.ReplicationOriginAdvance, "state_replacement", 0x8000000000000001, false); err != nil {
		t.Fatal(err)
	}
	finish(nil)
	retained := source.CatalogRead()
	descriptor := retained.Get(retained.NameAddress(catalog.ReplicationOriginNameKey("state_replacement")))
	if _, err := target.ReplaceCatalog(t.Context(), source, retained); err != nil {
		t.Fatal(err)
	}
	if source.CatalogRead().Get(descriptor.Address()) != descriptor {
		t.Fatal("replacement changed source descriptor")
	}
	h, finish = originStateUnitHost(t, target)
	value, err := originStateUnitCall(t, target, h, functions.ReplicationOriginProgress, "state_replacement", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	position, err := value.PGLSN()
	if err != nil || position != 0x8000000000000001 {
		t.Fatal(position, err)
	}
	finish(nil)
	stage := target.RestoreStage()
	h, finish = originStateUnitHost(t, target)
	if _, err := originStateUnitCall(t, target, h, functions.ReplicationOriginAdvance, "state_replacement", 7, false); err != nil {
		t.Fatal(err)
	}
	finish(nil)
	before := target.CatalogRead().State()
	if _, err := target.ReplaceCatalog(t.Context(), stage, stage.CatalogRead()); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("stale progress replacement admitted", err)
	}
	if target.CatalogRead().State() != before {
		t.Fatal("failed replacement published catalog")
	}
	h, finish = originStateUnitHost(t, target)
	value, err = originStateUnitCall(t, target, h, functions.ReplicationOriginProgress, "state_replacement", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	position, err = value.PGLSN()
	if err != nil || position != 7 {
		t.Fatal(position, err)
	}
	finish(nil)
	for _, clear := range []bool{false, true} {
		stage = target.RestoreStage()
		before = target.CatalogRead().State()
		h, finish = originStateUnitHost(t, target)
		kind := functions.ReplicationOriginAdvance
		if clear {
			kind = functions.ReplicationOriginDrop
		}
		if _, err := originStateUnitCall(t, target, h, kind, "state_replacement", 0, false); err != nil {
			t.Fatal(err)
		}
		finish(context.Canceled)
		if _, err := target.ReplaceCatalog(t.Context(), stage, stage.CatalogRead()); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("zero/clear generation lost", clear, err)
		}
		if target.CatalogRead().State() != before {
			t.Fatal("generation rejection changed registration backing")
		}
	}
	stage = target.RestoreStage()
	h, finish = originStateUnitHost(t, target)
	if value, err := originStateUnitCall(t, target, h, functions.ReplicationOriginProgress, "state_replacement", 0, false); err != nil || !value.IsNull() {
		t.Fatal(value, err)
	}
	if _, err := originStateUnitCall(t, target, h, functions.ReplicationOriginProgress, "state_replacement", 0, true); err == nil {
		t.Fatal("flush promise fabricated")
	}
	finish(nil)
	if _, err := target.ReplaceCatalog(t.Context(), stage, stage.CatalogRead()); err != nil {
		t.Fatal("read/failed flush invalidated replacement", err)
	}
}
