package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/publication"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The fixture installs real descriptor roots, not SQL commands or fake payloads.
func publicationOwnerFixture(t *testing.T) (*core.Engine, *catalog.Relation, *catalog.Publication, *catalog.PublicationRelation) {
	t.Helper()
	e := engine.New()
	v := e.CatalogRead()
	state := catalog.CloneState(v.State())
	typ, _ := v.LookupType(sqltype.Int4)
	r := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 910001}, ObjectRevision: 1, ObjectName: "publication_owner", ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Owner: e.RoleStore.Domain.Principal(10), RelationKind: catalog.TableRelation, TableMethod: 2, Columns: []catalog.Column{{Name: "a", Usage: typ.Usage()}, {Name: "b", Usage: typ.Usage()}}, Attributes: []int32{1, 2}, Index: map[string]int{"a": 0, "b": 1}}
	p := &catalog.Publication{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationClassID, ObjectID: 910002}, ObjectRevision: 1, ObjectName: "publication_owner_p", Owner: r.Owner, Insert: true, GeneratedColumns: 'n'}
	m := &catalog.PublicationRelation{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationRelationClassID, ObjectID: 910003}, ObjectRevision: 1, Publication: p.Address(), Relation: r.Address()}
	for _, d := range []catalog.Descriptor{r, p, m} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.Mu.Lock()
	e.Catalog.State = state
	e.Mu.Unlock()
	return e, r, p, m
}

func TestPublicationDescriptorAuthority(t *testing.T) {
	e, r, p, m := publicationOwnerFixture(t)
	foreign := engine.New()
	v := e.CatalogRead()
	if err := (catalog.PublicationProvider{}).Validate(v, p); err != nil {
		t.Fatal(err)
	}
	if err := (catalog.PublicationRelationProvider{}).Validate(v, m); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*catalog.Publication){func(n *catalog.Publication) { n.Owner = foreign.RoleStore.Domain.Principal(10) }, func(n *catalog.Publication) { n.ObjectRevision = 0 }, func(n *catalog.Publication) { n.GeneratedColumns = 'x' }, func(n *catalog.Publication) { n.ObjectName = "" }} {
		n := *p
		mutate(&n)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(n.Address(), &n)
		state.BindName(catalog.DescriptorNameKey(&n), n.Address())
		if err := (catalog.PublicationProvider{}).Validate(v.WithState(state), &n); err == nil {
			t.Fatal("forged publication accepted")
		}
	}
	for _, mutate := range []func(*catalog.PublicationRelation){func(n *catalog.PublicationRelation) { n.Relation.ObjectID++ }, func(n *catalog.PublicationRelation) { n.ColumnsSpecified = true; n.SetColumns([]int32{2, 1}) }, func(n *catalog.PublicationRelation) { n.ColumnsSpecified = true; n.SetColumns([]int32{1, 1}) }, func(n *catalog.PublicationRelation) { n.ColumnsSpecified = true; n.SetColumns([]int32{3}) }, func(n *catalog.PublicationRelation) { n.ObjectRevision = 0 }} {
		n := *m
		mutate(&n)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(n.Address(), &n)
		state.BindName(catalog.DescriptorNameKey(&n), n.Address())
		if err := (catalog.PublicationRelationProvider{}).Validate(v.WithState(state), &n); err == nil {
			t.Fatal("forged membership accepted")
		}
	}
	columns := []int32{1, 2}
	n := *m
	n.ColumnsSpecified = true
	n.SetColumns(columns)
	columns[0] = 99
	dependencies := []ids.ObjectAddress{{ClassID: ids.RelationClassID, ObjectID: r.Address().ObjectID, SubID: 1}}
	n.SetFilterDependencies(dependencies)
	dependencies[0].SubID = 99
	detached := n.FilterDependencies()
	detached[0].SubID = 98
	if !slices.Equal(n.Columns, []int32{1, 2}) || n.FilterDependencies()[0].SubID != 1 || m.ColumnsSpecified || m.Columns != nil {
		t.Fatal("descriptor borrowed caller backing")
	}
	state := catalog.CloneState(v.State())
	state.RemoveDescriptor(m.Address())
	count := 0
	for range v.WithState(state).PublicationRelations() {
		count++
	}
	if count != 0 {
		t.Fatal("retired mapping survived index")
	}
	if v.Get(m.Address()) != m || v.Get(p.Address()) != p || &r.Attributes[0] != &v.MustRelation(r.Address()).Attributes[0] {
		t.Fatal("old immutable root changed")
	}
	if err := (catalog.PublicationProvider{}).Validate(v, p); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationFilterTruth(t *testing.T) {
	e, r, _, m := publicationOwnerFixture(t)
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"a > 0", "CASE WHEN a IS NULL THEN NULL::boolean ELSE COALESCE(a,0)>0 END"} {
		raw := enginetest.Statement(t, "SELECT "+text).(*parser.Select).Targets[0]
		e.Mu.Lock()
		condition, err := core.BindRetainedPredicate(e, op, auth, r, raw, sqltype.InputEnvironment{}, "PUBLICATION WHERE", "WHERE")
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		refs, err := publication.ValidateFilter(context.Background(), auth.Catalog, condition, raw.SourcePosition())
		if err != nil {
			t.Fatal(err)
		}
		copy := *m
		copy.Filter = condition
		copy.SetFilterDependencies(refs)
		state := catalog.CloneState(auth.Catalog.State())
		state.SetDescriptor(copy.Address(), &copy)
		v := auth.Catalog.WithState(state)
		if err = (catalog.PublicationRelationProvider{}).Validate(v, &copy); err != nil {
			t.Fatal(err)
		}
		saved := condition.Presentation()
		savedRoot := saved.Query().Root
		savedDependencies := saved.Dependencies()
		typ := r.Columns[0].Usage.Type
		for i, want := range []plan.Truth{plan.True, plan.False, plan.Unknown} {
			value := sqltype.Null(typ)
			if i < 2 {
				value, err = sqltype.Integer(typ, int64(1-i))
				if err != nil {
					t.Fatal(err)
				}
			}
			runtime := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: v}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{value, sqltype.Null(typ)}}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: savedDependencies}}
			result, e := condition.Expression().Eval(runtime)
			if e != nil {
				t.Fatal(e)
			}
			truth, e := plan.TruthOf(result)
			if e != nil || truth != want {
				t.Fatalf("%s case%d truth=%v err=%v want=%v", text, i, truth, e, want)
			}
			if (truth == plan.True) != (i == 0) {
				t.Fatal("non-TRUE publication predicate matched")
			}
		}
		next := catalog.CloneState(state)
		renamed := *r
		renamed.ObjectName = "publication_current"
		renamed.ObjectRevision++
		next.SetDescriptor(r.Address(), &renamed)
		sql, err := publication.FilterSQL(context.Background(), v.WithState(next), &copy)
		if err != nil || sql == "" {
			t.Fatal("current-name normal deparse", sql, err)
		}
		if condition.Presentation() != saved || condition.Presentation().Query().Root != savedRoot || len(savedDependencies) > 0 && &saved.Dependencies()[0] != &savedDependencies[0] || stored.PolicyOf(copy.Filter) != condition {
			t.Fatal("retained filter backing replaced")
		}
	}
}

func TestPublicationCaptureLifetime(t *testing.T) {
	e, _, p, _ := publicationOwnerFixture(t)
	produce := publicationProducer(t)
	var saved [][]sqltype.Value
	publicationInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows, err := produce(in)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		saved = rows
		next := catalog.CloneState(in.Catalog.State())
		n := *p
		n.ObjectName = "publication_changed"
		n.ObjectRevision++
		next.UnbindName(catalog.DescriptorNameKey(p))
		next.SetDescriptor(n.Address(), &n)
		next.BindName(catalog.DescriptorNameKey(&n), n.Address())
		e.Catalog.State = next
		retained, err := produce(in)
		if err != nil || len(retained) != 1 {
			t.Fatal(retained, err)
		}
		text, _ := retained[0][1].Text()
		if text != p.Name() {
			t.Fatal("capture rebound to current definition", text)
		}
		bad := in
		bad.Catalog = in.Catalog.WithState(next)
		if _, err = produce(bad); err == nil {
			t.Fatal("unpaired root admitted")
		}
	})
	publicationInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows, err := produce(in)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		text, _ := rows[0][1].Text()
		if text != "publication_changed" {
			t.Fatal("new capture stale", text)
		}
	})
	text, _ := saved[0][1].Text()
	if text != p.Name() {
		t.Fatal("retained value backing changed")
	}
}

func TestPublicationRejectedPublication(t *testing.T) {
	e, _, p, _ := publicationOwnerFixture(t)
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	n := *p
	n.ObjectRevision++
	n.Insert = false
	if err := stage.Replace(&n); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel() // Actual validated candidate, before the final publication fence.
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled admitted candidate", err)
	}
	if e.CatalogRead().State() != before.State() || before.Get(p.Address()) != p || !p.Insert {
		t.Fatal("rejected candidate changed accepted roots")
	}
}

func TestPublicationAnonymousRecordFilter(t *testing.T) {
	e, r, _, _ := publicationOwnerFixture(t)
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	raw := enginetest.Statement(t, "SELECT (a,b) IS NOT NULL").(*parser.Select).Targets[0]
	e.Mu.Lock()
	condition, err := core.BindRetainedPredicate(e, op, auth, r, raw, sqltype.InputEnvironment{}, "PUBLICATION WHERE", "WHERE")
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publication.ValidateFilter(context.Background(), auth.Catalog, condition, raw.SourcePosition()); err != nil {
		t.Fatal("normal anonymous record rejected", err)
	}
	typ := r.Columns[0].Usage.Type
	value, err := sqltype.Integer(typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		row  []sqltype.Value
		want plan.Truth
	}{
		{[]sqltype.Value{value, value}, plan.True},
		{[]sqltype.Value{value, sqltype.Null(typ)}, plan.False},
		{[]sqltype.Value{sqltype.Null(typ), sqltype.Null(typ)}, plan.False},
	} {
		saved := condition.Presentation()
		runtime := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: test.row}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: saved.Dependencies()}}
		result, err := condition.Expression().Eval(runtime)
		if err != nil {
			t.Fatal(err)
		}
		truth, err := plan.TruthOf(result)
		if err != nil || truth != test.want {
			t.Fatal("normal row NULL predicate semantics", truth, test.want, err)
		}
	}
}
