package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowGroupsEndpoints(t *testing.T) {
	// Each pair is independently specified head/tail for groups [0,1,2]
	// having complete row spans [0,2),[2,3),[3,5).
	for _, tc := range []struct {
		kind   parser.FrameBoundKind
		offset int64
		want   [3][2]int
	}{
		{parser.FrameUnboundedPreceding, 0, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameUnboundedFollowing, 0, [3][2]int{{5, 5}, {5, 5}, {5, 5}}},
		{parser.FrameCurrentRow, 0, [3][2]int{{0, 2}, {2, 3}, {3, 5}}},
		{parser.FrameOffsetPreceding, 0, [3][2]int{{0, 2}, {2, 3}, {3, 5}}},
		{parser.FrameOffsetPreceding, 1, [3][2]int{{0, 0}, {0, 2}, {2, 3}}},
		{parser.FrameOffsetPreceding, 2, [3][2]int{{0, 0}, {0, 0}, {0, 2}}},
		{parser.FrameOffsetPreceding, math.MaxInt64, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameOffsetFollowing, 0, [3][2]int{{0, 2}, {2, 3}, {3, 5}}},
		{parser.FrameOffsetFollowing, 1, [3][2]int{{2, 3}, {3, 5}, {5, 5}}},
		{parser.FrameOffsetFollowing, 2, [3][2]int{{3, 5}, {5, 5}, {5, 5}}},
		{parser.FrameOffsetFollowing, math.MaxInt64, [3][2]int{{5, 5}, {5, 5}, {5, 5}}},
	} {
		for group, want := range tc.want {
			for side, upper := range []bool{false, true} {
				got, err := exec.WindowGroupsEndpoint(context.Background(), tc.kind, tc.offset, []int{2, 3, 5}, group, upper)
				if err != nil || got != want[side] {
					t.Fatal(tc, group, upper, got, err)
				}
			}
		}
	}
	usage := sqltype.Builtins()[0].Usage()
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int8 {
			usage = typ.Usage()
		}
	}
	value, err := exec.WindowFrameCount(context.Background(), usage, 3, 2)
	n, integerErr := value.Integer()
	if err != nil || integerErr != nil || n != 0 {
		t.Fatal(value, err, integerErr)
	}
}

func TestQueryWindowGroupsRemap(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER w FROM t WINDOW w AS(ORDER BY id GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING)")
	defer source.Close()
	original := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	before := original.Specs[0].Frame
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := stored.RemapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil || mapped == query.Root {
		t.Fatal(mapped, err)
	}
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if next == original || next.Specs[0].Frame.Unit != parser.FrameGroups || next.Specs[0].Definition != original.Specs[0].Definition || !reflect.DeepEqual(next.SortIndices, original.SortIndices) || next.Calls[0].Expression == original.Calls[0].Expression || next.Calls[0].Expression.Slot != original.Calls[0].Expression.Slot {
		t.Fatal("graph/identity ownership", next, original)
	}
	if next.Specs[0].Order[0].Expression.ResultUsage().Type != types[sqltype.Int4] || original.Specs[0].Order[0].Expression.ResultUsage().Type == types[sqltype.Int4] {
		t.Fatal("key descriptor ownership")
	}
	oldSource := original.Child.(*plan.SourceNode)
	newSource := next.Child.(*plan.SourceNode)
	if oldSource == newSource || newSource.Ref.Address.ObjectID != oldSource.Ref.Address.ObjectID+100 || oldSource.Ref != source.Ref {
		t.Fatal("source identity remap", oldSource.Ref, newSource.Ref)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for _, pair := range [][2]plan.WindowBound{{before.Start, next.Specs[0].Frame.Start}, {before.End, next.Specs[0].Frame.End}} {
		old, bound := pair[0], pair[1]
		if bound.Kind != old.Kind || bound.Offset == old.Offset || bound.Offset.ResultUsage().Type != types[sqltype.Int8] || old.Offset.ResultUsage().Type == types[sqltype.Int8] || bound.Support != (sqltype.RangeSupport{}) || old.Support != (sqltype.RangeSupport{}) {
			t.Fatal("bigint offset ownership/support", old, bound)
		}
		value, err := bound.Offset.Eval(&evaluation)
		n, integerErr := value.Integer()
		if err != nil || integerErr != nil || n != 1 || value.Type() != types[sqltype.Int8] {
			t.Fatal(value, err, integerErr)
		}
	}
	if original.Specs[0].Frame != before {
		t.Fatal("remap mutated original frame")
	}
}

func TestQueryWindowGroupsIdentity(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER w,count(*) OVER w,count(*) OVER v,count(*) OVER(ORDER BY id RANGE 1 PRECEDING) FROM t WINDOW w AS(ORDER BY id GROUPS 1 PRECEDING),v AS(ORDER BY id GROUPS 1 PRECEDING)")
	defer source.Close()
	project := query.Root.(*plan.ProjectNode)
	original := project.Child.(*plan.WindowNode)
	if len(original.Specs) != 3 || len(original.Calls) != 3 || original.Specs[0].Definition != 1 || original.Specs[1].Definition != 2 || original.Specs[2].Frame.Unit != parser.FrameRange || !reflect.DeepEqual(original.SortIndices, []int{0, 0, 0}) || !reflect.DeepEqual(original.SpecIndices, []int{0, 1, 2}) {
		t.Fatal("unit/named identity/shared sort", original)
	}
	a, b, c := project.Expressions[0].(*plan.WindowExpr), project.Expressions[1].(*plan.WindowExpr), project.Expressions[2].(*plan.WindowExpr)
	if a.Slot != b.Slot || a.Slot == c.Slot {
		t.Fatal("named slots", a, b, c)
	}
	// The query mapper calls its callback on whole expressions. GROUPS 1 has
	// an assignment-coercion wrapper, so the callback owns child recursion.
	var replaceLiteral func(expr.Expr) (expr.Expr, bool, error)
	replaceLiteral = func(expr expr.Expr) (expr.Expr, bool, error) {
		switch e := expr.(type) {
		case plan.LiteralExpr:
			value, err := sqltype.Integer(e.ResultUsage().Type, 2)
			e.Value = value
			return e, true, err
		case plan.CoercionExpr:
			child, changed, err := replaceLiteral(e.Child)
			if changed {
				e.Child = child
			}
			return e, changed, err
		default:
			return expr, false, nil
		}
	}
	copied, err := plan.RewriteQueryExpressions(query.Root, replaceLiteral)
	if err != nil || copied == query.Root {
		t.Fatal(copied, err)
	}
	next := copied.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if next == original || !reflect.DeepEqual(next.SortIndices, original.SortIndices) {
		t.Fatal("clone sort ownership", next)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for i, spec := range original.Specs {
		if next.Specs[i].Definition != spec.Definition || next.Specs[i].Frame.Unit != spec.Frame.Unit {
			t.Fatal("lost frame identity")
		}
		old, err := spec.Frame.Start.Offset.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		value, err := next.Specs[i].Frame.Start.Offset.Eval(&evaluation)
		oldN, _ := old.Integer()
		newN, _ := value.Integer()
		if err != nil || oldN != 1 || newN != 2 {
			t.Fatal("rewrite mutated original", old, value, err)
		}
	}
	// Inline specs must also distinguish units when their key/bound SQL matches.
	_, inline, inlineSource := queryCoreFixture(t, "SELECT count(*) OVER(ORDER BY id GROUPS 1 PRECEDING),count(*) OVER(ORDER BY id RANGE 1 PRECEDING) FROM t")
	defer inlineSource.Close()
	n := inline.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if len(n.Specs) != 2 || !reflect.DeepEqual(n.SortIndices, []int{0, 0}) {
		t.Fatal(n)
	}
}

func TestQueryWindowGroupsCancellation(t *testing.T) {
	for _, phase := range []string{"key", "prepare", "hash", "equal", "sort", "peer", "groups-offset", "groups-validation", "groups-frame", "count", "output", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			q := "SELECT id,row_number() OVER(PARTITION BY v ORDER BY id),count(*) OVER(PARTITION BY v ORDER BY id GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING) FROM t"
			_, query, source := queryCoreFixture(t, q)
			continuation, err := exec.CaptureSource(context.Background(), source.View, source.Ref)
			if err != nil {
				t.Fatal(err)
			}
			defer continuation.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &windowCancelContext{Context: ctx, cancel: cancel, phase: phase}
			rows, count, err := exec.Finalize(probe, query.Root, source, nil)
			if !probe.entered || !errors.Is(err, context.Canceled) || rows != nil || count != 0 || source.View != nil {
				t.Fatal(probe.entered, rows, count, err, source.View)
			}
			source.Close()
			source.Close()
			recovered, n, err := exec.Finalize(context.Background(), query.Root, continuation, nil)
			if err != nil || recovered == nil || n != 4 {
				t.Fatal(recovered, n, err)
			}
			recovered.Close()
			recovered.Close()
		})
	}
}

func TestQueryWindowGroupsOffsetFailureRelease(t *testing.T) {
	for _, frame := range []string{"BETWEEN -1 PRECEDING AND NULL FOLLOWING", "BETWEEN NULL PRECEDING AND -1 FOLLOWING"} {
		_, query, source := queryCoreFixture(t, "SELECT count(*) OVER(ORDER BY v GROUPS "+frame+") FROM t")
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
		code, message := "22013", "frame starting offset must not be negative"
		if frame == "BETWEEN NULL PRECEDING AND -1 FOLLOWING" {
			code, message = "22004", "frame starting offset must not be null"
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
		var diagnostic *sqlstate.Error
		if rows != nil || count != 0 || source.View != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(rows, count, err, source.View)
		}
		source.Close()
		source.Close()
	}
}

func TestQueryWindowGroupsMetadata(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id,count(*) OVER(ORDER BY v GROUPS 1 PRECEDING) AS n FROM t")
	defer source.Close()
	schema := query.Root.Schema()
	if schema[0].OriginTable == 0 || schema[0].OriginAttribute != 1 {
		t.Fatal(schema)
	}
	column := schema[1]
	if column.Usage.Type.ID() != sqltype.Int8 || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Nullable || !column.NullabilityKnown {
		t.Fatal(column)
	}
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || rows == nil || count != 4 {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	tuple := make([]sqltype.Value, 2)
	for i, want := range []int64{2, 2, 3, 2} {
		present, err := rows.Next(context.Background(), tuple)
		id, _ := tuple[0].Integer()
		n, _ := tuple[1].Integer()
		if !present || err != nil || id != int64(i+1) || n != want {
			t.Fatal(present, err, id, n)
		}
	}
	rows.Close()
	rows.Close()
	if source.View != nil || source.WindowValues != nil || source.WindowIndices != nil || source.Input != nil || source.Scratch != nil {
		t.Fatal("overlay/pin resources survived close")
	}
}

func TestQueryWindowGroupsFinishAtomicity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE s(id INT)")
	run(t, e, "INSERT INTO s VALUES(1),(2),(3)")
	run(t, e, "CREATE TABLE d(n BIGINT)")
	run(t, e, "INSERT INTO d VALUES(99)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "INSERT INTO d SELECT count(*) OVER(ORDER BY id GROUPS 1 PRECEDING) FROM s"), 0, nil)
	if err != nil || result.Count != 3 {
		_ = scope.Finish(err)
		t.Fatal(result, err)
	}
	cancel()
	if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !scope.Finished || scope.View != nil || scope.LockTicket != nil {
		t.Fatal("statement resources survived Finish")
	}
	current := run(t, e, "SELECT n FROM d").Rows
	defer current.Close()
	tuple := make([]sqltype.Value, 1)
	present, err := current.Next(context.Background(), tuple)
	value, _ := tuple[0].Integer()
	if !present || err != nil || value != 99 {
		t.Fatal(present, err, value)
	}
	if present, err := current.Next(context.Background(), tuple); present || err != nil {
		t.Fatal(present, err)
	}
	run(t, e, "INSERT INTO d SELECT count(*) OVER(ORDER BY id GROUPS 1 PRECEDING) FROM s")
}

func TestQueryWindowGroupsDomainRemap(t *testing.T) {
	e := New()
	run(t, e, "CREATE DOMAIN groups_key AS INT")
	run(t, e, "CREATE DOMAIN groups_offset AS BIGINT")
	run(t, e, "CREATE TABLE t(id INT)")
	ast := statement(t, "SELECT count(*) OVER w FROM t WINDOW w AS(ORDER BY id::groups_key GROUPS 1::groups_offset PRECEDING)")
	ref := roleTableRef(t, e, "t")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	description, err := Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.Query.Root
	original := root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	key := original.Specs[0].Order[0].Expression.ResultUsage().Type
	var offsetDomain *sqltype.Descriptor
	plan.WalkExpression(original.Specs[0].Frame.Start.Offset, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			offsetDomain = expr.ResultUsage().Type
		}
	})
	if !key.IsDomain() || offsetDomain == nil {
		t.Fatal("missing analyzed domains", key, offsetDomain)
	}
	for _, old := range []*sqltype.Descriptor{key, offsetDomain} {
		next, err := sqltype.Domain(old.ID(), old.DiagnosticName(), old.SQLName(), types[old.DomainBase().Type.ID()].Usage())
		if err != nil {
			t.Fatal(err)
		}
		types[old.ID()] = next
	}
	lookup := func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] }
	mapped, err := stored.RemapViewQuery(New().CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if next.Specs[0].Frame.Unit != parser.FrameGroups || next.Specs[0].Definition != 1 || next.Specs[0].Frame.Start.Support != (sqltype.RangeSupport{}) || next.Specs[0].Order[0].Expression.ResultUsage().Type != types[key.ID()] || original.Specs[0].Order[0].Expression.ResultUsage().Type != key {
		t.Fatal("domain/unit ownership", next)
	}
	var mappedDomain *sqltype.Descriptor
	plan.WalkExpression(next.Specs[0].Frame.Start.Offset, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			mappedDomain = expr.ResultUsage().Type
		}
	})
	if mappedDomain != types[offsetDomain.ID()] || mappedDomain == offsetDomain || !analyze.QueryDescriptorsChanged(root, lookup) || analyze.QueryDescriptorsChanged(mapped, lookup) {
		t.Fatal("offset domain remap retained/mutated descriptors", mappedDomain, offsetDomain)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	value, err := next.Specs[0].Frame.Start.Offset.Eval(&evaluation)
	n, integerErr := value.Integer()
	if err != nil || integerErr != nil || n != 1 || value.Type() != types[sqltype.Int8] {
		t.Fatal(value, err, integerErr)
	}
}

// Bind real GROUPS source manifests and check private column ACLs without
// executing the public SQL witness duplicated by the external reference family.
func TestQueryWindowGroupsColumnAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	table := domainUnitTable(t, e, op, "CREATE TABLE public.groups_acl(id INT,k INT)")
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), table.Address())
	reader := e.RoleStore.Domain.Principal(1000000)
	roles := cloneRoleFixture(e.RoleStore.Published)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "groups_unit_reader"})

	e.RoleStore.PublishLocked(roles)
	column := func(index int) ids.ObjectRef {
		object := ref
		object.Address.SubID = table.Attributes[index]
		return object
	}
	grant := func(object ids.ObjectRef, rights authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: table.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: rights}}}
		if err := e.SetObjectACL(context.Background(), object, acl); err != nil {
			t.Fatal(err)
		}
	}
	grant(ref, 0)
	for _, tc := range []struct {
		query  string
		reads  []int
		denied bool
	}{
		{"SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) FROM groups_acl WHERE FALSE", []int{1}, true},
		{"SELECT id FROM groups_acl WINDOW unused AS(ORDER BY k GROUPS CURRENT ROW)", []int{0, 1}, true},
		{"SELECT count(*) OVER(ORDER BY 1 GROUPS (SELECT k FROM groups_acl) PRECEDING)", []int{1}, true},
		{"SELECT 1 WINDOW unused AS(ORDER BY 1 GROUPS (SELECT k FROM groups_acl) PRECEDING)", []int{1}, true},
		{"SELECT count(*) OVER(ORDER BY 1 GROUPS CURRENT ROW) FROM groups_acl LIMIT 0", nil, false},
		{"SELECT count(*) OVER(ORDER BY id GROUPS 1 PRECEDING) FROM groups_acl", []int{0}, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			for i := range table.Columns {
				rights := authorization.Privileges(0)
				if i == 0 {
					rights = authorization.SelectPrivilege
				}
				grant(column(i), rights)
			}
			auth := catalogAuthorization(e, e.CatalogRead())
			b := NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
			b.Query.Auth = &auth
			_, err := b.BindSelect(statement(t, tc.query).(*parser.Select), false)
			if err != nil || len(b.Query.Sources) != 1 {
				t.Fatal(err, b.Query.Sources)
			}
			reads := b.Query.Sources[0].Uses
			if len(reads) != len(tc.reads) {
				t.Fatal("source manifest width", reads, tc.reads)
			}
			for _, want := range tc.reads {
				found := false
				for _, got := range reads {
					found = found || got == want
				}
				if !found {
					t.Fatal("missing actual key/offset read", reads, want)
				}
			}
			check := func(denied bool) {
				t.Helper()
				current := catalogAuthorization(e, e.CatalogRead())
				current.State.CurrentUser = reader
				err := analyze.CheckQuerySources(current, b.Query.Sources)
				if !denied {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var got *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table groups_acl"}
				if !errors.As(err, &got) || *got != want {
					t.Fatal(got, err, want)
				}
			}
			check(tc.denied)
			for _, i := range tc.reads {
				grant(column(i), authorization.SelectPrivilege)
			}
			check(false)
			if len(tc.reads) > 0 {
				grant(column(tc.reads[0]), 0)
			} else {
				grant(column(0), 0)
			}
			check(true)
		})
	}
}
