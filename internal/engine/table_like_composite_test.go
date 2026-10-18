package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTableLikeCompositeSourceACL(t *testing.T) {
	e := New()
	run(t, e, "CREATE USER composite_reader")
	run(t, e, "CREATE TYPE source_type AS (a INT,b TEXT)")
	run(t, e, "CREATE SCHEMA private_composite")
	run(t, e, "CREATE TYPE private_composite.source_type AS (a INT)")
	sourceRef := roleTableRef(t, e, "source_type")
	source := e.CatalogRead().MustRelation(sourceRef.Address)
	typeRef := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)})
	sourceType := e.CatalogRead().MustType(typeRef.Address)
	if sourceType.OwningRelation != source.Address() {
		t.Fatal("composite fixture lacks reciprocal type authority")
	}
	reader, _ := e.Roles().Lookup("composite_reader")
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	denied := authorization.ACLValue{Explicit: true}
	grant := func(privilege authorization.Privileges) authorization.ACLValue {
		return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: privilege}}}
	}
	setTypeACL := func(acl authorization.ACLValue) {
		t.Helper()
		// Type ACL fixtures use the existing immutable descriptor publication
		// seam, as domain/enum authorization tests do; no public setter expands.
		current := e.CatalogRead()
		replacement := *current.MustType(typeRef.Address)
		replacement.ACL = acl
		replacement.ACLSyntheticDefault = false
		replacement.ObjectRevision++
		stage := e.BeginCatalogStage(current)
		if err := stage.Replace(&replacement); err != nil {
			t.Fatal(err)
		}
		domainUnitPublish(t, e, stage)
	}
	setTypeACL(denied)
	op, done := roleOperation(t, e, "composite_reader")
	defer done()
	assertDenied := func(query string, want sqlstate.Error) {
		t.Helper()
		if _, err := e.Describe(op, statement(t, query)); err != nil {
			t.Fatal("semantic utility rejected at Prepare", err)
		}
		stage, _, err := tableLikeBuild(t, e, op, query)
		e.DiscardCatalogStage(stage)
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatal("composite source authorization diagnostic", err, got, want)
		}
	}
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for type source_type"}
	assertDenied("CREATE TABLE clone (LIKE source_type INCLUDING ALL)", want)
	column := sourceRef
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, grant(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(context.Background(), sourceRef, grant(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	assertDenied("CREATE TABLE clone (LIKE source_type INCLUDING ALL)", want)
	assertDenied("CREATE TABLE clone (LIKE private_composite.source_type INCLUDING ALL)", sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema private_composite", Position: "26"})
	if err := e.SetObjectACL(context.Background(), sourceRef, denied); err != nil {
		t.Fatal(err)
	}
	setTypeACL(grant(authorization.UsagePrivilege))
	source = e.CatalogRead().MustRelation(sourceRef.Address)
	sourceType = e.CatalogRead().MustType(typeRef.Address)
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(LIKE source_type INCLUDING ALL)")
	if err != nil {
		e.DiscardCatalogStage(stage)
		t.Fatal("type USAGE did not independently authorize source", err)
	}
	defer e.DiscardCatalogStage(stage)
	if destination.Owner != reader.Ref || destination.ACL.Explicit || len(destination.ColumnACL) != 0 || destination.RelationKind != tableRelation || destination.Address() == source.Address() || destination.RowType == source.RowType {
		t.Fatal("composite authority/identity copied", destination)
	}
	if len(destination.Defaults) != 0 || len(destination.Generations) != 0 || len(destination.Identities) != 0 || len(destination.CheckConstraints) != 0 || len(destination.UniqueConstraints) != 0 {
		t.Fatal("inapplicable composite providers invented")
	}
	capturedRelation, capturedType := false, false
	for _, descriptor := range stage.likeSources {
		capturedRelation = capturedRelation || descriptor == source
	}
	for _, descriptor := range stage.domainBases {
		capturedType = capturedType || descriptor == sourceType
	}
	if !capturedRelation || !capturedType {
		t.Fatal("reciprocal composite source captures missing", capturedRelation, capturedType)
	}
	for _, column := range destination.Columns {
		if !column.Nullable || !column.NullabilityKnown {
			t.Fatal("composite nullability changed", column)
		}
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	setTypeACL(denied)
	if e.CatalogRead().Get(source.Address()) != source {
		t.Fatal("type-only replacement changed backing source fixture")
	}
	_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
	var conflict CatalogConflict
	if !errors.As(err, &conflict) {
		t.Fatal("changed reciprocal type authority was not fenced", err)
	}
	if e.CatalogRead().Get(destination.Address()) != nil || e.storage[destination.Address()] != nil {
		t.Fatal("type-incarnation conflict published destination fragments")
	}
}
