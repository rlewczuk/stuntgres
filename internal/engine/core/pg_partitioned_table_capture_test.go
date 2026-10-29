package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func partitionCatalogInput(t *testing.T, e *core.Engine) pgcatalog.Input {
	t.Helper()
	view := e.CatalogRead()
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3350})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.Roles(), Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})}
}

func partitionCatalogRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(3350)
	if !ok {
		t.Fatal("missing partition catalog producer")
	}
	rows, err := produce(in)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func partitionCatalogTypes(in pgcatalog.Input) [4]*sqltype.Descriptor {
	return [4]*sqltype.Descriptor{in.Source.Columns[4].Usage.Type, in.Source.Columns[5].Usage.Type, in.Source.Columns[6].Usage.Type, in.Source.Columns[7].Usage.Type}
}

func TestPGPartitionedTableCatalogCapture(t *testing.T) {
	e := engine.New()
	produce, ok := pgcatalog.ProducerFor(3350)
	if !ok {
		t.Fatal("missing producer")
	}
	for _, populated := range []bool{false, true} {
		if populated {
			for _, q := range []string{
				"CREATE TABLE ppt_unit(a INT,b INT,c INT) PARTITION BY RANGE(c,(a+(1+2)))",
				"CREATE TABLE ppt_unit_default PARTITION OF ppt_unit DEFAULT PARTITION BY LIST(b)",
				"CREATE TABLE ppt_unit_leaf PARTITION OF ppt_unit_default DEFAULT",
				"CREATE TABLE ppt_unit_hash(a INT) PARTITION BY HASH(a)",
			} {
				enginetest.Run(t, e, q)
			}
		}
		in := partitionCatalogInput(t, e)
		rows := partitionCatalogRows(t, in)
		wantCount := 0
		if populated {
			wantCount = 3
		}
		if len(rows) != wantCount || len(in.Source.Columns) != 8 {
			t.Fatal("descriptor membership/schema", rows)
		}
		for i, id := range []sqltype.ID{26, 18, 21, 26, 22, 30, 30, 194} {
			c := in.Source.Columns[i]
			typ, _ := in.Catalog.LookupType(id)
			meta := in.Source.ColumnMeta.CatalogAttributes[i]
			dims, collation := int16(0), uint32(0)
			if i >= 4 && i <= 6 {
				dims = 1
			}
			if i == 7 {
				collation = 950
			}
			if c.Usage.Type != typ || c.Usage.Typmod != -1 || c.OriginTable != 3350 || c.OriginAttribute != int16(i+1) || !c.NullabilityKnown || c.Nullable != (i == 7) || meta.NotNull != (i != 7) || meta.Dimensions != dims || uint32(meta.Collation) != collation {
				t.Fatal("source schema authority", i, c, meta)
			}
		}
		for _, row := range rows {
			if len(row) != 8 {
				t.Fatal("row arity", row)
			}
			oid, _ := row[0].OID()
			d := in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
			for i, value := range row {
				if value.Type() != in.Source.Columns[i].Usage.Type || value.IsNull() != (i == 7 && d.Name() != "ppt_unit") {
					t.Fatal("row typed ownership/nullability", d.Name(), i, value)
				}
			}
			for i := 4; i <= 6; i++ {
				if row[i].ArrayRank() != 1 || row[i].ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 0, Length: int32(len(d.Partition.Keys))}) {
					t.Fatal("vector shape", row[i])
				}
			}
			defaultID, _ := row[3].OID()
			if defaultID != uint32(d.Partition.DefaultChild.ObjectID) {
				t.Fatal("immediate default identity", defaultID)
			}
			wantStrategy, wantAttrs, wantCount, method := "r", "3 0", int64(2), ids.OID(403)
			switch d.Name() {
			case "ppt_unit_default":
				wantStrategy, wantAttrs, wantCount = "l", "2", 1
			case "ppt_unit_hash":
				wantStrategy, wantAttrs, wantCount, method = "h", "1", 1, 405
			}
			strategy, _ := row[1].Text()
			count, _ := row[2].Integer()
			attrs, err := row[4].Type().Output(t.Context(), row[4])
			if err != nil || strategy != wantStrategy || count != wantCount || attrs != wantAttrs {
				t.Fatal("independent strategy/key facts", d.Name(), row, err)
			}
			for i := range row[5].ArrayLen() {
				classID, _ := row[5].ArrayElement(i).OID()
				class, ok := in.Catalog.OperatorClass(ids.OID(classID))
				collation, _ := row[6].ArrayElement(i).OID()
				if !ok || class.Method != method || class.InputType != 23 || collation != 0 {
					t.Fatal("selected current class/collation authority", d.Name(), classID, collation)
				}
			}
			if d.Name() == "ppt_unit" {
				for column, want := range map[int]string{4: "3 0", 5: "1978 1978", 6: "0 0"} {
					text, err := row[column].Type().Output(t.Context(), row[column])
					if err != nil || text != want {
						t.Fatal("numeric projection", column, text, err)
					}
				}
				tree, err := row[7].Tree()
				list, ok := tree.(plan.ExpressionListTree)
				if err != nil || !ok || list.Len() != 1 || !reflect.DeepEqual(list.At(0), d.Partition.Keys[1].Derived.Definition) || reflect.DeepEqual(list.At(0), d.Partition.Keys[1].Derived.Execution) {
					t.Fatal("original ordered definition tree", tree, err)
				}
			}
		}
		for _, column := range []int{-1, 0, 1, 2, 3, 4, 5, 6, 7} {
			bad := *in.Source
			bad.Columns = append([]catalog.Column(nil), bad.Columns...)
			if column < 0 {
				bad.Columns = bad.Columns[:7]
			} else {
				foreign := engine.New()
				bad.Columns[column].Usage.Type, _ = foreign.CatalogRead().LookupType(bad.Columns[column].Usage.Type.ID())
			}
			invalid := in
			invalid.Source = &bad
			next := catalog.CloneState(in.Catalog.State())
			next.SetDescriptor(bad.Address(), &bad)
			invalid.Catalog = e.Catalog.Read(next)
			if rows, err := produce(invalid); rows != nil || err == nil {
				t.Fatal("invalid empty/populated source published", column, rows, err)
			}
		}
		// Real authenticated dispatch, including the zero-row source.
		e.Mu.Lock()
		captured, err := core.CaptureCatalogSourceRows(in, produce)
		e.Mu.Unlock()
		if err != nil || captured == nil || captured.Count() != wantCount {
			t.Fatal("authenticated capture", captured, err)
		}
	}
}

func TestPGPartitionedTableCaptureStableAttributes(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE ppt_stable(a INT,b INT,c INT) PARTITION BY RANGE(c,a)")
	in := partitionCatalogInput(t, e)
	old := in.Catalog.MustRelation(enginetest.RoleTableRef(t, e, "ppt_stable").Address)
	before := partitionCatalogRows(t, in)[0]

	// Supply a valid immutable descriptor with a retired non-key attribute.
	// Hierarchy DROP COLUMN admission is not this catalog producer's contract.
	next := *old
	next.Columns = []catalog.Column{old.Columns[0], old.Columns[2]}
	next.Attributes = []int32{1, 3}
	next.Index = map[string]int{"a": 0, "c": 1}
	history := pgcatalog.AttributeMetadata(old, 1)
	history.Name = "........pg.dropped.2........"
	history.TypeID, history.NotNull, history.HasDefault, history.Generated, history.Dropped = 0, false, false, "", true
	next.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{2: history}
	next.ObjectRevision++
	state := catalog.CloneState(in.Catalog.State())
	state.SetDescriptor(next.Address(), &next)
	captured := in
	captured.Catalog = e.Catalog.Read(state)
	after := partitionCatalogRows(t, captured)[0]
	produce, _ := pgcatalog.ProducerFor(3350)
	e.Mu.Lock()
	dispatched, err := core.CaptureCatalogSourceRows(captured, produce)
	e.Mu.Unlock()
	if err != nil || dispatched == nil || dispatched.Count() != 1 || !reflect.DeepEqual(dispatched.Anonymous[0], after) {
		t.Fatal("authenticated stable attribute capture", dispatched, err)
	}
	for _, row := range [][]sqltype.Value{before, after} {
		attrs, err := row[4].Type().Output(t.Context(), row[4])
		if err != nil || attrs != "3 1" {
			t.Fatal("stable key attributes were compacted to column slots", attrs, err)
		}
	}
	if !reflect.DeepEqual(before, after) || in.Catalog.MustRelation(old.Address()) != old || len(old.Columns) != 3 || next.IndexForAttribute(3) != 1 {
		t.Fatal("stable attribute capture changed immutable source or key facts")
	}
}

func TestPGPartitionedTableCaptureAuthority(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE ppt_valid_prefix(a INT) PARTITION BY RANGE(a)")
	enginetest.Run(t, e, "CREATE TABLE ppt_authority(a INT) PARTITION BY RANGE(a)")
	in := partitionCatalogInput(t, e)
	produce, _ := pgcatalog.ProducerFor(3350)
	foreign := engine.New()
	for _, mode := range []string{"owner", "state", "domain", "epoch", "source"} {
		bad := in
		switch mode {
		case "owner":
			bad.Owner = nil
		case "state":
			bad.Catalog = catalog.NewRead(in.Owner, nil)
		case "domain":
			bad.Ref = foreign.Catalog.Domain.Ref(in.Ref.Epoch, in.Ref.Address)
		case "epoch":
			bad.Ref.Epoch++
		case "source":
			detached := *in.Source
			bad.Source = &detached
		}
		e.Mu.Lock()
		rows, err := core.CaptureCatalogSourceRows(bad, produce)
		e.Mu.Unlock()
		var state interface{ SQLState() string }
		if rows != nil || err == nil || errors.As(err, &state) {
			t.Fatal("dispatch authority", mode, rows, err)
		}
	}
	ref := enginetest.RoleTableRef(t, e, "ppt_authority")
	d := in.Catalog.MustRelation(ref.Address)
	for _, mode := range []string{"detached", "owner", "state", "kind", "definition", "strategy", "empty", "attribute", "class", "type", "collation", "default", "expression"} {
		bad := *d
		definition := *d.Partition
		definition.Keys = append([]catalog.PartitionKeyEntry(nil), definition.Keys...)
		bad.Partition = &definition
		view := in.Catalog
		switch mode {
		case "owner":
			view = catalog.NewRead(nil, view.State())
		case "state":
			view = catalog.NewRead(view.Owner(), nil)
		case "kind":
			bad.RelationKind = catalog.TableRelation
		case "definition":
			bad.Partition = nil
		case "strategy":
			definition.Strategy = parser.PartitionStrategy(99)
		case "empty":
			definition.Keys = nil
		case "attribute":
			definition.Keys[0].Attribute = 999
		case "class":
			definition.Keys[0].Class = sqltype.IndexClass{}
		case "type":
			definition.Keys[0].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Int4)
		case "collation":
			definition.Keys[0].Usage.Collation.ID = 950
		case "default":
			definition.DefaultChild = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999}
		case "expression":
			definition.Keys[0].Attribute = 0
			definition.Keys[0].Derived = &catalog.DerivedIndexKey{Attributes: d.Attributes, Definition: plan.ColumnExpr{Origin: in.Owner.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999999, SubID: 1})}}
		}
		if mode != "detached" && mode != "owner" && mode != "state" {
			next := catalog.CloneState(view.State())
			next.SetDescriptor(d.Address(), &bad)
			view = e.Catalog.Read(next)
		}
		fact, err := in.Runtime.PartitionMetadata(t.Context(), view, &bad, partitionCatalogTypes(in))
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) || !reflect.DeepEqual(fact, pgcatalog.PartitionMetadataFact{}) {
			t.Fatal("projection authority", mode, fact, err)
		}
		if mode != "owner" && mode != "state" && mode != "detached" {
			candidate := in
			candidate.Catalog = view
			if rows, err := produce(candidate); rows != nil || err == nil {
				t.Fatal("malformed descriptor returned prefix", mode, rows, err)
			}
		}
	}
	for _, address := range []ids.ObjectAddress{
		{ClassID: ids.OperatorClassCatalogID, ObjectID: 1978},
		{ClassID: ids.TypeClassID, ObjectID: 23},
	} {
		next := catalog.CloneState(in.Catalog.State())
		next.RemoveDescriptor(address)
		view := e.Catalog.Read(next)
		fact, err := in.Runtime.PartitionMetadata(t.Context(), view, d, partitionCatalogTypes(in))
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) || !reflect.DeepEqual(fact, pgcatalog.PartitionMetadataFact{}) {
			t.Fatal("numeric identity outlived provider authority", address, fact, err)
		}
	}
}

// Err checkpoints deterministically interrupt the real enumeration/key walk;
// no elapsed-time assumption or production inspection hook is needed.
type partitionCancelContext struct {
	context.Context
	remaining int
}

func (c *partitionCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

type partitionCancelRuntime struct {
	pgcatalog.Runtime
	cancel context.CancelFunc
}

func (r partitionCancelRuntime) PartitionMetadata(ctx context.Context, view catalog.Read, relation *catalog.Relation, types [4]*sqltype.Descriptor) (pgcatalog.PartitionMetadataFact, error) {
	fact, err := r.Runtime.PartitionMetadata(ctx, view, relation, types)
	if err == nil {
		r.cancel()
	}
	return fact, err
}

func TestPGPartitionedTableCaptureCancellation(t *testing.T) {
	e := engine.New()
	produce, _ := pgcatalog.ProducerFor(3350)
	for _, populated := range []bool{false, true} {
		if populated {
			enginetest.Run(t, e, "CREATE TABLE ppt_cancel(a INT,b INT,c INT) PARTITION BY RANGE(a,(b+1),c)")
			enginetest.Run(t, e, "CREATE TABLE ppt_cancel_second(a INT) PARTITION BY HASH(a)")
		}
		in := partitionCatalogInput(t, e)
		for _, checkpoint := range []int{1, 2, 10} {
			in.Context = &partitionCancelContext{Context: t.Context(), remaining: checkpoint}
			if rows, err := produce(in); rows != nil || err != context.Canceled {
				t.Fatal("enumeration published prefix", populated, checkpoint, rows, err)
			}
		}
		if populated {
			d := in.Catalog.MustRelation(enginetest.RoleTableRef(t, e, "ppt_cancel").Address)
			// Advance through every real validation/key/vector/tree checkpoint,
			// requiring zero facts for each interrupted invocation.
			succeeded := false
			for checkpoint := 1; checkpoint < 100; checkpoint++ {
				ctx := &partitionCancelContext{Context: t.Context(), remaining: checkpoint}
				fact, err := in.Runtime.PartitionMetadata(ctx, in.Catalog, d, partitionCatalogTypes(in))
				if err == nil {
					succeeded = true
					break
				}
				if err != context.Canceled || !reflect.DeepEqual(fact, pgcatalog.PartitionMetadataFact{}) {
					t.Fatal("key projection published partial fact", checkpoint, fact, err)
				}
			}
			if !succeeded {
				t.Fatal("projection never reached final checkpoint")
			}
			ctx, cancel := context.WithCancel(t.Context())
			in.Context = ctx
			in.Runtime = partitionCancelRuntime{Runtime: in.Runtime, cancel: cancel}
			rows, err := produce(in)
			cancel()
			if rows != nil || err != context.Canceled {
				t.Fatal("completed first fact leaked a row prefix", rows, err)
			}
		}
	}
}

func TestPGPartitionedTableCaptureOwnership(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE ppt_owned(a INT,b TEXT) PARTITION BY RANGE(a,lower(b))",
		"CREATE TABLE ppt_owned_default PARTITION OF ppt_owned DEFAULT",
	} {
		enginetest.Run(t, e, q)
	}
	in := partitionCatalogInput(t, e)
	old := in.Catalog.MustRelation(enginetest.RoleTableRef(t, e, "ppt_owned").Address)
	row := partitionCatalogRows(t, in)[0]
	tree, err := row[7].Tree()
	list, ok := tree.(plan.ExpressionListTree)
	if err != nil || !ok || list.Len() != 1 || !reflect.DeepEqual(list.At(0), old.Partition.Keys[1].Derived.Definition) {
		t.Fatal("retained definition carrier", tree, err)
	}
	original := old.Partition.Keys[1].Derived.Definition.(functions.String)
	retained := list.At(0).(functions.String)
	if &original.Args[0] != &retained.Args[0] {
		t.Fatal("immutable definition backing copied or rebound")
	}
	before, err := tree.StructuralOutput(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Repeated projections own distinct numeric buffers while immutable nodes
	// remain the descriptor's original definitions, not reparsed display SQL.
	other := partitionCatalogRows(t, in)[0]
	if row[4].ArrayElement(0).Type() != row[4].Type().Element() || !reflect.DeepEqual(other, row) {
		t.Fatal("captured vector element generation")
	}
	vectorBacking := func(value sqltype.Value) [2]uintptr {
		payload := reflect.ValueOf(value).FieldByName("array")
		return [2]uintptr{payload.Pointer(), payload.Elem().FieldByName("elements").Pointer()}
	}
	var backing [3][2]uintptr
	for i := range backing {
		backing[i] = vectorBacking(row[4+i])
		if backing[i][0] == 0 || backing[i][1] == 0 || backing[i] == vectorBacking(other[4+i]) {
			t.Fatal("numeric captures share mutable construction backing", i)
		}
	}
	enginetest.Run(t, e, "ALTER TABLE ppt_owned RENAME COLUMN b TO renamed_b")
	if in.Catalog.MustRelation(old.Address()) != old || e.CatalogRead().MustRelation(old.Address()) == old {
		t.Fatal("rename mutated captured descriptor")
	}
	enginetest.Run(t, e, "DROP TABLE ppt_owned CASCADE")
	enginetest.Run(t, e, "CREATE TABLE ppt_owned(a INT) PARTITION BY HASH(a)")
	current := partitionCatalogRows(t, partitionCatalogInput(t, e))[0]
	oldID, _ := row[0].OID()
	newID, _ := current[0].OID()
	if oldID == newID || !current[7].IsNull() || old.Partition.DefaultChild.ObjectID == 0 {
		t.Fatal("replacement frontier/default lost")
	}
	after, err := tree.StructuralOutput(t.Context())
	if err != nil || before != after || !reflect.DeepEqual(partitionCatalogRows(t, in)[0], row) {
		t.Fatal("old tree/backing consulted replacement", err)
	}
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	fresh := partitionCatalogInput(t, e)
	if fresh.Source == in.Source || fresh.Catalog.Epoch() == in.Catalog.Epoch() {
		t.Fatal("replacement retained old source authority")
	}
	for _, column := range []int{4, 5, 6, 7} {
		if fresh.Source.Columns[column].Usage.Type == row[column].Type() {
			t.Fatal("replacement reused captured carrier generation", column)
		}
	}
	if len(partitionCatalogRows(t, fresh)) != 0 || !reflect.DeepEqual(partitionCatalogRows(t, in)[0], row) || &original.Args[0] != &retained.Args[0] {
		t.Fatal("replacement changed retained generation/backing")
	}
	for column, want := range map[int]string{4: "1 0", 5: "1978 3126", 6: "0 100"} {
		value := row[column]
		if vectorBacking(value) != backing[column-4] {
			t.Fatal("retained numeric payload was replaced", column)
		}
		text, err := value.Type().Output(t.Context(), value)
		if err != nil || text != want || value.Type() != in.Source.Columns[column].Usage.Type || value.ArrayDimension(0).Lower != 0 {
			t.Fatal("retained numeric backing/type generation", column, text, err)
		}
	}
}
