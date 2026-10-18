package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
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
				got, err := windowGroupsEndpoint(context.Background(), tc.kind, tc.offset, []int{2, 3, 5}, group, upper)
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
	value, err := windowFrameCount(context.Background(), usage, 3, 2)
	n, integerErr := value.Integer()
	if err != nil || integerErr != nil || n != 0 {
		t.Fatal(value, err, integerErr)
	}
}

func TestQueryWindowGroupsRemap(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER w FROM t WINDOW w AS(ORDER BY id GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING)")
	defer source.close()
	original := query.Root.(*projectNode).child.(*windowNode)
	before := original.specs[0].frame
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := remapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID += 100; return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil || mapped == query.Root {
		t.Fatal(mapped, err)
	}
	next := mapped.(*projectNode).child.(*windowNode)
	if next == original || next.specs[0].frame.unit != parser.FrameGroups || next.specs[0].definition != original.specs[0].definition || !reflect.DeepEqual(next.sortIndices, original.sortIndices) || next.calls[0].expression == original.calls[0].expression || next.calls[0].expression.slot != original.calls[0].expression.slot {
		t.Fatal("graph/identity ownership", next, original)
	}
	if next.specs[0].order[0].expression.ResultUsage().Type != types[sqltype.Int4] || original.specs[0].order[0].expression.ResultUsage().Type == types[sqltype.Int4] {
		t.Fatal("key descriptor ownership")
	}
	oldSource := original.child.(*sourceNode)
	newSource := next.child.(*sourceNode)
	if oldSource == newSource || newSource.ref.Address.ObjectID != oldSource.ref.Address.ObjectID+100 || oldSource.ref != source.ref {
		t.Fatal("source identity remap", oldSource.ref, newSource.ref)
	}
	evaluation := EvalContext{Context: context.Background()}
	for _, pair := range [][2]boundWindowBound{{before.start, next.specs[0].frame.start}, {before.end, next.specs[0].frame.end}} {
		old, bound := pair[0], pair[1]
		if bound.kind != old.kind || bound.offset == old.offset || bound.offset.ResultUsage().Type != types[sqltype.Int8] || old.offset.ResultUsage().Type == types[sqltype.Int8] || bound.support != (sqltype.RangeSupport{}) || old.support != (sqltype.RangeSupport{}) {
			t.Fatal("bigint offset ownership/support", old, bound)
		}
		value, err := bound.offset.Eval(&evaluation)
		n, integerErr := value.Integer()
		if err != nil || integerErr != nil || n != 1 || value.Type() != types[sqltype.Int8] {
			t.Fatal(value, err, integerErr)
		}
	}
	if original.specs[0].frame != before {
		t.Fatal("remap mutated original frame")
	}
}

func TestQueryWindowGroupsIdentity(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER w,count(*) OVER w,count(*) OVER v,count(*) OVER(ORDER BY id RANGE 1 PRECEDING) FROM t WINDOW w AS(ORDER BY id GROUPS 1 PRECEDING),v AS(ORDER BY id GROUPS 1 PRECEDING)")
	defer source.close()
	project := query.Root.(*projectNode)
	original := project.child.(*windowNode)
	if len(original.specs) != 3 || len(original.calls) != 3 || original.specs[0].definition != 1 || original.specs[1].definition != 2 || original.specs[2].frame.unit != parser.FrameRange || !reflect.DeepEqual(original.sortIndices, []int{0, 0, 0}) || !reflect.DeepEqual(original.specIndices, []int{0, 1, 2}) {
		t.Fatal("unit/named identity/shared sort", original)
	}
	a, b, c := project.expressions[0].(*windowExpression), project.expressions[1].(*windowExpression), project.expressions[2].(*windowExpression)
	if a.slot != b.slot || a.slot == c.slot {
		t.Fatal("named slots", a, b, c)
	}
	// The query mapper calls its callback on whole expressions. GROUPS 1 has
	// an assignment-coercion wrapper, so the callback owns child recursion.
	var replaceLiteral func(expr.Expr) (expr.Expr, bool, error)
	replaceLiteral = func(expr expr.Expr) (expr.Expr, bool, error) {
		switch e := expr.(type) {
		case literalExpression:
			value, err := sqltype.Integer(e.ResultUsage().Type, 2)
			e.value = value
			return e, true, err
		case coercionExpression:
			child, changed, err := replaceLiteral(e.child)
			if changed {
				e.child = child
			}
			return e, changed, err
		default:
			return expr, false, nil
		}
	}
	copied, err := rewriteQueryExpressions(query.Root, replaceLiteral)
	if err != nil || copied == query.Root {
		t.Fatal(copied, err)
	}
	next := copied.(*projectNode).child.(*windowNode)
	if next == original || !reflect.DeepEqual(next.sortIndices, original.sortIndices) {
		t.Fatal("clone sort ownership", next)
	}
	evaluation := EvalContext{Context: context.Background()}
	for i, spec := range original.specs {
		if next.specs[i].definition != spec.definition || next.specs[i].frame.unit != spec.frame.unit {
			t.Fatal("lost frame identity")
		}
		old, err := spec.frame.start.offset.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		value, err := next.specs[i].frame.start.offset.Eval(&evaluation)
		oldN, _ := old.Integer()
		newN, _ := value.Integer()
		if err != nil || oldN != 1 || newN != 2 {
			t.Fatal("rewrite mutated original", old, value, err)
		}
	}
	// Inline specs must also distinguish units when their key/bound SQL matches.
	_, inline, inlineSource := queryCoreFixture(t, "SELECT count(*) OVER(ORDER BY id GROUPS 1 PRECEDING),count(*) OVER(ORDER BY id RANGE 1 PRECEDING) FROM t")
	defer inlineSource.close()
	n := inline.Root.(*projectNode).child.(*windowNode)
	if len(n.specs) != 2 || !reflect.DeepEqual(n.sortIndices, []int{0, 0}) {
		t.Fatal(n)
	}
}

func TestQueryWindowGroupsCancellation(t *testing.T) {
	for _, phase := range []string{"key", "prepare", "hash", "equal", "sort", "peer", "groups-offset", "groups-validation", "groups-frame", "count", "output", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			q := "SELECT id,row_number() OVER(PARTITION BY v ORDER BY id),count(*) OVER(PARTITION BY v ORDER BY id GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING) FROM t"
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

func TestQueryWindowGroupsOffsetFailureRelease(t *testing.T) {
	for _, frame := range []string{"BETWEEN -1 PRECEDING AND NULL FOLLOWING", "BETWEEN NULL PRECEDING AND -1 FOLLOWING"} {
		_, query, source := queryCoreFixture(t, "SELECT count(*) OVER(ORDER BY v GROUPS "+frame+") FROM t")
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

func TestQueryWindowGroupsMetadata(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id,count(*) OVER(ORDER BY v GROUPS 1 PRECEDING) AS n FROM t")
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
	if source.view != nil || source.windowValues != nil || source.windowIndices != nil || source.input != nil || source.scratch != nil {
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
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	description, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := description.query.Root
	original := root.(*projectNode).child.(*windowNode)
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	key := original.specs[0].order[0].expression.ResultUsage().Type
	var offsetDomain *sqltype.Descriptor
	walkBoundExpression(original.specs[0].frame.start.offset, func(expr expr.Expr) {
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
	next := mapped.(*projectNode).child.(*windowNode)
	if next.specs[0].frame.unit != parser.FrameGroups || next.specs[0].definition != 1 || next.specs[0].frame.start.support != (sqltype.RangeSupport{}) || next.specs[0].order[0].expression.ResultUsage().Type != types[key.ID()] || original.specs[0].order[0].expression.ResultUsage().Type != key {
		t.Fatal("domain/unit ownership", next)
	}
	var mappedDomain *sqltype.Descriptor
	walkBoundExpression(next.specs[0].frame.start.offset, func(expr expr.Expr) {
		if expr.ResultUsage().Type.IsDomain() {
			mappedDomain = expr.ResultUsage().Type
		}
	})
	if mappedDomain != types[offsetDomain.ID()] || mappedDomain == offsetDomain || !queryDescriptorsChanged(root, lookup) || queryDescriptorsChanged(mapped, lookup) {
		t.Fatal("offset domain remap retained/mutated descriptors", mappedDomain, offsetDomain)
	}
	evaluation := EvalContext{Context: context.Background()}
	value, err := next.specs[0].frame.start.offset.Eval(&evaluation)
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
	ref := e.catalog.domain.Ref(e.catalog.state.epoch, table.address)
	reader := e.authorizationDomain.Principal(1000000)
	roles := cloneRoleFixture(e.roles)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "groups_unit_reader"})

	e.roles = roles
	column := func(index int) ids.ObjectRef {
		object := ref
		object.Address.SubID = table.attributes[index]
		return object
	}
	grant := func(object ids.ObjectRef, rights authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: table.owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: rights}}}
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
			for i := range table.columns {
				rights := authorization.Privileges(0)
				if i == 0 {
					rights = authorization.SelectPrivilege
				}
				grant(column(i), rights)
			}
			auth := catalogAuthorization(e, e.CatalogRead())
			b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
			b.queryAuth = &auth
			_, err := b.bindSelect(statement(t, tc.query).(*parser.Select), false)
			if err != nil || len(b.querySources) != 1 {
				t.Fatal(err, b.querySources)
			}
			reads := b.querySources[0].uses
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
				current.state.CurrentUser = reader
				err := current.checkQuerySources(b.querySources)
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
