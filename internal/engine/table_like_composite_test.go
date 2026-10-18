package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
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
	source := e.CatalogRead().state.descriptors[sourceRef.Address].(*relationDescriptor)
	typeRef := ObjectRef{owner: e.domain, Epoch: e.CatalogRead().Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(source.rowType)}}
	sourceType := e.CatalogRead().state.descriptors[typeRef.Address].(*typeDescriptor)
	if sourceType.owningRelation != source.address {
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
		replacement := *current.state.descriptors[typeRef.Address].(*typeDescriptor)
		replacement.acl = acl
		replacement.aclSyntheticDefault = false
		replacement.revision++
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
	source = e.CatalogRead().state.descriptors[sourceRef.Address].(*relationDescriptor)
	sourceType = e.CatalogRead().state.descriptors[typeRef.Address].(*typeDescriptor)
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(LIKE source_type INCLUDING ALL)")
	if err != nil {
		e.DiscardCatalogStage(stage)
		t.Fatal("type USAGE did not independently authorize source", err)
	}
	defer e.DiscardCatalogStage(stage)
	if destination.owner != reader.Ref || destination.acl.Explicit || len(destination.columnACL) != 0 || destination.relationKind != tableRelation || destination.address == source.address || destination.rowType == source.rowType {
		t.Fatal("composite authority/identity copied", destination)
	}
	if len(destination.defaults) != 0 || len(destination.generations) != 0 || len(destination.identities) != 0 || len(destination.checkConstraints) != 0 || len(destination.uniqueConstraints) != 0 {
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
	for _, column := range destination.columns {
		if !column.Nullable || !column.NullabilityKnown {
			t.Fatal("composite nullability changed", column)
		}
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	setTypeACL(denied)
	if e.CatalogRead().state.descriptors[source.address] != source {
		t.Fatal("type-only replacement changed backing source fixture")
	}
	_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
	var conflict CatalogConflict
	if !errors.As(err, &conflict) {
		t.Fatal("changed reciprocal type authority was not fenced", err)
	}
	if e.CatalogRead().state.descriptors[destination.address] != nil || e.storage[destination.address] != nil {
		t.Fatal("type-incarnation conflict published destination fragments")
	}
}
