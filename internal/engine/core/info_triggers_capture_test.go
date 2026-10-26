package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
)

// Only the deterministic cancellation boundary is controlled; all definitions,
// authority validation, rendering and produced rows use the genuine runtime.
type triggerCancelRuntime struct {
	core.EngineCatalogRuntime
	cancel context.CancelFunc
}

func (r triggerCancelRuntime) VisitTriggerDefinitions(ctx context.Context, read catalog.Read, relation *catalog.Relation, disclose bool, visit func(pgcatalog.TriggerFact) error) error {
	return r.EngineCatalogRuntime.VisitTriggerDefinitions(ctx, read, relation, disclose, func(f pgcatalog.TriggerFact) error {
		if err := visit(f); err != nil {
			return err
		}
		r.cancel()
		return nil
	})
}
func infoTriggersCaptureFailures(t *testing.T) {
	t.Helper()
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, function, "actual", stored.TriggerUpdate)
	definition = infoTriggersWhen(t, e, scope, read, relation, definition)
	relation = triggerUnitInstall(t, e, relation, definition)
	in := infoTriggersInput(t, e, scope)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTriggersOID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	interrupted := in
	interrupted.Context = ctx
	interrupted.Runtime = triggerCancelRuntime{EngineCatalogRuntime: in.Runtime.(core.EngineCatalogRuntime), cancel: cancel}
	if rows, err := produce(interrupted); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("candidate cancellation exposed prefix", rows, err)
	}
	for _, remaining := range []int{1, 2, 3} {
		rendererContext := &dtpCancelContext{Context: context.Background(), remaining: remaining}
		var facts []stored.TriggerPresentation
		err := stored.VisitTriggerDefinitions(rendererContext, in.Catalog, relation, stored.Presentation{}, true, func(f stored.TriggerPresentation) error { facts = append(facts, f); return nil })
		if !errors.Is(err, context.Canceled) || facts != nil {
			t.Fatal("render cancellation", facts, err)
		}
	}
	for _, epoch := range []uint64{0, uint64(in.Ref.Epoch) + 1} {
		bad := in
		bad.Ref = bad.Ref.Domain().Ref(in.Ref.Epoch, bad.Ref.Address)
		// ObjectRef epoch type is retained by assignment from the captured reference.
		if epoch == 0 {
			bad.Ref.Epoch = 0
		} else {
			bad.Ref.Epoch++
		}
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid epoch", rows, err)
		}
	}
	state := catalog.CloneState(in.Catalog.State())
	broken := *function
	broken.SQL = nil
	state.SetDescriptor(broken.Address(), &broken)
	bad := in
	bad.Catalog = e.Catalog.Read(state)
	runtime := in.Runtime.(core.EngineCatalogRuntime)
	runtime.Auth.Recapture(bad.Catalog)
	bad.Roles = runtime.Auth.Roles
	bad.Runtime = runtime
	if rows, err := produce(bad); rows != nil || err == nil {
		t.Fatal("malformed current routine", rows, err)
	}
	p := definition.Parts()
	p.UpdateColumns = []int32{99}
	invalid, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	copy := *relation
	copy.Triggers = stored.TriggersPayload([]stored.Trigger{invalid})
	state = catalog.CloneState(in.Catalog.State())
	state.SetDescriptor(copy.Address(), &copy)
	bad = in
	bad.Catalog = e.Catalog.Read(state)
	runtime = in.Runtime.(core.EngineCatalogRuntime)
	runtime.Auth.Recapture(bad.Catalog)
	bad.Roles = runtime.Auth.Roles
	bad.Runtime = runtime
	if rows, err := produce(bad); rows != nil || err == nil {
		t.Fatal("malformed current attribute", rows, err)
	}
}
