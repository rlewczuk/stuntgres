package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func queryCreateDomain(t *testing.T, e *Engine) *catalog.Type {
	t.Helper()
	e.mu.Lock()
	oid, err := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	typ, err := sqltype.Domain(sqltype.ID(oid), "query_domain", "query_domain", testUsage(e, sqltype.Int4))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := &catalog.Type{SQLType: typ, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err = stage.Create(descriptor); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return descriptor
}

func TestQueryTableCreateDescriptor(t *testing.T) {
	e := New()
	domain := queryCreateDomain(t, e)
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	sized := testUsage(e, sqltype.VarChar)
	sized.Typmod = 8
	sized.Collation.ID = 950
	schema := plan.QuerySchema{{Name: "old", Usage: sized, Nullable: false, NullabilityKnown: true, OriginTable: 19000, OriginAttribute: 3}, {Name: "d", Usage: domain.SQLType.Usage(), Nullable: false, NullabilityKnown: true, OriginTable: 19000, OriginAttribute: 4}}
	expressions := make([]expr.Expr, len(schema))
	for i, column := range schema {
		usage := column.Usage
		expressions[i] = queryProbe(usage, func(*exec.Context) (sqltype.Value, error) { return sqltype.Null(usage.Type), nil })
	}
	query := &plan.Query{Root: &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: expressions}}
	syntax := &parser.CreateQueryTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "copy"}}}, Columns: []parser.Identifier{{Text: "renamed"}}, Query: &parser.Select{}, NoData: true}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var stage *CatalogStage
	var d *catalog.Relation
	var skipped bool
	if err == nil {
		stage, d, skipped, err = e.prepareQueryTableLocked(op, auth, syntax, analyze.Binding{Query: query})
	}
	e.mu.Unlock()
	if err != nil || skipped {
		t.Fatal(err, skipped)
	}
	defer e.DiscardCatalogStage(stage)
	if len(d.Columns) != 2 || d.Columns[0].Name != "renamed" || d.Columns[1].Name != "d" || d.Columns[0].Usage != sized || d.Columns[1].Usage != domain.SQLType.Usage() {
		t.Fatal(d.Columns)
	}
	if &d.Columns[0] == &schema[0] || schema[0].Name != "old" || schema[0].Nullable || schema[0].OriginTable != 19000 {
		t.Fatal("source metadata mutated", schema)
	}
	for i, column := range d.Columns {
		if !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(d.Address().ObjectID) || column.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[column.Name] != i {
			t.Fatal(column, d.Attributes, d.Index)
		}
	}
	if len(d.Constraints.NotNull)+len(d.Constraints.Check)+len(d.Constraints.Unique)+len(d.ColumnMeta.Defaults)+len(d.ColumnMeta.Generations)+len(d.ColumnMeta.Identities)+len(d.Security.ColumnACL) != 0 || !reflect.DeepEqual(d.Security.ACL, authorization.ACLValue{}) || d.Owner != auth.State.CurrentUser {
		t.Fatal("source table decorations inherited", d)
	}
	queryCreatePublish(t, e, stage)
	if len(e.storage.Relations[d.Address()].Versions) != 0 {
		t.Fatal("descriptor-only phase demanded rows")
	}
	// The actual dependency closure records the destination's stable attribute.
	drop := e.BeginCatalogStage(e.CatalogRead())
	drop.domainTargets = []ids.ObjectAddress{domain.Address()}
	edges, err := domainDependencyClosure(context.Background(), drop, drop.domainTargets)
	if err != nil || len(edges) != 1 || edges[0].Target != domain.Address() || edges[0].Column != "d" || edges[0].Address != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: d.Address().ObjectID, SubID: 2}) {
		t.Fatal("stored domain dependency lost", edges, err)
	}
}

func TestQueryTableCreateDescriptorDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, code, message string
		labels              []parser.Identifier
		conflict            bool
	}{
		{"duplicate", "42701", `column "x" specified more than once`, []parser.Identifier{{Text: "x"}, {Text: "x"}}, false},
		{"system", "42701", `column name "ctid" conflicts with a system column name`, []parser.Identifier{{Text: "ctid"}}, false},
		{"arity", "42601", "too many column names were specified", []parser.Identifier{{Text: "a"}, {Text: "b"}, {Text: "c"}}, false},
		{"collation", "42P22", `no collation was derived for column "x" with collatable type text`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			usage := testUsage(e, sqltype.Text)
			info := expr.Info{Usage: usage, CollationConflict: tc.conflict}
			project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "x", Usage: usage}, {Name: "y", Usage: usage}}, Expressions: []expr.Expr{plan.LiteralExpr{Info: info, Value: sqltype.Null(usage.Type)}, plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: sqltype.Null(usage.Type)}}}
			syntax := &parser.CreateQueryTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "copy"}}}, Columns: tc.labels, Query: &parser.Select{}}
			registry := queryCreateTypes(e)
			e.mu.Lock()
			auth, authErr := e.authorizationLocked(op)
			var stage *CatalogStage
			if authErr == nil {
				stage, _, _, err = e.prepareQueryTableLocked(op, auth, syntax, analyze.Binding{Query: &plan.Query{Root: project}})
			} else {
				err = authErr
			}
			e.mu.Unlock()
			var diagnostic *sqlstate.Error
			if stage != nil || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != "" {
				t.Fatal(stage, err)
			}
			wantHint := ""
			if tc.conflict {
				wantHint = "Use the COLLATE clause to set the collation explicitly."
			}
			if diagnostic.Hint != wantHint {
				t.Fatal(diagnostic.Hint)
			}
			queryCreateAssertTypes(t, e, registry)
		})
	}
}

func TestQueryTableCreateTypeGenerationFence(t *testing.T) {
	e := New()
	domain := queryCreateDomain(t, e)
	stage, d := queryCreateStage(t, e, "copy", 0)
	stage.domainBases = []catalog.Descriptor{domain}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	next := *domain
	next.ObjectRevision++
	peer := e.BeginCatalogStage(e.CatalogRead())
	if err = peer.Replace(&next); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, peer)
	_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal(err)
	}
	queryCreateAssertAbsent(t, e, d)
}

func TestQueryTableCreateStructuralOutputWrappers(t *testing.T) {
	e := New()
	tree := testUsage(e, sqltype.PgNodeTree).Type
	domain, err := sqltype.Domain(61001, "tree_domain", "tree_domain", tree.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(61002, "_tree_domain", domain)
	if err != nil {
		t.Fatal(err)
	}
	outer, err := sqltype.Domain(61003, "tree_array_domain", "tree_array_domain", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{tree, domain, array, outer} {
		err := ValidateQueryTableOutputType(typ)
		var got *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "query table output type pg_node_tree is unsupported"}
		if !errors.As(err, &got) || *got != *want {
			t.Fatal("structural wrapper crossed physical output boundary", typ.Name(), err)
		}
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Char, sqltype.Name, sqltype.Regclass, sqltype.Xid} {
		if err := ValidateQueryTableOutputType(testUsage(e, id).Type); err != nil {
			t.Fatal("ordinary stored output rejected", id, err)
		}
	}
}

func TestQueryTableCreateEnumArrayOutputBoundary(t *testing.T) {
	e := New()
	mood, err := sqltype.Enum(61101, "mood", "public.mood", []string{"z", "a", "m"})
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(61102, "_mood", mood)
	if err != nil {
		t.Fatal(err)
	}
	domain := func(id sqltype.ID, name string, base *sqltype.Descriptor) *sqltype.Descriptor {
		t.Helper()
		typ, err := sqltype.Domain(id, name, name, base.Usage())
		if err != nil {
			t.Fatal(err)
		}
		return typ
	}
	arrayOf := func(id sqltype.ID, name string, element *sqltype.Descriptor) *sqltype.Descriptor {
		t.Helper()
		typ, err := sqltype.Array(id, name, element)
		if err != nil {
			t.Fatal(err)
		}
		return typ
	}
	arrayDomain := domain(61103, "mood_array_domain", array)
	nested := arrayOf(61104, "_mood_array_domain", arrayDomain)
	outer := domain(61105, "nested_domain", nested)
	for _, typ := range []*sqltype.Descriptor{array, arrayDomain, nested, outer} {
		err := ValidateQueryTableOutputType(typ)
		var got *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported array element type"}
		if !errors.As(err, &got) || *got != *want {
			t.Fatal("enum array crossed query-derived storage boundary", typ.Name(), err)
		}
	}
	moodDomain := domain(61106, "mood_domain", mood)
	projection := arrayOf(61107, "_mood_domain", moodDomain)
	projectionDomain := domain(61108, "projection_domain", projection)
	integerArray := arrayOf(61109, "_integer", testUsage(e, sqltype.Int4).Type)
	for _, typ := range []*sqltype.Descriptor{mood, moodDomain, projection, projectionDomain, integerArray} {
		if err := ValidateQueryTableOutputType(typ); err != nil {
			t.Fatal("supported query-derived output rejected", typ.Name(), err)
		}
	}
}

func TestQueryTableCreateStructuralDescriptorAdmission(t *testing.T) {
	for _, noData := range []bool{false, true} {
		e := New()
		op, done, err := testOperation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(done)
		usage := testUsage(e, sqltype.PgNodeTree)
		calls := 0
		probe := queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
			calls++
			return sqltype.Null(usage.Type), nil
		})
		project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "tree", Usage: usage}}, Expressions: []expr.Expr{probe}}
		syntax := &parser.CreateQueryTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "copy"}}}, Query: &parser.Select{}, NoData: noData}
		registry := queryCreateTypes(e)
		beforeRow, beforeVersion, beforeFrontier := e.storage.RowFrontier(), e.storage.VersionFrontier(), e.visibility.CommitSeq()
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		var stage *CatalogStage
		if err == nil {
			stage, _, _, err = e.prepareQueryTableLocked(op, auth, syntax, analyze.Binding{Query: &plan.Query{Root: project}})
		}
		e.mu.Unlock()
		var got *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "query table output type pg_node_tree is unsupported"}
		if stage != nil || !errors.As(err, &got) || *got != *want || calls != 0 {
			done()
			t.Fatal("structural DATA/NO DATA admission reached runtime or stage", noData, stage, err, calls)
		}
		queryCreateAssertTypes(t, e, registry)
		if e.storage.RowFrontier() != beforeRow || e.storage.VersionFrontier() != beforeVersion || e.visibility.CommitSeq() != beforeFrontier {
			done()
			t.Fatal("structural admission reserved rows/frontier")
		}
		existing, d := queryCreateStage(t, e, "copy", 1)
		queryCreatePublish(t, e, existing)
		syntax.IfNotExists = true
		e.mu.Lock()
		auth, err = e.authorizationLocked(op)
		var skipped bool
		if err == nil {
			stage, _, skipped, err = e.prepareQueryTableLocked(op, auth, syntax, analyze.Binding{Query: &plan.Query{Root: project}})
		}
		e.mu.Unlock()
		if err != nil || stage != nil || !skipped || calls != 0 || e.CatalogRead().Get(d.Address()) != d {
			done()
			t.Fatal("existing-target skip reached structural validator/runtime", noData, stage, skipped, err, calls)
		}
		done()
	}
}
