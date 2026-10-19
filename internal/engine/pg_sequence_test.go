package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgSequenceUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2224}
	d, ok := e.catalog.state.Relation(a)
	if !ok {
		t.Fatal("missing pg_sequence")
	}
	return d, e.catalog.Domain.Ref(e.catalog.state.Epoch(), a)
}
func pgSequenceUnitBag(t *testing.T, source *querySource) map[uint32][]any {
	t.Helper()
	bag := make(map[uint32][]any)
	for _, row := range source.anonymous {
		if len(row) != 8 {
			t.Fatal("row width", row)
		}
		values := make([]any, 8)
		for i := range 2 {
			n, err := row[i].OID()
			if err != nil {
				t.Fatal(err)
			}
			values[i] = n
		}
		for i := 2; i < 7; i++ {
			n, err := row[i].Integer()
			if err != nil {
				t.Fatal(err)
			}
			values[i] = n
		}
		b, err := row[7].Boolean()
		if err != nil {
			t.Fatal(err)
		}
		values[7] = b
		id := values[0].(uint32)
		if _, exists := bag[id]; exists {
			t.Fatal("duplicate definition", id)
		}
		bag[id] = values
	}
	return bag
}
func TestPGSequenceShape(t *testing.T) {
	e := New()
	d, _ := pgSequenceUnitSource(t, e)
	if d.Name() != "pg_sequence" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || d.AttributeHighWater != 8 || len(d.Columns) != 8 {
		t.Fatal(d)
	}
	for i, tc := range []struct {
		name string
		typ  sqltype.ID
	}{
		{"seqrelid", sqltype.Oid}, {"seqtypid", sqltype.Oid}, {"seqstart", sqltype.Int8}, {"seqincrement", sqltype.Int8}, {"seqmax", sqltype.Int8}, {"seqmin", sqltype.Int8}, {"seqcache", sqltype.Int8}, {"seqcycle", sqltype.Bool},
	} {
		c := d.Columns[i]
		if c.Name != tc.name || c.Usage.Type.ID() != tc.typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2224 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[tc.name] != i {
			t.Fatal("descriptor", i, c)
		}
	}
	for _, name := range []string{"oid", "last_value", "is_called"} {
		if _, exists := d.Index[name]; exists {
			t.Fatal("invented state column", name)
		}
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	physical := *d
	physical.RelationKind = catalog.TableRelation
	if err := (relationProvider{}).Validate(e.CatalogRead(), &physical); err == nil {
		t.Fatal("physical NOT NULL requires constraint identities")
	}
	foreignType, _ := New().CatalogRead().LookupType(sqltype.Oid)
	for _, tc := range []struct {
		name       string
		invalidate func(*catalog.Relation)
	}{
		{"identity", func(r *catalog.Relation) { r.ObjectAddress.ObjectID = 0 }},
		{"type", func(r *catalog.Relation) { r.Columns[0].Usage.Type = foreignType }},
		{"live attribute", func(r *catalog.Relation) { r.Attributes[0] = 0 }},
		{"constraint record", func(r *catalog.Relation) { r.NotNullConstraints = []catalog.NotNullConstraint{{Attribute: 1}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := *d
			invalid.Columns = append([]catalog.Column(nil), d.Columns...)
			invalid.Attributes = append([]int32(nil), d.Attributes...)
			tc.invalidate(&invalid)
			if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
				t.Fatal("virtual-source exemption bypassed validation")
			}
		})
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.Security.ACL)
	}
	class := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roleRead(e.catalog.read(e.catalog.state), e.roles), ids.ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows.anonymous {
		id, _ := row[0].OID()
		if id != 2224 {
			continue
		}
		name, _ := row[1].Text()
		ns, _ := row[2].OID()
		kind, _ := row[class.Index["relkind"]].Text()
		count, _ := row[class.Index["relnatts"]].Integer()
		if name != "pg_sequence" || ns != 11 || kind != "r" || count != 8 {
			t.Fatal(row)
		}
		found = true
	}
	if !found {
		t.Fatal("pg_class identity absent")
	}
}
func TestPGSequenceProjection(t *testing.T) {
	e := New()
	source, ref := pgSequenceUnitSource(t, e)
	state := *e.catalog.state
	state.CloneDescriptors()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	options := &catalog.SequenceOptions{SQLType: sqltype.Int4, Start: 15, Increment: 7, Max: 200, Min: -20, Cache: 9223372036854775807, Cycle: true}
	d := &catalog.Relation{ObjectAddress: a, RelationKind: catalog.SequenceRelation, Sequence: options}
	state.SetDescriptor(a, d)
	state.SetDescriptor(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, &catalog.Relation{RelationKind: catalog.TableRelation})
	state.SetDescriptor(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}, &catalog.Relation{RelationKind: catalog.ViewRelation})
	e.storage.sequences[a] = &sequenceGenerator{last: 99, called: true, generation: 100}
	rows, err := capturePGSequence(context.Background(), &state, nil, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32][]any{20000: {uint32(20000), uint32(23), int64(15), int64(7), int64(200), int64(-20), int64(9223372036854775807), true}}
	if !reflect.DeepEqual(pgSequenceUnitBag(t, rows), want) {
		t.Fatal(pgSequenceUnitBag(t, rows), want)
	}
	if options.Cache != 9223372036854775807 || e.storage.sequences[a].last != 99 || !e.storage.sequences[a].called {
		t.Fatal("projection touched definition or state")
	}
}
func TestPGSequenceEmptySource(t *testing.T) {
	e := New()
	source, ref := pgSequenceUnitSource(t, e)
	base := *e.catalog.state
	base.CloneDescriptors()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	base.SetDescriptor(a, &catalog.Relation{ObjectAddress: a, RelationKind: catalog.SequenceRelation, Sequence: &catalog.SequenceOptions{SQLType: sqltype.Int8, Start: 1, Increment: 1, Max: 9223372036854775807, Min: 1, Cache: 1}})
	own := newCatalogChanges()
	own.descriptors[a] = nil
	for _, tc := range []struct {
		name string
		base *catalog.State
		own  *catalogChanges
	}{
		{"no definitions", e.catalog.state, nil},
		{"private drop", &base, own},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := capturePGSequence(context.Background(), tc.base, tc.own, ref, source)
			if err != nil {
				t.Fatal(err)
			}
			if rows.anonymous == nil || len(rows.anonymous) != 0 {
				t.Fatal("empty catalog must remain an anonymous source", rows)
			}
		})
	}
}
func TestPGSequenceSnapshotsAndCancellation(t *testing.T) {
	e := New()
	source, ref := pgSequenceUnitSource(t, e)
	base := *e.catalog.state
	base.CloneDescriptors()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	dropped := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
	added := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	options := &catalog.SequenceOptions{SQLType: sqltype.Int2, Start: -1, Increment: -3, Max: -1, Min: -32768, Cache: 1}
	d := &catalog.Relation{ObjectAddress: a, RelationKind: catalog.SequenceRelation, Sequence: options}
	base.SetDescriptor(a, d)
	other := *d
	other.ObjectAddress = dropped
	base.SetDescriptor(dropped, &other)
	old, err := capturePGSequence(context.Background(), &base, nil, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	oldBag := pgSequenceUnitBag(t, old)
	own := newCatalogChanges()
	updated := *d
	newOptions := *options
	newOptions.Start = -4
	updated.Sequence = &newOptions
	own.descriptors[a] = &updated
	own.descriptors[dropped] = nil
	addition := *d
	addition.ObjectAddress = added
	own.descriptors[added] = &addition
	want := map[uint32][]any{20000: {uint32(20000), uint32(21), int64(-4), int64(-3), int64(-1), int64(-32768), int64(1), false}, 20002: {uint32(20002), uint32(21), int64(-1), int64(-3), int64(-1), int64(-32768), int64(1), false}}
	// Latest publication has none of the retained base's sequence definitions.
	e.catalog.state = catalog.CloneState(e.catalog.state)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	owner := op.session.transactions
	owner.sequenceCatalog = &base
	owner.catalog = own
	scope := &StatementScope{op: op, owner: owner}
	e.mu.Lock()
	captured, err := e.openRelationSourceLocked(scope, ref, e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), queryRowMark{}, nil, nil)
	if err != nil || !reflect.DeepEqual(pgSequenceUnitBag(t, captured), want) {
		t.Fatal("retained base plus own overlay", err)
	}
	if owner.accessedTemporary || !reflect.DeepEqual(pgSequenceUnitBag(t, old), oldBag) || d.Sequence.Start != -1 {
		t.Fatal("capture mutated retained rows or physical TEMP authority")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, state := range []*catalog.State{&base, catalog.NewState(0)} {
		rows, err := capturePGSequence(ctx, state, own, ref, source)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled source", rows, err)
		}
	}
}
func TestPGSequenceSnapshotOwnerLifecycle(t *testing.T) {
	for _, isolation := range []Isolation{ReadCommitted, RepeatableRead, Serializable} {
		t.Run(isolationText(isolation), func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			ref, err := e.Begin(op, TxOptions{Isolation: isolation})
			if err != nil {
				t.Fatal(err)
			}
			owner := op.session.transactions
			scope := &StatementScope{op: op, owner: owner, txRef: ref}
			base := e.catalog.state
			if err := scope.snapshotPhase(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			if stableIsolation(isolation) && owner.sequenceCatalog != base || !stableIsolation(isolation) && owner.sequenceCatalog != nil {
				t.Fatal("snapshot pointer policy")
			}
			mark := owner.captureSavepoint("s")
			owner.catalog = newCatalogChanges()
			owner.restoreSavepoint(mark, mark.settings.clone())
			if stableIsolation(isolation) && owner.sequenceCatalog != base {
				t.Fatal("savepoint discarded transaction snapshot")
			}
			e.mu.Lock()
			e.catalog.state = catalog.CloneState(e.catalog.state)
			e.mu.Unlock()
			if err := scope.snapshotPhase(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			if stableIsolation(isolation) && owner.sequenceCatalog != base {
				t.Fatal("peer publication replaced retained pointer")
			}
			if _, err := e.Rollback(op, ref); err != nil {
				t.Fatal(err)
			}
			if owner.sequenceCatalog != nil || owner.snapshotSet {
				t.Fatal("terminal rollback retained pointer")
			}
		})
	}
}
func TestPGSequenceSerializableCandidateLifecycle(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "unsafe_retry"}[unsafe], func(t *testing.T) {
			e := New()
			writer := serializableUnitNode(t, e, false)
			if unsafe {
				out := serializableUnitNode(t, e, false)
				e.mu.Lock()
				writer.didWrite = true
				writer.outgoing[out] = struct{}{}
				out.incoming[writer] = struct{}{}
				e.serializableCommittedLocked(out.ref, e.commitSeq)
				e.mu.Unlock()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, node := serializableUnitAcquisition(t, e, ctx, true, true)
			join := serializableUnitCaptureWorker(t, scope)
			serializableUnitAwaitCandidate(t, e, node)
			e.mu.Lock()
			old := e.catalog.state
			captured := scope.owner.sequenceCatalog
			e.mu.Unlock()
			if captured != old {
				t.Fatal("candidate failed to retain catalog")
			}
			if unsafe {
				e.mu.Lock()
				e.catalog.state = catalog.CloneState(e.catalog.state)
				next := e.catalog.state
				e.commitSeq++
				e.serializableCommittedLocked(writer.ref, e.commitSeq)
				e.endSerializableLocked(writer.ref, true)
				e.mu.Unlock()
				if err := join(); err != nil {
					t.Fatal(err)
				}
				if scope.owner.sequenceCatalog != next || !scope.owner.snapshotSet {
					t.Fatal("unsafe candidate catalog was not recaptured")
				}
			} else {
				cancel()
				if err := join(); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if scope.owner.sequenceCatalog != nil || scope.owner.snapshotSet {
					t.Fatal("canceled candidate retained catalog")
				}
			}
		})
	}
}
