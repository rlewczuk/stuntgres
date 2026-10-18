package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Bind a genuine existing String provider and authorize its real released body.
// Missing/foreign manifests must not be repaired from an equal catalog OID or
// from successfully reconstructing immutable metadata in another engine.
func TestRoutineBodyOriginalLifetimeAndForeignRetag(t *testing.T) {
	e := New()
	admin, finish := roleOperation(t, e, "postgres")
	defer finish()
	roleExecute(t, e, admin, "CREATE ROLE body_lifetime_reader LOGIN")
	reader, readerDone := roleOperation(t, e, "body_lifetime_reader")
	defer readerDone()
	input, _ := sessionPresentation(reader.session)
	e.mu.Lock()
	auth, err := e.authorizationLocked(reader)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	binder := newExpressionBinder(context.Background(), e, input, reader, nil)
	binder.queryAuth = &auth
	binder.queryNamespace = &queryNamespace{}
	node, err := binder.bind(parser.FunctionExpr{Name: preparedBoundaryName("bit_length"), Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "abc"}}}}, expressionPolicy{context: projectionContext})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	original := binder.dependencies
	evaluation := EvalContext{Context: context.Background(), routineAuthority: &auth, routineRecipe: true, routineChecks: original}
	if err := admitRoutineCall(&evaluation, node, true, false); err != nil {
		t.Fatal("genuine original body authority", err)
	}
	var missing []expressionDependency
	for _, dependency := range original {
		if dependency.ref.Address != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1374}) {
			missing = append(missing, dependency)
		}
	}
	evaluation.routineChecks = missing
	if err := admitRoutineCall(&evaluation, node, true, false); err == nil {
		t.Fatal("body acquired lifetime from raw OID after its original ref was removed")
	}
	evaluation.routineChecks = []expressionDependency{}
	if err := admitRoutineCall(&evaluation, node, true, false); err == nil {
		t.Fatal("empty owning recipe borrowed another manifest")
	}

	other := New()
	otherAdmin, otherDone := roleOperation(t, other, "postgres")
	defer otherDone()
	roleExecute(t, other, otherAdmin, "CREATE ROLE body_lifetime_reader LOGIN")
	otherReader, otherReaderDone := roleOperation(t, other, "body_lifetime_reader")
	defer otherReaderDone()
	other.mu.Lock()
	foreign, captureErr := other.authorizationLocked(otherReader)
	other.mu.Unlock()
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	evaluation.routineAuthority = &foreign
	evaluation.routineChecks = original
	if err := admitRoutineCall(&evaluation, node, true, false); err == nil {
		t.Fatal("foreign original body accepted")
	}
	remapped, _, err := retagBoundTypes(other.CatalogRead(), node, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		current, _ := other.CatalogRead().LookupType(old.ID())
		return current
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := admitRoutineCall(&evaluation, remapped, true, false); err == nil {
		t.Fatal("retag manufactured destination lifetime from descriptor addresses")
	}
	evaluation.routineAuthority = &auth
	evaluation.routineChecks = original
	if err := admitRoutineCall(&evaluation, node, true, false); err != nil {
		t.Fatal("negative probes changed source recipe", err)
	}
}

func TestRoutineBodyStoredRecipeReplacement(t *testing.T) {
	ctx := context.Background()
	source := New()
	setup := sessionStart(t, source, nil)
	sessionSQL(t, setup, "CREATE TABLE body_defaults(v integer DEFAULT bit_length('abc'))")
	sessionSQL(t, setup, "CREATE VIEW body_view AS SELECT bit_length('abc') AS v")
	type recipe struct {
		consumer *relationDescriptor
		node     expr.Expr
		refs     []ids.ObjectRef
		checks   []expressionDependency
	}
	capture := func(e *Engine) []recipe {
		view := e.CatalogRead()
		table := view.MustRelation(roleTableRef(t, e, "body_defaults").Address)
		stored := view.MustRelation(roleTableRef(t, e, "body_view").Address)
		def := table.Defaults[table.Attributes[0]]
		return []recipe{
			{consumer: table, node: def.expression, refs: def.dependencies},
			{consumer: stored, node: viewOf(stored).query.Root.(*projectNode).expressions[0], checks: viewOf(stored).dependencies},
		}
	}
	authority := func(e *Engine) AuthorizationView {
		op, done := roleOperation(t, e, "postgres")
		defer done()
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		return auth
	}
	before := source.CatalogRead()
	original := capture(source)
	sourceAuth := authority(source)
	// Save actual source manifests and declaration provenance before remapping;
	// no reconstructed/grafted expected recipe substitutes for the stored one.
	originalRefs := append([]ids.ObjectRef(nil), original[0].refs...)
	originalChecks := append([]expressionDependency(nil), original[1].checks...)
	sourceCall, err := expressionRoutineDescriptor(original[0].node)
	if err != nil || sourceCall == nil {
		t.Fatal("missing original DEFAULT routine carrier", err)
	}
	sourceOwner, sourceRevision := sourceCall.Owner, sourceCall.revision
	target := New()
	retired := target.CatalogRead()
	retiredRef := target.catalog.domain.Ref(retired.Epoch(), sourceCall.address)
	change, err := target.ReplaceCatalog(ctx, source, before)
	if err != nil || !change.Replacement {
		t.Fatal("replace genuine stored catalog", change, err)
	}
	current := target.CatalogRead()
	if current.Epoch() != retired.Epoch()+1 {
		t.Fatal("replacement did not advance destination epoch")
	}
	if err := target.ValidateObjectRef(current, retiredRef); err == nil {
		t.Fatal("retired destination routine ref survived replacement")
	}
	targetAuth := authority(target)
	imported := capture(target)
	for i, item := range imported {
		if item.consumer == original[i].consumer || item.consumer.Owner != target.authorizationDomain.Principal(item.consumer.Owner.ID()) {
			t.Fatal("stored consumer retained source ownership", i)
		}
		call, err := expressionRoutineDescriptor(item.node)
		if err != nil || call == nil || call.address != sourceCall.address || call == sourceCall ||
			call.Owner != target.authorizationDomain.Principal(call.Owner.ID()) || call.revision == 0 {
			t.Fatal("stored call lost destination declaration provenance", i, err)
		}
		evaluation := EvalContext{Context: ctx, routineAuthority: &targetAuth, routineRecipe: true,
			routineConsumer: item.consumer, routineReferences: item.refs, routineChecks: item.checks}
		if err := admitRoutineCall(&evaluation, item.node, true, false); err != nil {
			t.Fatal("remapped original recipe admission", i, err)
		}
		// The released TEXT wrapper calls octet_length(text), then int4mul.
		// Each original body ref must independently remain necessary; equal
		// builtin addresses in either engine cannot repair a missing manifest.
		for _, id := range []ids.OID{1811, 1374, 141} {
			address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
			probe := evaluation
			probe.routineReferences = []ids.ObjectRef{}
			probe.routineChecks = nil
			var selected ids.ObjectRef
			if i == 0 {
				for _, ref := range item.refs {
					if ref.Domain() != target.catalog.domain || ref.Epoch != current.Epoch() {
						t.Fatal("DEFAULT manifest retained source/old-epoch ref", ref)
					}
					if ref.Address == address {
						selected = ref
					} else {
						probe.routineReferences = append(probe.routineReferences, ref)
					}
				}
			} else {
				probe.routineReferences = nil
				probe.routineChecks = []expressionDependency{}
				for _, dependency := range item.checks {
					ref := dependency.ref
					if ref.Domain() != target.catalog.domain || ref.Epoch != current.Epoch() {
						t.Fatal("view manifest retained source/old-epoch ref", ref)
					}
					if ref.Address == address {
						selected = ref
						d, ok := current.Descriptor(address)
						if !ok || d.Revision() != dependency.revision {
							t.Fatal("view manifest lost original declaration revision", address)
						}
					} else {
						probe.routineChecks = append(probe.routineChecks, dependency)
					}
				}
			}
			if selected.Domain() == nil {
				t.Fatal("stored original manifest omitted released body identity", i, id)
			}
			if err := target.ValidateObjectRef(current, selected); err != nil {
				t.Fatal("remapped original ref invalid", err)
			}
			if err := admitRoutineCall(&probe, item.node, true, false); err == nil {
				t.Fatal("missing original body ref was rescued", i, id)
			}
		}
		foreign := evaluation
		foreign.routineAuthority = &sourceAuth
		if err := admitRoutineCall(&foreign, item.node, true, false); err == nil {
			t.Fatal("destination recipe borrowed source authority", i)
		}
		old := evaluation
		old.routineReferences, old.routineChecks = original[i].refs, original[i].checks
		if err := admitRoutineCall(&old, item.node, true, false); err == nil {
			t.Fatal("destination recipe borrowed same-address source manifest", i)
		}
		if i == 1 {
			stale := evaluation
			stale.routineChecks = append([]expressionDependency(nil), item.checks...)
			for j := range stale.routineChecks {
				if stale.routineChecks[j].ref.Address == call.address {
					stale.routineChecks[j].revision++
				}
			}
			if err := admitRoutineCall(&stale, item.node, true, false); err == nil {
				t.Fatal("view accepted mismatched original routine revision")
			}
		}
	}
	after := capture(source)
	if source.CatalogRead().state != before.state || sourceCall.Owner != sourceOwner || sourceCall.revision != sourceRevision ||
		!reflect.DeepEqual(after[0].refs, originalRefs) || !reflect.DeepEqual(after[1].checks, originalChecks) {
		t.Fatal("replacement or negative probes mutated source provenance")
	}
	for i, item := range after {
		if item.consumer != original[i].consumer || !reflect.DeepEqual(item.node, original[i].node) {
			t.Fatal("replacement mutated original stored recipe", i)
		}
		evaluation := EvalContext{Context: ctx, routineAuthority: &sourceAuth, routineRecipe: true,
			routineConsumer: item.consumer, routineReferences: item.refs, routineChecks: item.checks}
		if err := admitRoutineCall(&evaluation, item.node, true, false); err != nil {
			t.Fatal("source original recipe lost admission", i, err)
		}
	}
}

// Every supported released SQL root is bound through its actual native
// concrete provider. Then remove each selected body lifetime independently:
// an equal released address cannot stand in for that missing original ref.
// This is an admission/provenance unit, not a replacement for public value/ACL
// oracle witnesses, nor an invocation of sleep or a fabricated body evaluator.
func TestRoutineBodyEveryProviderOriginalLifetime(t *testing.T) {
	e := New()
	admin, finish := roleOperation(t, e, "postgres")
	defer finish()
	roleExecute(t, e, admin, "CREATE ROLE body_each_reader LOGIN")
	roleExecute(t, e, admin, "CREATE TABLE body_inputs(t text,i integer,n numeric,b bytea,bs bit(3),d date,tm time,tz timetz,ts timestamp,zt timestamptz,iv interval,id oid,nm name,n8 bigint,ip inet,flag boolean)")
	reader, readerDone := roleOperation(t, e, "body_each_reader")
	defer readerDone()
	ref := roleTableRef(t, e, "public.body_inputs")
	input, _ := sessionPresentation(reader.session)
	// Expected callees are independently pinned to REL_18_0 declarations and
	// SQL bodies, not obtained by walking the implementation's closure table.
	cases := []struct {
		id         ids.OID
		expression string
		calls      []ids.OID
	}{
		{879, "lpad(t,i)", []ids.OID{873}}, {880, "rpad(t,i)", []ids.OID{874}},
		{1811, "bit_length(t)", []ids.OID{1374, 141}}, {1810, "bit_length(b)", []ids.OID{720, 141}}, {1812, "bit_length(bs)", []ids.OID{1681}},
		{1708, "round(n)", []ids.OID{1707}}, {1710, "trunc(n)", []ids.OID{1709}}, {1384, "date_part('year',d)", []ids.OID{2024, 2021}}, {2059, "age(ts)", []ids.OID{2024, 2058}},
		{1741, "log(n)", []ids.OID{1736}}, {1481, "log10(n)", []ids.OID{1736}},
		{1296, "tm+d", []ids.OID{1272}}, {1298, "tz+d", []ids.OID{1297}}, {1848, "iv+tm", []ids.OID{1747}}, {2546, "iv+d", []ids.OID{2071}}, {2547, "iv+tz", []ids.OID{1749}},
		{2548, "iv+ts", []ids.OID{2032}}, {2549, "iv+zt", []ids.OID{1189}}, {2550, "i+d", []ids.OID{1141}},
		{2074, `substring(t,'#"b#"','#')`, []ids.OID{1986, 2073}},
		{1285, "quote_literal(i)", []ids.OID{1283}}, {1290, "quote_nullable(i)", []ids.OID{1289}}, {2003, "t||i", []ids.OID{1258}}, {2004, "i||t", []ids.OID{1258}},
		{1285, "quote_literal(flag)", []ids.OID{2971, 1283}}, {1285, "quote_literal(ip)", []ids.OID{730, 1283}},
		{2631, "n8+ip", []ids.OID{2630}},
		{3935, "pg_sleep_for(iv)", []ids.OID{2649, 1189, 6203, 1725, 1746, 2626}}, {3936, "pg_sleep_until(zt)", []ids.OID{2649, 6203, 1725, 1746, 2626}},
		{1305, "(zt,iv) OVERLAPS (zt,iv)", []ids.OID{1189, 1304}}, {1306, "(zt,zt) OVERLAPS (zt,iv)", []ids.OID{1189, 1304}}, {1307, "(zt,iv) OVERLAPS (zt,zt)", []ids.OID{1189, 1304}},
		{2042, "(ts,iv) OVERLAPS (ts,iv)", []ids.OID{2032, 2041}}, {2043, "(ts,ts) OVERLAPS (ts,iv)", []ids.OID{2032, 2041}}, {2044, "(ts,iv) OVERLAPS (ts,ts)", []ids.OID{2032, 2041}},
		{1309, "(tm,iv) OVERLAPS (tm,iv)", []ids.OID{1747, 1308}}, {1310, "(tm,tm) OVERLAPS (tm,iv)", []ids.OID{1747, 1308}}, {1311, "(tm,iv) OVERLAPS (tm,tm)", []ids.OID{1747, 1308}},
		{1215, "obj_description(id,nm)", []ids.OID{184, 62, 65}}, {1993, "shobj_description(id,nm)", []ids.OID{184, 62}}, {1348, "obj_description(id)", []ids.OID{184, 65}}, {1216, "col_description(id,i)", []ids.OID{184, 65}},
	}
	for _, test := range cases {
		t.Run(test.expression, func(t *testing.T) {
			raw := statement(t, "SELECT "+test.expression).(*parser.Select).Targets[0]
			e.mu.Lock()
			auth, err := e.authorizationLocked(reader)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			binder := newExpressionBinder(context.Background(), e, input, reader, nil)
			binder.queryAuth, binder.ref = &auth, ref
			binder.relation = auth.catalog.MustRelation(ref.Address)
			node, err := binder.bind(raw, expressionPolicy{context: projectionContext})
			e.mu.Unlock()
			if err != nil {
				t.Fatal("actual provider selection", err)
			}
			outer, err := expressionRoutineDescriptor(node)
			if err != nil || outer == nil || outer.address.ObjectID != test.id {
				t.Fatal("selected released root", outer, err)
			}
			evaluation := EvalContext{Context: context.Background(), routineAuthority: &auth, routineRecipe: true, routineChecks: binder.dependencies}
			if err := admitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("original body admission", err)
			}
			for _, id := range test.calls {
				address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
				var missing []expressionDependency
				for _, dependency := range binder.dependencies {
					if dependency.ref.Address != address {
						missing = append(missing, dependency)
					}
				}
				evaluation.routineChecks = missing
				if err := admitRoutineCall(&evaluation, node, true, false); err == nil {
					t.Fatal("selected body lifetime was manufactured or omitted", id)
				}
			}
		})
	}
}
