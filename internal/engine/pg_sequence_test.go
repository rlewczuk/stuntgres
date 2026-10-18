package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgSequenceUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2224}
	d, ok := e.catalog.state.descriptors[a].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_sequence")
	}
	return d, e.catalog.domain.Ref(e.catalog.state.epoch, a)
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
	if d.name != "pg_sequence" || d.namespace.ObjectID != 11 || d.relationKind != catalogRelation || d.attributeHighWater != 8 || len(d.columns) != 8 {
		t.Fatal(d)
	}
	for i, tc := range []struct {
		name string
		typ  sqltype.ID
	}{
		{"seqrelid", sqltype.Oid}, {"seqtypid", sqltype.Oid}, {"seqstart", sqltype.Int8}, {"seqincrement", sqltype.Int8}, {"seqmax", sqltype.Int8}, {"seqmin", sqltype.Int8}, {"seqcache", sqltype.Int8}, {"seqcycle", sqltype.Bool},
	} {
		c := d.columns[i]
		if c.Name != tc.name || c.Usage.Type.ID() != tc.typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2224 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[tc.name] != i {
			t.Fatal("descriptor", i, c)
		}
	}
	for _, name := range []string{"oid", "last_value", "is_called"} {
		if _, exists := d.index[name]; exists {
			t.Fatal("invented state column", name)
		}
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal(err)
	}
	physical := *d
	physical.relationKind = tableRelation
	if err := (relationProvider{}).Validate(e.CatalogRead(), &physical); err == nil {
		t.Fatal("physical NOT NULL requires constraint identities")
	}
	foreignType, _ := New().CatalogRead().LookupType(sqltype.Oid)
	for _, tc := range []struct {
		name       string
		invalidate func(*relationDescriptor)
	}{
		{"identity", func(r *relationDescriptor) { r.address.ObjectID = 0 }},
		{"type", func(r *relationDescriptor) { r.columns[0].Usage.Type = foreignType }},
		{"live attribute", func(r *relationDescriptor) { r.attributes[0] = 0 }},
		{"constraint record", func(r *relationDescriptor) { r.notNullConstraints = []notNullConstraint{{Attribute: 1}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := *d
			invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
			invalid.attributes = append([]int32(nil), d.attributes...)
			tc.invalidate(&invalid)
			if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
				t.Fatal("virtual-source exemption bypassed validation")
			}
		})
	}
	if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.acl)
	}
	class := e.catalog.state.descriptors[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}].(*relationDescriptor)
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
		kind, _ := row[class.index["relkind"]].Text()
		count, _ := row[class.index["relnatts"]].Integer()
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
	state.descriptors = maps.Clone(e.catalog.state.descriptors)
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	options := &sequenceOptions{typ: sqltype.Int4, start: 15, increment: 7, max: 200, min: -20, cache: 9223372036854775807, cycle: true}
	d := &relationDescriptor{address: a, relationKind: sequenceRelation, sequence: options}
	state.descriptors[a] = d
	state.descriptors[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}] = &relationDescriptor{relationKind: tableRelation}
	state.descriptors[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}] = &relationDescriptor{relationKind: viewRelation}
	e.sequences[a] = &sequenceGenerator{last: 99, called: true, generation: 100}
	rows, err := capturePGSequence(context.Background(), &state, nil, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32][]any{20000: {uint32(20000), uint32(23), int64(15), int64(7), int64(200), int64(-20), int64(9223372036854775807), true}}
	if !reflect.DeepEqual(pgSequenceUnitBag(t, rows), want) {
		t.Fatal(pgSequenceUnitBag(t, rows), want)
	}
	if options.cache != 9223372036854775807 || e.sequences[a].last != 99 || !e.sequences[a].called {
		t.Fatal("projection touched definition or state")
	}
}
func TestPGSequenceEmptySource(t *testing.T) {
	e := New()
	source, ref := pgSequenceUnitSource(t, e)
	base := *e.catalog.state
	base.descriptors = maps.Clone(e.catalog.state.descriptors)
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	base.descriptors[a] = &relationDescriptor{address: a, relationKind: sequenceRelation, sequence: &sequenceOptions{typ: sqltype.Int8, start: 1, increment: 1, max: 9223372036854775807, min: 1, cache: 1}}
	own := newCatalogChanges()
	own.descriptors[a] = nil
	for _, tc := range []struct {
		name string
		base *catalogState
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
	base.descriptors = maps.Clone(e.catalog.state.descriptors)
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	dropped := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
	added := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	options := &sequenceOptions{typ: sqltype.Int2, start: -1, increment: -3, max: -1, min: -32768, cache: 1}
	d := &relationDescriptor{address: a, relationKind: sequenceRelation, sequence: options}
	base.descriptors[a] = d
	other := *d
	other.address = dropped
	base.descriptors[dropped] = &other
	old, err := capturePGSequence(context.Background(), &base, nil, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	oldBag := pgSequenceUnitBag(t, old)
	own := newCatalogChanges()
	updated := *d
	newOptions := *options
	newOptions.start = -4
	updated.sequence = &newOptions
	own.descriptors[a] = &updated
	own.descriptors[dropped] = nil
	addition := *d
	addition.address = added
	own.descriptors[added] = &addition
	want := map[uint32][]any{20000: {uint32(20000), uint32(21), int64(-4), int64(-3), int64(-1), int64(-32768), int64(1), false}, 20002: {uint32(20002), uint32(21), int64(-1), int64(-3), int64(-1), int64(-32768), int64(1), false}}
	// Latest publication has none of the retained base's sequence definitions.
	e.catalog.state = cloneCatalog(e.catalog.state)
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
	if owner.accessedTemporary || !reflect.DeepEqual(pgSequenceUnitBag(t, old), oldBag) || d.sequence.start != -1 {
		t.Fatal("capture mutated retained rows or physical TEMP authority")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, state := range []*catalogState{&base, {descriptors: map[ids.ObjectAddress]Descriptor{}}} {
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
			e.catalog.state = cloneCatalog(e.catalog.state)
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
				e.catalog.state = cloneCatalog(e.catalog.state)
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
