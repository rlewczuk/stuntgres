package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func inheritanceDMLPublishedGraph(t *testing.T) (*core.Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	ps, p := queryCreateStage(t, e, "p", 1)
	if err := e.StageQueryTableRows(context.Background(), ps, p, [][]sqltype.Value{{inheritanceQueryInt(e, 10)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, ps)
	cs, c := queryCreateStage(t, e, "c", 1)
	c.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(p.Address()), Columns: map[int32]catalog.InheritedColumn{c.Attributes[0]: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: p.Attributes[0]}}}}, NotNull: map[int32]catalog.InheritedConstraint{}, Checks: map[string]catalog.InheritedConstraint{}}
	if err := e.StageQueryTableRows(context.Background(), cs, c, [][]sqltype.Value{{inheritanceQueryInt(e, 20)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, cs)
	// Child publication may issue the parent's real subclass-marker revision.
	// Return the actual published identities, not pre-publication candidates.
	published := e.CatalogRead()
	return e, published.MustRelation(p.Address()), published.MustRelation(c.Address())
}
