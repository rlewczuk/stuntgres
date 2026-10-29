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
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func pgIndexCaptureInput(t *testing.T, e *core.Engine) pgcatalog.Input {
	t.Helper()
	view := e.CatalogRead()
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2610})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), d.Address()), Source: d, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})}
}

func pgIndexCaptureRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(2610)
	if !ok {
		t.Fatal("missing pg_index producer")
	}
	rows, err := produce(in)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPGIndexCatalogCapture(t *testing.T) {
	e := engine.New()
	in := pgIndexCaptureInput(t, e)
	if rows := pgIndexCaptureRows(t, in); len(rows) != 0 {
		t.Fatal("empty inventory manufactured indexes", rows)
	}
	wantTypes := []sqltype.ID{26, 26, 21, 21, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 22, 30, 30, 22, 194, 194}
	if len(in.Source.Columns) != len(wantTypes) {
		t.Fatal("incomplete schema")
	}
	for i, c := range in.Source.Columns {
		typ, ok := in.Catalog.LookupType(wantTypes[i])
		fact := in.Source.ColumnMeta.CatalogAttributes[i]
		dims, collation := int16(0), uint32(0)
		if i >= 15 && i <= 18 {
			dims = 1
		}
		if i >= 19 {
			collation = 950
		}
		if !ok || c.Usage.Type != typ || c.Usage.Typmod != -1 || c.OriginTable != 2610 || c.OriginAttribute != int16(i+1) || !c.NullabilityKnown || c.Nullable != (i >= 19) || fact.NotNull != (i < 19) || fact.Dimensions != dims || uint32(fact.Collation) != collation {
			t.Fatal("schema/type/attribute authority", i, c, fact)
		}
	}
	produce, _ := pgcatalog.ProducerFor(2610)
	for _, populated := range []bool{false, true} {
		if populated {
			enginetest.Run(t, e, "CREATE TABLE pgi_capture(n INT,payload TEXT)")
			enginetest.Run(t, e, "CREATE INDEX pgi_capture_ix ON pgi_capture(n DESC) INCLUDE(payload)")
			in = pgIndexCaptureInput(t, e)
			rows := pgIndexCaptureRows(t, in)
			if len(rows) != 1 || len(rows[0]) != 21 {
				t.Fatal("complete real index capture", rows)
			}
			for i, v := range rows[0] {
				if v.Type() != in.Source.Columns[i].Usage.Type || v.IsNull() != (i >= 19) {
					t.Fatal("row descriptor/null authority", i, v)
				}
			}
		}
		bad := *in.Source
		bad.Columns = append([]catalog.Column(nil), in.Source.Columns...)
		bad.Columns = bad.Columns[:20]
		invalid := in
		invalid.Source = &bad
		if rows, err := produce(invalid); rows != nil || err == nil {
			t.Fatal("wrong arity accepted", rows, err)
		}
		bad.Columns = append([]catalog.Column(nil), in.Source.Columns...)
		foreign := engine.New()
		bad.Columns[20].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.PgNodeTree)
		if rows, err := produce(invalid); rows != nil || err == nil {
			t.Fatal("foreign descriptor generation accepted", rows, err)
		}
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		invalid = in
		invalid.Context = canceled
		if rows, err := produce(invalid); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled capture published a prefix", rows, err)
		}
	}
	// Exercise the actual authenticated dispatch and its complete captured row.
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	source, err := core.EngineOpenRelationSourceLocked(e, scope, in.Ref, in.Source.Owner, plan.RowMark{}, nil, nil)
	if err != nil || source == nil {
		t.Fatal("admitted composed capture", err)
	}
	defer source.Close()
	if len(source.Anonymous) != 1 || len(source.Anonymous[0]) != 21 {
		t.Fatal("composed capture lost full row")
	}
	// Projection consumes the captured view's authority, not the dispatch's
	// catalog-source Ref. Supply invalid authority to the real projection seam.
	foreign := engine.New()
	indexRef := enginetest.RoleTableRef(t, e, "pgi_capture_ix")
	for _, invalid := range []struct {
		name string
		view catalog.Read
		ref  ids.ObjectRef
	}{
		{"missing owner", catalog.NewRead(nil, in.Catalog.State()), indexRef},
		{"foreign owner", foreign.Catalog.Read(in.Catalog.State()), indexRef},
		{"foreign domain", in.Catalog, foreign.Catalog.Domain.Ref(indexRef.Epoch, indexRef.Address)},
		{"foreign epoch", in.Catalog, indexRef.Domain().Ref(indexRef.Epoch+1, indexRef.Address)},
	} {
		facts, names, err := index.ProjectIndexMetadata(t.Context(), invalid.view, invalid.ref)
		if err == nil || err.Error() != "stuntgres: invalid index metadata capture" || !reflect.DeepEqual(facts, index.Metadata{}) || !reflect.DeepEqual(names, index.IndexesMetadata{}) {
			t.Fatal("invalid projection authority published metadata", invalid.name, facts, names, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("authority invariant acquired SQLSTATE", invalid.name, err)
		}
	}
}

func TestPGIndexMetadataOwnership(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE pgi_owned(unused INT,n INT,s TEXT,payload INT)",
		"CREATE INDEX pgi_owned_ix ON pgi_owned(lower(s),(n+(2-1)),n DESC NULLS LAST) INCLUDE(payload) WHERE n > 0",
		"ALTER TABLE pgi_owned DROP COLUMN unused",
	} {
		enginetest.Run(t, e, q)
	}
	in := pgIndexCaptureInput(t, e)
	ref := enginetest.RoleTableRef(t, e, "pgi_owned_ix")
	d := in.Catalog.MustRelation(ref.Address)
	projection, _, err := index.ProjectIndexMetadata(t.Context(), in.Catalog, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projection.IndKey, []int16{0, 0, 2, 4}) || !reflect.DeepEqual(projection.IndClass, []ids.OID{3126, 1978, 1978}) || !reflect.DeepEqual(projection.IndCollation, []ids.OID{100, 0, 0}) || !reflect.DeepEqual(projection.IndOption, []int16{0, 0, 1}) || len(projection.IndexExpressions) != 2 {
		t.Fatal("original definition vectors", projection)
	}
	row := pgIndexCaptureRows(t, in)[0]
	tree, err := row[19].Tree()
	list, ok := tree.(plan.ExpressionListTree)
	if err != nil || !ok || list.Len() != 2 {
		t.Fatal("ordered typed expression list", tree, err)
	}
	for i := range list.Len() {
		if !reflect.DeepEqual(list.At(i), d.SQLIndex.Keys[i].Derived.Definition) {
			t.Fatal("definition node replaced", i)
		}
	}
	if reflect.DeepEqual(list.At(1), d.SQLIndex.Keys[1].Derived.Execution) {
		t.Fatal("fixture lost definition/execution distinction")
	}
	original := d.SQLIndex.Keys[0].Derived.Definition.(functions.String)
	retained := list.At(0).(functions.String)
	if &original.Args[0] != &retained.Args[0] {
		t.Fatal("immutable function backing copied")
	}
	predicate, err := row[20].Tree()
	bound, ok := predicate.(plan.BoundTree)
	if err != nil || !ok || !reflect.DeepEqual(bound.Expression, d.SQLIndex.Predicate.Definition) {
		t.Fatal("original predicate lost", predicate, err)
	}
	before, err := tree.StructuralOutput(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Candidate catalog roots carry the same owner facts used by the real
	// concurrent lifecycle; the SQL producer must not infer validity/readiness
	// from index existence or from each other.
	for _, state := range [][3]bool{{false, false, true}, {true, false, true}, {true, true, true}, {false, false, false}} {
		definition := *d.SQLIndex
		definition.Ready, definition.Valid, definition.Live = state[0], state[1], state[2]
		replacement := *d
		replacement.SQLIndex = &definition
		next := catalog.CloneState(in.Catalog.State())
		next.SetDescriptor(d.Address(), &replacement)
		candidate := in
		candidate.Catalog = e.Catalog.Read(next)
		got := pgIndexCaptureRows(t, candidate)[0]
		for offset, want := range map[int]bool{10: state[1], 12: state[0], 13: state[2]} {
			flag, err := got[offset].Boolean()
			if err != nil || flag != want {
				t.Fatal("publication fact rederived", state, offset, flag, err)
			}
		}
	}
	// A separately owned projection is writable without changing descriptors or
	// the list and numeric buffers already transferred into this captured row.
	projection.IndKey[0] = 9
	projection.IndClass[0] = 9
	projection.IndCollation[0] = 9
	projection.IndOption[0] = 9
	projection.IndexExpressions[0] = nil
	enginetest.Run(t, e, "ALTER TABLE pgi_owned RENAME COLUMN n TO renamed_n")
	enginetest.Run(t, e, "DROP INDEX pgi_owned_ix")
	enginetest.Run(t, e, "CREATE INDEX pgi_owned_ix ON pgi_owned(renamed_n)")
	current := pgIndexCaptureRows(t, pgIndexCaptureInput(t, e))[0]
	oldOID, _ := row[0].OID()
	newOID, _ := current[0].OID()
	if oldOID == newOID || !current[19].IsNull() || !current[20].IsNull() || current[19].Type().ID() != sqltype.PgNodeTree || current[20].Type().ID() != sqltype.PgNodeTree {
		t.Fatal("replacement graph or typed NULL absence lost")
	}
	after, err := tree.StructuralOutput(t.Context())
	if err != nil || before != after || row[19].Type() != in.Source.Columns[19].Usage.Type || row[20].Type() != in.Source.Columns[20].Usage.Type {
		t.Fatal("retained tree generation/backing changed", err)
	}
	for column, want := range map[int]string{15: "0 0 2 4", 16: "100 0 0", 17: "3126 1978 1978", 18: "0 0 1"} {
		v := row[column]
		text, err := v.Type().Output(t.Context(), v)
		if err != nil || text != want || v.ArrayDimension(0).Lower != 0 || v.Type() != in.Source.Columns[column].Usage.Type {
			t.Fatal("retained vector backing/generation changed", column, text, err)
		}
	}
	if historical := pgIndexCaptureRows(t, in); !reflect.DeepEqual(historical[0], row) {
		t.Fatal("historical capture consulted replacement graph")
	}
}
