package engine

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Observe real operations on the window stack and invoke an ordinary CancelFunc.
// This never substitutes a provider, adds a production hook, or depends on timing.
type windowCancelContext struct {
	context.Context
	cancel            context.CancelFunc
	phase             string
	entered, computed bool
}

func (c *windowCancelContext) Err() error {
	if !c.entered {
		var pcs [48]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		window, merge, compare, hash, equal, prepare, key, advance, finalize, output, publication := false, false, false, false, false, false, false, false, false, false, false
		offset, inrange, groups, physical, offsetEval := false, false, false, false, false
		caller := ""
		for {
			frame, more := frames.Next()
			name := frame.Function
			if caller == "" {
				caller = name
			}
			window = window || strings.HasSuffix(name, ".openWindowNode")
			merge = merge || strings.Contains(name, ".mergeQueryIndices")
			compare = compare || strings.HasSuffix(name, ".Comparison.Compare")
			hash = hash || strings.HasSuffix(name, ".Grouping.Hash")
			equal = equal || strings.HasSuffix(name, ".Grouping.Equal")
			prepare = prepare || strings.HasSuffix(name, ".Grouping.PrepareKey")
			key = key || strings.Contains(name, "columnExpression.Eval")
			advance = advance || strings.HasSuffix(name, ".windowFrameEndpoint")
			offset = offset || strings.HasSuffix(name, ".evaluateWindowOffsets")
			inrange = inrange || strings.HasSuffix(name, ".RangeSupport.InRange")
			groups = groups || strings.HasSuffix(name, ".windowGroupsEndpoint")
			physical = physical || strings.HasSuffix(name, ".windowRowsEndpoint")
			offsetEval = offsetEval || strings.Contains(name, "literalExpression.Eval")
			finalize = finalize || strings.HasSuffix(name, ".windowFrameCount")
			output = output || strings.Contains(name, "windowExpression).Eval")
			publication = publication || strings.HasSuffix(name, ".finalizeQuery")
			if !more {
				break
			}
		}
		c.computed = c.computed || window && finalize
		hit := false
		switch c.phase {
		case "key":
			hit = window && key
		case "prepare":
			hit = window && prepare
		case "hash":
			hit = window && hash
		case "equal":
			hit = window && equal
		case "sort":
			hit = window && compare && merge
		case "peer":
			hit = window && compare && !merge
		case "frame":
			hit = window && advance
		case "offset":
			hit = window && offset
		case "inrange":
			hit = window && inrange
		case "groups-offset", "rows-offset":
			hit = window && offset && offsetEval
		case "groups-validation", "rows-validation":
			// Child Eval/coercion may also check context after the literal has
			// returned. Only the direct offset-validator checkpoint qualifies.
			hit = window && strings.HasSuffix(caller, ".evaluateWindowOffsets")
		case "groups-frame":
			hit = window && groups
		case "rows-frame":
			hit = window && physical
		case "count":
			hit = window && finalize
		case "output":
			hit = output && c.computed
		case "prepublication":
			hit = publication && !window && !output && c.computed
		}
		if hit {
			c.entered = true
			c.cancel()
		}
	}
	return c.Context.Err()
}
func TestQueryWindowActualPhaseCancellation(t *testing.T) {
	for _, phase := range []string{"key", "prepare", "hash", "equal", "sort", "peer", "offset", "inrange", "frame", "count", "output", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			q := "SELECT id,row_number() OVER(PARTITION BY v ORDER BY id),count(*) OVER(PARTITION BY v ORDER BY id) FROM t"
			if phase == "peer" {
				q = "SELECT count(*) OVER(ORDER BY v) FROM t"
			}
			if phase == "offset" || phase == "inrange" {
				q = "SELECT count(*) OVER(ORDER BY id RANGE BETWEEN 1 PRECEDING AND 1 FOLLOWING) FROM t"
			}
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
			cancel()
			source.close()
			source.close()
			recovered, recoveredCount, recoveryErr := finalizeQuery(context.Background(), query.Root, continuation, nil)
			if recoveryErr != nil || recovered == nil || recoveredCount != 4 {
				t.Fatal(recovered, recoveredCount, recoveryErr)
			}
			recovered.Close()
		})
	}
}
func TestQueryWindowMetadataAndOverlayOwnership(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT id,row_number() OVER(ORDER BY id) AS rn,count(*) OVER(ORDER BY v) AS n FROM t ORDER BY id")
	schema := query.Root.Schema()
	if schema[0].OriginTable == 0 || schema[0].OriginAttribute != 1 {
		t.Fatal(schema)
	}
	for _, column := range schema[1:] {
		if column.Usage.Type.ID() != sqltype.Int8 || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Nullable || !column.NullabilityKnown {
			t.Fatal(column)
		}
	}
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil || count != 4 || source.view != nil {
		t.Fatal(rows, count, err, source.view)
	}
	defer rows.Close()
	rows.Close()
	rows.Close()
	if rows.Schema()[1].Name != "rn" {
		t.Fatal(rows.Schema())
	}
}
func TestQueryWindowLateKeyFailureAndCountOverflow(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*) OVER(ORDER BY 1/(id-3)) FROM t LIMIT 1")
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	var diagnostic *sqlstate.Error
	if rows != nil || count != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" || source.view != nil {
		t.Fatal(rows, count, err, source.view)
	}

}
func TestQueryWindowColumnAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER window_reader")
	roleExecute(t, e, op, "CREATE TABLE window_secret(v INT,k INT)")
	ref := roleTableRef(t, e, "window_secret")
	reader, _ := e.Roles().Lookup("window_reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION window_reader")
	for _, q := range []string{"SELECT row_number() OVER() FROM window_secret", "SELECT count(*) OVER() FROM window_secret WHERE FALSE"} {
		roleError(t, e, op, q, "42501", "permission denied for table window_secret", "")
	}
	column := ref
	column.Address.SubID = 1
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	if err := e.SetObjectACL(context.Background(), column, grant); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT row_number() OVER() FROM window_secret", "SELECT count(*) OVER(PARTITION BY v) FROM window_secret WHERE FALSE"} {
		roleExecute(t, e, op, q).Rows.Close()
	}
	for _, q := range []string{"SELECT row_number() OVER(ORDER BY k) FROM window_secret", "SELECT v FROM window_secret ORDER BY count(*) OVER(PARTITION BY k)", "SELECT v FROM window_secret WINDOW unused AS(ORDER BY k)"} {
		roleError(t, e, op, q, "42501", "permission denied for table window_secret", "")
	}
	ast := statement(t, "SELECT row_number() OVER(),count(*) OVER() FROM window_secret")
	held, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleError(t, e, op, "SELECT count(*) OVER() FROM window_secret", "42501", "permission denied for table window_secret", "")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &held
	result, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table window_secret"}
	if result.Rows != nil || result.Count != 0 || !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(result, err)
	}
}

func TestQueryWindowInsertFinishVeto(t *testing.T) {
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
	result, err := scope.Execute(statement(t, "INSERT INTO d SELECT count(*) OVER(ORDER BY id) FROM s"), 0, nil)
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
	run(t, e, "INSERT INTO d SELECT row_number() OVER() FROM s")
}

func TestQueryWindowBorrowedOverlayAndPayloadOwnership(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY row_number() OVER(ORDER BY id DESC)")
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil || count != 4 || len(rows.Schema()) != 1 {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	run(t, e, "INSERT INTO t VALUES(5,9,FALSE)")
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	tuple := make([]sqltype.Value, 1)
	for _, want := range []int64{4, 3, 2, 1} {
		present, err := rows.Next(context.Background(), tuple)
		id, _ := tuple[0].Integer()
		if !present || err != nil || id != want {
			t.Fatal(present, err, id)
		}
	}
	rows.Close()
	rows.Close()
	if source.view != nil {
		t.Fatal("retained overlay failed to release its physical pin")
	}
	run(t, e, "CREATE TABLE payload(id INT,b BYTEA)")
	run(t, e, `INSERT INTO payload VALUES(1,'\x00ff'),(2,'\xff00')`)
	run(t, e, "CREATE VIEW payload_view AS SELECT id,b,row_number() OVER(ORDER BY id DESC) AS rn FROM payload")
	borrowed := run(t, e, "SELECT b FROM payload_view ORDER BY rn").Rows
	defer borrowed.Close()
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	var retained sqltype.Value
	for i, want := range []string{`\xff00`, `\x00ff`} {
		present, err := borrowed.Next(context.Background(), tuple)
		if !present || err != nil {
			t.Fatal(present, err)
		}
		text, err := tuple[0].Type().Output(context.Background(), tuple[0])
		if err != nil || text != want {
			t.Fatal(text, err, want)
		}
		if i == 0 {
			retained = tuple[0]
		}
	}
	text, err := retained.Type().Output(context.Background(), retained)
	if err != nil || text != `\xff00` {
		t.Fatal("borrowed payload was overwritten", text, err)
	}
}

func TestQueryWindowDefinitionAnalysisPrecedence(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT,v INT)")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"SELECT row_number() OVER w,target_missing FROM t WINDOW w AS(ORDER BY key_missing)",
		"SELECT row_number() OVER(ORDER BY key_missing),target_missing FROM t",
		"SELECT row_number() OVER w FROM t WHERE where_missing=1 WINDOW w AS(ORDER BY key_missing)",
		"SELECT row_number() OVER w FROM t GROUP BY group_missing WINDOW w AS(ORDER BY key_missing)",
		"SELECT row_number() OVER w FROM t HAVING having_missing=1 WINDOW w AS(ORDER BY key_missing)",
		"SELECT row_number() OVER w FROM t WINDOW w AS(ORDER BY key_missing) ORDER BY order_missing",
	} {
		missing := ""
		for _, name := range []string{"target_missing", "where_missing", "group_missing", "having_missing", "order_missing"} {
			if strings.Contains(q, name) {
				missing = name
				break
			}
		}
		_, err := e.Describe(op, statement(t, q))
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "` + missing + `" does not exist`, Position: strconv.Itoa(strings.Index(q, missing) + 1)}
		var got *sqlstate.Error
		if !errors.As(err, &got) || *got != want {
			t.Fatal(q, got, err, want)
		}
	}
	// Repeated raw specs ignore token positions, but genuinely different specs
	// must still reject an ambiguous output alias before resolving unused keys.
	if _, err := e.Describe(op, statement(t, "SELECT row_number() OVER(ORDER BY id) AS rn,row_number() OVER(ORDER BY id) AS rn FROM t ORDER BY rn")); err != nil {
		t.Fatal(err)
	}
	q := "SELECT row_number() OVER(ORDER BY id) AS rn,row_number() OVER(ORDER BY v) AS rn FROM t WINDOW unused AS(ORDER BY key_missing) ORDER BY rn"
	_, err := e.Describe(op, statement(t, q))
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42702", Message: `ORDER BY "rn" is ambiguous`, Position: strconv.Itoa(strings.LastIndex(q, "rn") + 1)}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(got, err, want)
	}
}

func TestQueryWindowReviewDescriptorRemap(t *testing.T) {
	e := New()
	for _, q := range []string{
		"CREATE TYPE window_enum AS ENUM('z','a')",
		"CREATE DOMAIN window_domain AS window_enum",
		"CREATE TABLE enum_source(id INT,e window_domain)",
		"INSERT INTO enum_source VALUES(1,'a'),(2,'z'),(3,'a'),(4,NULL)",
		"CREATE VIEW enum_window AS SELECT id,row_number() OVER(PARTITION BY e ORDER BY id) AS rn,count(*) OVER(ORDER BY e) AS n FROM enum_source",
	} {
		run(t, e, q)
	}
	ref := roleTableRef(t, e, "enum_window")
	definition := e.CatalogRead().MustRelation(ref.Address).view
	original := definition.query.Root.(*projectNode).child.(*windowNode)
	oldEnum := domainUnitType(t, e.CatalogRead(), "window_enum").typ
	oldDomain := domainUnitType(t, e.CatalogRead(), "window_domain").typ
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	fresh := New()
	if err := fresh.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	newEnum := domainUnitType(t, fresh.CatalogRead(), "window_enum").typ
	newDomain := domainUnitType(t, fresh.CatalogRead(), "window_domain").typ
	if oldEnum == newEnum || oldDomain == newDomain || newDomain.BaseType() != newEnum {
		t.Fatal("descriptors not replaced")
	}
	mapped, err := remapViewQuery(New().CatalogRead(), definition.query.Root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == oldEnum {
			return newEnum
		}
		if old == oldDomain {
			return newDomain
		}
		next, ok := fresh.CatalogRead().LookupType(old.ID())
		if !ok {
			t.Fatalf("missing replacement type %v", old.ID())
		}
		return next
	})
	if err != nil || mapped == definition.query.Root {
		t.Fatal(mapped, err)
	}
	next := mapped.(*projectNode).child.(*windowNode)
	value, err := newDomain.Input(context.Background(), "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	grouped, ordered := false, false
	for i, spec := range original.specs {
		for j, key := range spec.partition {
			if key.ResultUsage().Type != oldDomain {
				continue
			}
			grouped = true
			if next.specs[i].partition[j].ResultUsage().Type != newDomain {
				t.Fatal("partition usage not replaced")
			}
			if _, err := next.specs[i].grouping[j].Hash(context.Background(), value); err != nil {
				t.Fatal("remapped grouping retained old owner", err)
			}
			if _, err := spec.grouping[j].Hash(context.Background(), value); err == nil {
				t.Fatal("original grouping accepted replacement owner")
			}
			if key.ResultUsage().Type != oldDomain {
				t.Fatal("original partition mutated")
			}
		}
		for j, key := range spec.order {
			if key.expression.ResultUsage().Type != oldDomain {
				continue
			}
			ordered = true
			if next.specs[i].order[j].expression.ResultUsage().Type != newDomain {
				t.Fatal("ordering usage not replaced")
			}
			if sign, err := next.specs[i].order[j].comparison.Compare(context.Background(), value, value, sqltype.InputEnvironment{}); err != nil || sign != 0 {
				t.Fatal("remapped ordering retained old owner", sign, err)
			}
			if _, err := key.comparison.Compare(context.Background(), value, value, sqltype.InputEnvironment{}); err == nil {
				t.Fatal("original ordering accepted replacement owner")
			}
			if key.expression.ResultUsage().Type != oldDomain {
				t.Fatal("original ordering mutated")
			}
		}
	}
	if !grouped || !ordered {
		t.Fatal("descriptor-changing partition/order witnesses absent", grouped, ordered)
	}
	for i, call := range original.calls {
		if next.calls[i].expression.slot != call.expression.slot || next.calls[i].expression == call.expression {
			t.Fatal("remap lost slot or retained original occurrence")
		}
	}
}

func TestQueryWindowReview3SignatureAnalysisNoRegistration(t *testing.T) {
	for _, tc := range []struct{ call, code string }{
		{"bool_and(true)", ""}, {"bool_and(1)", "42883"},
		{"bool_or(false)", ""}, {"bool_or(1)", "42883"},
		{"every(true)", ""}, {"every(1)", "42883"},
		{"avg(1)", ""}, {"avg(true)", "42883"}, {"avg(NULL)", "42725"},
		{"min(1)", ""}, {"min(true)", "42883"},
		{"max(1)", ""}, {"max(true)", "42883"},
		{"any_value(1)", ""}, {"any_value(1,2)", "42883"}, {"any_value(NULL)", "42804"},
		{"string_agg('x','|')", ""}, {"string_agg(1,2)", "42883"},
		{"sum(1)", ""}, {"sum(true)", "42883"}, {"sum()", "42883"}, {"sum(1,2)", "42883"}, {"sum(NULL)", "42725"},
	} {
		t.Run(tc.call, func(t *testing.T) {
			raw := statement(t, "SELECT "+tc.call+" OVER ()").(*parser.Select).Targets[0].(parser.AggregateExpr)
			seed := statement(t, "SELECT count(*)").(*parser.Select).Targets[0]
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.aggregateClause = "SELECT"
			e.mu.Lock()
			defer e.mu.Unlock()
			if _, err := b.bind(seed, expressionPolicy{context: projectionContext}); err != nil {
				t.Fatal(err)
			}
			before := append([]aggregateCall(nil), b.aggregates...)
			name := parser.ObjectName{Parts: []parser.Identifier{{Text: raw.Name, Pos: raw.Pos}}}
			var argument expr.Expr
			_, err := b.resolveWindowAggregateSignature(raw, name, &argument)
			var diagnostic *sqlstate.Error
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, b.aggregates) || len(b.windowCalls) != 0 || b.argumentOwner != (argumentWindowOwner{}) {
				t.Fatal("signature analysis altered ordinary/provider/window state", before, b.aggregates, b.windowCalls, b.argumentOwner)
			}
		})
	}
}

func TestQueryWindowReview4ArgumentOwnerRestoration(t *testing.T) {
	for _, tc := range []struct{ sql, code string }{
		{"SELECT count(count(row_number() OVER ())) OVER ()", "42803"},
		{"SELECT count(sum(row_number() OVER ())) OVER ()", "42803"},
		{"SELECT count(count(row_number() OVER ()) OVER ())", "42P20"},
		{"SELECT count((SELECT row_number() OVER ()),row_number() OVER ())", "42883"},
		{"SELECT count((SELECT row_number() OVER ()),row_number() OVER ()) OVER ()", "42883"},
		{"SELECT count(*) FILTER(WHERE row_number() OVER ()>0) OVER ()", "42P20"},
		{"SELECT count(count(1),row_number() OVER ())", "42883"},
		{"SELECT count(count(1),row_number() OVER ()) OVER ()", "42883"},
		{"SELECT count(count(1),count(row_number() OVER ())) OVER ()", "42803"},
		{"SELECT row_number(count(1))", "42883"},
		{"SELECT count((SELECT count(row_number() OVER ()))) OVER ()", "42803"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.aggregateClause = "SELECT"
			b.queryFrame = 1
			e.mu.Lock()
			defer e.mu.Unlock()
			for _, sql := range []string{"SELECT count(*)", "SELECT row_number() OVER ()"} {
				if _, err := b.bind(statement(t, sql).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext}); err != nil {
					t.Fatal(err)
				}
			}
			if len(b.aggregates) != 1 || len(b.windowCalls) != 1 {
				t.Fatal("nonempty registration seeds absent")
			}
			// A distinct COUNT child must append beyond this bounded capacity.
			b.aggregates = b.aggregates[:len(b.aggregates):len(b.aggregates)]
			b.windowCalls = b.windowCalls[:len(b.windowCalls):len(b.windowCalls)]
			aggregates, windows := b.aggregates, b.windowCalls
			aggregateValues := append([]aggregateCall(nil), aggregates...)
			windowValues := append([]windowCall(nil), windows...)
			seedOccurrence := *windows[0].expression
			frame, clause := b.queryFrame, b.aggregateClause
			raw := statement(t, tc.sql).(*parser.Select).Targets[0]
			_, err := b.bind(raw, expressionPolicy{context: projectionContext})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
				t.Fatal(err)
			}
			if len(b.aggregates) != len(aggregates) || cap(b.aggregates) != cap(aggregates) || &b.aggregates[0] != &aggregates[0] ||
				len(b.windowCalls) != len(windows) || cap(b.windowCalls) != cap(windows) || &b.windowCalls[0] != &windows[0] {
				t.Fatal("failed analysis replaced exact registration headers", len(b.aggregates), cap(b.aggregates), len(b.windowCalls), cap(b.windowCalls))
			}
			if !reflect.DeepEqual(aggregateValues, b.aggregates) || !reflect.DeepEqual(windowValues, b.windowCalls) ||
				!reflect.DeepEqual(seedOccurrence, *b.windowCalls[0].expression) || b.argumentOwner != (argumentWindowOwner{}) ||
				len(b.queryScopes) != 0 || b.queryFrame != frame || b.aggregateClause != clause {
				t.Fatal("failed analysis mutated seed entries, occurrence identities or scoped state", b.argumentOwner, b.aggregates, b.windowCalls, b.queryScopes)
			}
			if _, err := b.bind(statement(t, "SELECT count(1)").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext}); err != nil {
				t.Fatal(err)
			}
			if len(b.aggregates) != len(aggregates)+1 || &b.aggregates[0] == &aggregates[0] ||
				!reflect.DeepEqual(aggregateValues, b.aggregates[:len(aggregates)]) || b.argumentOwner != (argumentWindowOwner{}) ||
				b.queryFrame != frame || b.aggregateClause != clause {
				t.Fatal("successful distinct ordinary registration was not retained across bounded reallocation")
			}
			retainedAggregates := b.aggregates
			if _, err := b.bind(statement(t, "SELECT row_number() OVER ()").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext}); err != nil {
				t.Fatal(err)
			}
			if len(b.windowCalls) != len(windows)+1 || b.argumentOwner != (argumentWindowOwner{}) ||
				!reflect.DeepEqual(seedOccurrence, *b.windowCalls[0].expression) || b.queryFrame != frame || b.aggregateClause != clause {
				t.Fatal("successful sibling retained failed owner or mutated seed", b.argumentOwner, b.windowCalls)
			}
			retainedWindows := b.windowCalls
			retainedAggregateValues := append([]aggregateCall(nil), retainedAggregates...)
			owner := argumentWindowOwner{active: true, window: true, found: true, first: parser.Position{Character: 23}}
			b.argumentOwner = owner
			leave := b.enterQuery(false)
			if b.argumentOwner != (argumentWindowOwner{}) {
				t.Fatal("lexical child inherited owner", b.argumentOwner)
			}
			b.aggregateClause = "SELECT"
			if _, err := b.bind(statement(t, "SELECT row_number() OVER ()").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext}); err != nil {
				t.Fatal(err)
			}
			leave()
			if b.argumentOwner != owner || len(b.windowCalls) != len(windows)+1 ||
				len(b.aggregates) != len(retainedAggregates) || cap(b.aggregates) != cap(retainedAggregates) ||
				&b.aggregates[0] != &retainedAggregates[0] || !reflect.DeepEqual(retainedAggregateValues, b.aggregates) ||
				len(b.windowCalls) != len(retainedWindows) || cap(b.windowCalls) != cap(retainedWindows) ||
				&b.windowCalls[0] != &retainedWindows[0] || !reflect.DeepEqual(seedOccurrence, *b.windowCalls[0].expression) ||
				b.queryFrame != frame || b.aggregateClause != clause || len(b.queryScopes) != 0 {
				t.Fatal("lexical child failed to restore parent ownership")
			}
		})
	}
}
