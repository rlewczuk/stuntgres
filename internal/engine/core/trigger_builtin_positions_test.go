package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

func triggerBuiltinPositionRow(t *testing.T, ctx context.Context, sources []string) (sqltype.Value, error) {
	t.Helper()
	e, scope, read, relation, _ := triggerUnitFixtureContext(t, ctx, "BEGIN RETURN NEW; END")
	text, _ := read.LookupType(sqltype.Text)
	vector, _ := read.LookupType(sqltype.TSVector)
	columns := []catalog.Column{{Name: "v", Usage: vector.Usage(), Nullable: true}}
	members := []sqltype.CompositeMember{{Attribute: 1, Name: "v", Usage: vector.Usage()}}
	index := map[string]int{"v": 0}
	attributes := []int32{1}
	arguments := []string{"v", "pg_catalog.english"}
	row := []sqltype.Value{sqltype.Null(vector)}
	for i, source := range sources {
		name := fmt.Sprintf("source%d", i)
		columns = append(columns, catalog.Column{Name: name, Usage: text.Usage(), Nullable: true})
		members = append(members, sqltype.CompositeMember{Attribute: int32(i + 2), Name: name, Usage: text.Usage()})
		index[name] = i + 1
		attributes = append(attributes, int32(i + 2))
		arguments = append(arguments, name)
		row = append(row, sqltype.String(text, source))
	}
	shape, err := sqltype.Composite(relation.RowType, relation.Name(), "public."+relation.Name(), 0, members)
	if err != nil { t.Fatal(err) }
	copy := *relation
	copy.Columns, copy.Index, copy.Attributes = columns, index, attributes
	rowtype := &catalog.Type{SQLType: shape, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, Owner: relation.Owner, OwningRelation: relation.Address()}
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	state.SetDescriptor(rowtype.Address(), rowtype)
	e.Catalog.PublishLocked(state)
	e.Mu.Unlock()
	read = e.CatalogRead()
	procedure := read.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3752})
	parts := triggerUnitDefinition(t, read, &copy, procedure, "vector_positions", stored.TriggerInsert).Parts()
	parts.Arguments = arguments
	definition, err := stored.NewTrigger(parts)
	if err != nil { t.Fatal(err) }
	relation = triggerUnitInstall(t, e, &copy, definition)
	triggerUnitAdmit(t, scope, relation)
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	result, err := scope.WriteHost().BeforeRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row})
	if err == nil && (!result.Proceed || &result.Row[0] != &row[0]) { t.Fatal("trigger lost producer tuple ownership") }
	return row[0], err
}

func TestTriggerBuiltinsPositions(t *testing.T) {
	for _, tc := range []struct {
		sources []string
		want string
	}{
		{[]string{"cat the", "dog"}, "'cat':1 'dog':3 'tail':4"},
		{[]string{"dog", "cat the"}, "'cat':2 'dog':1 'tail':4"},
		{[]string{"cat", "", "dog", "cat"}, "'cat':1,3 'dog':2 'tail':4"},
		{[]string{"the", "the"}, "'tail':3"},
	} {
		// The final independent source exposes the retained position counter,
		// including trailing stops that are absent from the preceding vector.
		value, err := triggerBuiltinPositionRow(t, t.Context(), append(tc.sources, "tail"))
		if err != nil { t.Fatal(err) }
		actual, err := value.Type().Output(t.Context(), value)
		if err != nil || actual != tc.want { t.Fatal(tc.sources, actual, err, tc.want) }
	}
}

func TestTriggerBuiltinsCancellationPositions(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	heads := 0
	ctx = workadmission.With(ctx, func(_ context.Context, class workadmission.Class) error {
		if class == workadmission.WorkTSVectorConvert {
			heads++
			if heads == 4 { cancel(); return context.Canceled }
		}
		return nil
	})
	value, err := triggerBuiltinPositionRow(t, ctx, []string{"cat the dog"})
	if !errors.Is(err, context.Canceled) || !value.IsNull() {
		t.Fatal("interior cancellation published a partial trigger vector", value, err)
	}
}
