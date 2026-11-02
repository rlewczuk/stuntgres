package stored_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The validators of packages above table run at fixed points of relation
// validation: Policies, then Partition, then the view Definition tail. The
// first failing validator's error is returned and later ones do not run.
func TestRelationValidatorsOrder(t *testing.T) {
	e := engine.New()
	source, _ := namespaceUnitSource(t, e, e.CatalogRead())
	materialized := refreshUnitRelation(t, e)
	read := e.CatalogRead()
	for _, d := range []*catalog.Relation{source, materialized} {
		if err := core.RelationProvider.Validate(read, d); err != nil {
			t.Fatal(d.Name(), err)
		}
	}
	recording := func(calls *[]string, failing string, failure error) table.RelationProvider {
		hook := func(name string) table.RelationValidator {
			return func(catalog.Read, *catalog.Relation) error {
				*calls = append(*calls, name)
				if name == failing {
					return failure
				}
				return nil
			}
		}
		return table.RelationProvider{Validators: table.RelationValidators{Policies: hook("policies"), Partition: hook("partition"), Definition: hook("definition")}}
	}
	for _, c := range []struct {
		d    *catalog.Relation
		want []string
	}{
		{source, []string{"policies", "partition"}},
		{materialized, []string{"policies", "partition", "definition"}},
	} {
		var calls []string
		if err := recording(&calls, "", nil).Validate(read, c.d); err != nil || !slices.Equal(calls, c.want) {
			t.Fatal(c.d.Name(), calls, err)
		}
		for i, failing := range c.want {
			failure := errors.New("rejected by " + failing)
			calls = nil
			if err := recording(&calls, failing, failure).Validate(read, c.d); err != failure || !slices.Equal(calls, c.want[:i+1]) {
				t.Fatal(c.d.Name(), failing, calls, err)
			}
		}
	}
}

func namespaceUnitSource(t *testing.T, e *core.Engine, c catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2615}
	d, ok := c.Relation(a)
	if !ok {
		t.Fatal("missing namespace source")
	}
	return d, e.Catalog.Domain.Ref(c.Epoch(), a)
}

func refreshUnitRelation(t *testing.T, e *core.Engine) *catalog.Relation {
	t.Helper()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	d.Populated = true
	rows := refreshUnitRows(t, d, 3)
	if err := e.StageQueryTableRows(context.Background(), stage, d, rows, enginetest.CatalogTransaction(t, e)); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return d
}

// Exercise the private binder/stager directly, not SQL execution wrappers.
func materializedUnitStage(t *testing.T, e *core.Engine) (*session.Operation, func(), *core.CatalogStage, *catalog.Relation) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	statements, err := parser.Parse("CREATE MATERIALIZED VIEW public.unit_mv AS SELECT 1::int AS i WITH NO DATA")
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*parser.CreateQueryTable)
	input, _ := op.Session.Settings.Presentation()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := core.Describe(context.Background(), e.AnalysisHost(op), s, auth, ids.ObjectRef{}, input, nil, nil)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage, descriptor, skipped, err := core.EnginePrepareQueryTableLocked(e, op, auth, s, b)
	e.Mu.Unlock()
	if err != nil || skipped {
		t.Fatalf("private stage: %v, skipped %v", err, skipped)
	}
	return op, done, stage, descriptor
}

func refreshUnitRows(t *testing.T, d *catalog.Relation, count int) [][]sqltype.Value {
	t.Helper()
	rows := make([][]sqltype.Value, count)
	for i := range rows {
		value, err := sqltype.Integer(d.Columns[0].Usage.Type, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = []sqltype.Value{value}
	}
	return rows
}

func queryCreatePublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err == nil {
		_, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err != nil {
		t.Fatal(err)
	}
	enginetest.CommitCatalogTransaction(t, stage)
}
