package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryWindowRowsEndpoints(t *testing.T) {
	for _, tc := range []struct {
		kind   parser.FrameBoundKind
		offset int64
		want   [3][2]int
	}{
		{parser.FrameUnboundedPreceding, 0, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameUnboundedFollowing, 0, [3][2]int{{3, 3}, {3, 3}, {3, 3}}},
		{parser.FrameCurrentRow, 0, [3][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{parser.FrameOffsetPreceding, 0, [3][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{parser.FrameOffsetPreceding, 1, [3][2]int{{0, 0}, {0, 1}, {1, 2}}},
		{parser.FrameOffsetPreceding, 2, [3][2]int{{0, 0}, {0, 0}, {0, 1}}},
		{parser.FrameOffsetPreceding, 100, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameOffsetPreceding, math.MaxInt64, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameOffsetFollowing, 0, [3][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{parser.FrameOffsetFollowing, 1, [3][2]int{{1, 2}, {2, 3}, {3, 3}}},
		{parser.FrameOffsetFollowing, 2, [3][2]int{{2, 3}, {3, 3}, {3, 3}}},
		{parser.FrameOffsetFollowing, 100, [3][2]int{{3, 3}, {3, 3}, {3, 3}}},
		{parser.FrameOffsetFollowing, math.MaxInt64, [3][2]int{{3, 3}, {3, 3}, {3, 3}}},
	} {
		for row, want := range tc.want {
			for side, upper := range []bool{false, true} {
				got, err := windowRowsEndpoint(context.Background(), tc.kind, tc.offset, row, 3, upper)
				if err != nil || got != want[side] {
					t.Fatal(tc, row, upper, got, err)
				}
			}
		}
	}
	for _, tc := range []struct {
		kind   parser.FrameBoundKind
		offset int64
		want   [2]int
	}{
		{parser.FrameCurrentRow, 0, [2]int{0, 1}},
		{parser.FrameOffsetPreceding, math.MaxInt64, [2]int{0, 0}},
		{parser.FrameOffsetFollowing, math.MaxInt64, [2]int{1, 1}},
	} {
		for side, upper := range []bool{false, true} {
			got, err := windowRowsEndpoint(context.Background(), tc.kind, tc.offset, 0, 1, upper)
			if err != nil || got != tc.want[side] {
				t.Fatal(tc, got, err)
			}
		}
	}
	var usage sqltype.Usage
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int8 {
			usage = typ.Usage()
		}
	}
	v, err := windowFrameCount(context.Background(), usage, 3, 2)
	n, integerErr := v.Integer()
	if err != nil || integerErr != nil || n != 0 {
		t.Fatal(v, err, integerErr)
	}
}

func TestQueryWindowRowsRemap(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER w FROM t WINDOW w AS(ORDER BY id ROWS BETWEEN 1 PRECEDING AND 1 FOLLOWING)")
	defer source.close()
	original := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	before := original.Specs[0].Frame
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := remapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil || mapped == query.Root {
		t.Fatal(mapped, err)
	}
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if next == original || next.Specs[0].Frame.Unit != parser.FrameRows || next.Specs[0].Definition != original.Specs[0].Definition || !reflect.DeepEqual(next.SortIndices, original.SortIndices) || next.Calls[0].Expression == original.Calls[0].Expression || next.Calls[0].Expression.Slot != original.Calls[0].Expression.Slot {
		t.Fatal("graph/identity ownership", next, original)
	}
	if next.Specs[0].Order[0].Expression.ResultUsage().Type != types[sqltype.Int4] || original.Specs[0].Order[0].Expression.ResultUsage().Type == types[sqltype.Int4] {
		t.Fatal("key descriptor ownership")
	}
	oldSource := original.Child.(*plan.SourceNode)
	newSource := next.Child.(*plan.SourceNode)
	if oldSource == newSource || newSource.Ref.Address.ObjectID != oldSource.Ref.Address.ObjectID+100 || oldSource.Ref != source.ref {
		t.Fatal("source identity remap", oldSource.Ref, newSource.Ref)
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background()}}
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

func TestQueryWindowRowsIdentity(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER w,count(*) OVER w,count(*) OVER v,count(*) OVER(ORDER BY id RANGE 1 PRECEDING) FROM t WINDOW w AS(ORDER BY id ROWS 1 PRECEDING),v AS(ORDER BY id ROWS 1 PRECEDING)")
	defer source.close()
	project := query.Root.(*plan.ProjectNode)
	original := project.Child.(*plan.WindowNode)
	if len(original.Specs) != 3 || len(original.Calls) != 3 || original.Specs[0].Definition != 1 || original.Specs[1].Definition != 2 || original.Specs[2].Frame.Unit != parser.FrameRange || !reflect.DeepEqual(original.SortIndices, []int{0, 0, 0}) || !reflect.DeepEqual(original.SpecIndices, []int{0, 1, 2}) {
		t.Fatal("unit/named identity/shared sort", original)
	}
	a, b, c := project.Expressions[0].(*plan.WindowExpr), project.Expressions[1].(*plan.WindowExpr), project.Expressions[2].(*plan.WindowExpr)
	if a.Slot != b.Slot || a.Slot == c.Slot {
		t.Fatal("named slots", a, b, c)
	}
	// The query mapper calls its callback on whole expressions. ROWS 1 has
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
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background()}}
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
	_, inline, inlineSource := queryCoreFixture(t, "SELECT count(*) OVER(ORDER BY id ROWS 1 PRECEDING),count(*) OVER(ORDER BY id RANGE 1 PRECEDING) FROM t")
	defer inlineSource.close()
	n := inline.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if len(n.Specs) != 2 || !reflect.DeepEqual(n.SortIndices, []int{0, 0}) {
		t.Fatal(n)
	}
}

func TestQueryWindowRowsCancellation(t *testing.T) {
	for _, phase := range []string{"key", "prepare", "hash", "equal", "sort", "rows-offset", "rows-validation", "rows-frame", "count", "output", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			q := "SELECT id,row_number() OVER(PARTITION BY v ORDER BY id),count(*) OVER(PARTITION BY v ORDER BY id ROWS BETWEEN 1 PRECEDING AND 1 FOLLOWING) FROM t"
			_, query, source := queryCoreFixture(t, q)
			continuation, err := captureQuerySource(context.Background(), source.view, source.ref)
			if err != nil {
				t.Fatal(err)
			}
			defer continuation.close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &windowCancelContext{Context: ctx, cancel: cancel, phase: phase}
			rows, count, err := finalizeQuery(probe, query.Root, source, nil)
			if !probe.entered || !errors.Is(err, context.Canceled) || rows != nil || count != 0 || source.view != nil {
				t.Fatal(probe.entered, rows, count, err, source.view)
			}
			source.close()
			source.close()
			recovered, n, err := finalizeQuery(context.Background(), query.Root, continuation, nil)
			if err != nil || recovered == nil || n != 4 {
				t.Fatal(recovered, n, err)
			}
			recovered.Close()
			recovered.Close()
		})
	}
}

func TestQueryWindowRowsOffsetFailureRelease(t *testing.T) {
	for _, frame := range []string{"BETWEEN -1 PRECEDING AND NULL FOLLOWING", "BETWEEN NULL PRECEDING AND -1 FOLLOWING"} {
		_, query, source := queryCoreFixture(t, "SELECT count(*) OVER(ORDER BY v ROWS "+frame+") FROM t")
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		code, message := "22013", "frame starting offset must not be negative"
		if frame == "BETWEEN NULL PRECEDING AND -1 FOLLOWING" {
			code, message = "22004", "frame starting offset must not be null"
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
		var diagnostic *sqlstate.Error
		if rows != nil || count != 0 || source.view != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(rows, count, err, source.view)
		}
		source.close()
		source.close()
	}
}

func TestQueryWindowRowsMetadata(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id,count(*) OVER(ORDER BY id ROWS 1 PRECEDING) AS n FROM t")
	defer source.close()
	schema := query.Root.Schema()
	if schema[0].OriginTable == 0 || schema[0].OriginAttribute != 1 {
		t.Fatal(schema)
	}
	column := schema[1]
	if column.Usage.Type.ID() != sqltype.Int8 || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Nullable || !column.NullabilityKnown {
		t.Fatal(column)
	}
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil || rows == nil || count != 4 {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	tuple := make([]sqltype.Value, 2)
	for i, want := range []int64{1, 2, 2, 2} {
		present, err := rows.Next(context.Background(), tuple)
		id, _ := tuple[0].Integer()
		n, _ := tuple[1].Integer()
		if !present || err != nil || id != int64(i+1) || n != want {
			t.Fatal(present, err, id, n)
		}
	}
	rows.Close()
	rows.Close()
	if source.view != nil || source.windowValues != nil || source.windowIndices != nil || source.input != nil || source.scratch != nil {
		t.Fatal("overlay/pin resources survived close")
	}
}

func TestQueryWindowRowsFinishAtomicity(t *testing.T) {
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
	result, err := scope.Execute(statement(t, "INSERT INTO d SELECT count(*) OVER(ORDER BY id ROWS 1 PRECEDING) FROM s"), 0, nil)
	if err != nil || result.Count != 3 {
		_ = scope.Finish(err)
		t.Fatal(result, err)
	}
	cancel()
	if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !scope.finished || scope.view != nil || scope.ticket != nil {
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
	run(t, e, "INSERT INTO d SELECT count(*) OVER(ORDER BY id ROWS 1 PRECEDING) FROM s")
}

func TestQueryWindowRowsDomainRemap(t *testing.T) {
	e := New()
	run(t, e, "CREATE DOMAIN rows_key AS INT")
	run(t, e, "CREATE DOMAIN rows_offset AS BIGINT")
	run(t, e, "CREATE TABLE t(id INT)")
	ast := statement(t, "SELECT count(*) OVER w FROM t WINDOW w AS(ORDER BY id::rows_key ROWS 1::rows_offset PRECEDING)")
	ref := roleTableRef(t, e, "t")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	description, err := describe(op.ctx, e.analysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.query.Root
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
	mapped, err := remapViewQuery(New().CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if next.Specs[0].Frame.Unit != parser.FrameRows || next.Specs[0].Definition != 1 || next.Specs[0].Frame.Start.Support != (sqltype.RangeSupport{}) || next.Specs[0].Order[0].Expression.ResultUsage().Type != types[key.ID()] || original.Specs[0].Order[0].Expression.ResultUsage().Type != key {
		t.Fatal("domain/unit ownership", next)
	}
	var mappedDomain *sqltype.Descriptor
	plan.WalkExpression(next.Specs[0].Frame.Start.Offset, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			mappedDomain = expr.ResultUsage().Type
		}
	})
	if mappedDomain != types[offsetDomain.ID()] || mappedDomain == offsetDomain || !queryDescriptorsChanged(root, lookup) || queryDescriptorsChanged(mapped, lookup) {
		t.Fatal("offset domain remap retained/mutated descriptors", mappedDomain, offsetDomain)
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background()}}
	value, err := next.Specs[0].Frame.Start.Offset.Eval(&evaluation)
	n, integerErr := value.Integer()
	if err != nil || integerErr != nil || n != 1 || value.Type() != types[sqltype.Int8] {
		t.Fatal(value, err, integerErr)
	}
}

// Bind real ROWS source manifests and check private column ACLs without
// executing the public SQL witness duplicated by the external reference family.
func TestQueryWindowRowsColumnAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	table := domainUnitTable(t, e, op, "CREATE TABLE public.rows_acl(id INT,k INT)")
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), table.Address())
	reader := e.roles.domain.Principal(1000000)
	roles := cloneRoleFixture(e.roles.published)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "rows_unit_reader"})

	e.roles.published = roles
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
		{"SELECT count(*) OVER(ORDER BY k ROWS 1 PRECEDING) FROM rows_acl WHERE FALSE", []int{1}, true},
		{"SELECT id FROM rows_acl WINDOW unused AS(ORDER BY k ROWS CURRENT ROW)", []int{0, 1}, true},
		{"SELECT count(*) OVER(ORDER BY 1 ROWS (SELECT k FROM rows_acl) PRECEDING)", []int{1}, true},
		{"SELECT 1 WINDOW unused AS(ORDER BY 1 ROWS (SELECT k FROM rows_acl) PRECEDING)", []int{1}, true},
		{"SELECT count(*) OVER(ORDER BY 1 ROWS CURRENT ROW) FROM rows_acl LIMIT 0", nil, false},
		{"SELECT count(*) OVER(ORDER BY id ROWS 1 PRECEDING) FROM rows_acl", []int{0}, false},
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
			b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
			b.query.auth = &auth
			_, err := b.bindSelect(statement(t, tc.query).(*parser.Select), false)
			if err != nil || len(b.query.sources) != 1 {
				t.Fatal(err, b.query.sources)
			}
			reads := b.query.sources[0].uses
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
				err := current.checkQuerySources(b.query.sources)
				if !denied {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var got *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table rows_acl"}
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
