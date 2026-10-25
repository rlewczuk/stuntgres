package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Function-level catalog/storage witnesses, not database/sql API scenarios.
func TestDomainIdentityStage(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA moved", "CREATE DOMAIN public.d AS integer DEFAULT 7 CONSTRAINT never_replay CHECK(FALSE)", "CREATE DOMAIN public.child AS public.d", "CREATE TYPE public.box AS(v public.d,keep integer)"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.identity_carrier(v public.d,c public.child)")
	before := e.CatalogRead()
	old := domainUnitType(t, before, "d")
	child := domainUnitType(t, before, "child")
	integer, _ := before.LookupType(sqltype.Int4)
	physical, err := sqltype.Integer(integer, 9)
	if err != nil {
		t.Fatal(err)
	}
	value, err := old.SQLType.Wrap(physical)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := child.SQLType.Wrap(physical)
	if err != nil {
		t.Fatal(err)
	}
	original := &storage.Relation{Generation: 3, Versions: []storage.RowVersion{{Row: 42, Version: 99, Born: 3, Values: []sqltype.Value{value, inherited}}}}
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), original)
	e.Visibility.SetCommitSeqLocked(3)
	e.Mu.Unlock()
	checkOID := old.DomainChecks[0].OID
	for _, q := range []string{"ALTER DOMAIN public.d RENAME TO renamed", "ALTER DOMAIN public.renamed SET SCHEMA moved"} {
		stage := domainUnitStage(t, e, op, q)
		if StageDomainAlter(stage).AlterContext() != nil {
			t.Fatal("identity edit used executable CHECK alteration")
		}
		domainUnitPublish(t, e, stage)
	}
	current := e.CatalogRead()
	next := current.MustType(old.Address())
	array := current.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)})
	if next.Name() != "renamed" || next.SQLType.SQLName() != "moved.renamed" || next.Address() != old.Address() || next.Array != old.Array || next.Owner != old.Owner || next.DomainChecks[0].OID != checkOID || next.DomainChecks[0].Name != old.DomainChecks[0].Name || next.DomainDefault == nil || next.DomainDefaultOwn != old.DomainDefaultOwn || next.ACLSyntheticDefault != old.ACLSyntheticDefault {
		t.Fatal("identity/definition authority lost", next)
	}
	if array.Namespace() != next.Namespace() || array.SQLType.Element() != next.SQLType || array.Name() != "_renamed" || current.MustType(child.Address()).SQLType.DomainBase().Type != next.SQLType {
		t.Fatal("current reciprocal/base generation stale")
	}
	carrier := current.MustRelation(table.Address())
	store := e.Storage.Relations[table.Address()]
	if carrier.Columns[0].Usage.Type != next.SQLType || carrier.Revision() != table.Revision() || store == original || store.Versions[0].Values[0].Type() != next.SQLType || store.Versions[0].Values[1].Type() != current.MustType(child.Address()).SQLType {
		t.Fatal("physical/catalog reownership missed or executable revision changed")
	}
	if before.MustType(old.Address()) != old || old.Name() != "d" || old.SQLType.SQLName() != "public.d" || table.Columns[0].Usage.Type != old.SQLType || original.Versions[0].Values[0].Type() != old.SQLType {
		t.Fatal("retained immutable roots changed")
	}
}

func TestDomainIdentityDropStage(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.d AS integer"))
	before := e.CatalogRead()
	old := domainUnitType(t, before, "d")
	canceled := domainUnitStage(t, e, op, "ALTER DOMAIN public.d RENAME TO canceled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled identity stage", err)
	}
	e.DiscardCatalogStage(canceled)
	if e.CatalogRead().State() != before.State() {
		t.Fatal("canceled stage published")
	}
	stale := domainUnitStage(t, e, op, "ALTER DOMAIN public.d RENAME TO stale")
	prepared, err := e.ValidateCatalogStage(context.Background(), stale)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER DOMAIN public.d RENAME TO winner"))
	if _, err := publishCatalog(e, prepared, PublicationToken{Epoch: before.Epoch()}); err == nil {
		t.Fatal("stale stage published")
	}
	if current := e.CatalogRead().MustType(old.Address()); current.Name() != "winner" || current.Array != old.Array {
		t.Fatal("conflict changed current identity", current)
	}
	// A canceled retirement must keep domain and automatic array together.
	drop := domainUnitStage(t, e, op, "DROP DOMAIN public.winner")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	if err := scope.Gate(statement(t, "DROP DOMAIN public.winner")); err != nil {
		t.Fatal(err)
	}
	if err := stageDomainDependents(scope, drop, parser.DropRestrict); err != nil {
		t.Fatal(err)
	}
	if drop.RemovalCount() != 2 {
		t.Fatal("retirement did not stage domain and array closure", drop.RemovalCount())
	}
	if _, err := e.ValidateCatalogStage(ctx, drop); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled retirement", err)
	}
	e.DiscardCatalogStage(drop)
	if _, ok := e.CatalogRead().Type(old.Address()); !ok {
		t.Fatal("canceled retirement removed domain")
	}
	if _, ok := e.CatalogRead().Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)}); !ok {
		t.Fatal("canceled retirement removed companion")
	}
}
