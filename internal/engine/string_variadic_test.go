package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestStringVariadicRetainedGeneration(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	ast, description := sequencePreparedDescription(t, e, s, "SELECT quote_ident('foo')")
	if description.quotes != nil {
		t.Fatal("Describe published Execute-only quotes")
	}
	result, err := sequencePreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if len(description.quotes) != 1 || description.quoteGeneration != s.discardPlansGeneration || description.planOwner != s.token {
		t.Fatal("missing owner-qualified capture")
	}
	original := description
	result, err = sequencePreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if &original.quotes[0] != &description.quotes[0] {
		t.Fatal("stable hit copied private group")
	}
	discardUnitApply(t, s, parser.DiscardPlans)
	if &original.quotes[0] != &description.quotes[0] || description.quoteGeneration != original.quoteGeneration {
		t.Fatal("eager discard mutation")
	}
	result, err = sequencePreparedExecute(e, s, ast, &description)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if &original.quotes[0] == &description.quotes[0] || description.quoteGeneration != s.discardPlansGeneration {
		t.Fatal("discard did not recollect quotes")
	}
	if original.quotes[0] != description.quotes[0] {
		t.Fatal("source capture changed during refresh")
	}

	bad, failed := sequencePreparedDescription(t, e, s, "SELECT quote_ident('foo'),quote_literal(1/0)")
	_, err = sequencePreparedExecute(e, s, bad, &failed)
	if err == nil || failed.quotes != nil {
		t.Fatal("failed demanded fold published partial group", err)
	}
	before := description
	discardUnitApply(t, s, parser.DiscardPlans)
	savedResults := description.Results
	description.Results = nil
	_, err = sequencePreparedExecute(e, s, ast, &description)
	description.Results = savedResults
	if err == nil || description.quoteGeneration != before.quoteGeneration || &description.quotes[0] != &before.quotes[0] {
		t.Fatal("signature failure published private group", err)
	}

	// Retagging owns a detached tree and exact new descriptors, never changing
	// the source generation. An equal OID cannot authorize capture adoption.
	ctx := context.Background()
	binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	bound, err := binder.bind(nullCountRaw("quote_literal", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 42}}), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	node := bound.(functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin])
	oldGroup := quoteCaptureCandidate{}
	_, _, err = foldStringVariadic(&EvalContext{Context: ctx, quoteCaptures: &oldGroup}, node)
	if err != nil || len(oldGroup.entries) != 1 {
		t.Fatal(err)
	}
	types := formatUnitTypes()
	retagged, changed, err := retagBoundTypes(binder.bindingCatalog(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	fresh := retagged.(functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin])
	if fresh.Usage.Type != types[sqltype.Text] || fresh.Args[0].ResultUsage().Type != types[sqltype.Int4] || node.Usage.Type == fresh.Usage.Type || node.Args[0].ResultUsage().Type == fresh.Args[0].ResultUsage().Type {
		t.Fatal("retag source mutated or authority not replaced")
	}
	candidate := quoteCaptureCandidate{entries: oldGroup.entries}
	_, _, err = foldStringVariadic(&EvalContext{Context: ctx, quoteCaptures: &candidate}, fresh)
	if err != nil || !candidate.private || len(candidate.entries) != 1 || candidate.entries[0].value.Type() != types[sqltype.Text] || oldGroup.entries[0].value.Type() != node.Usage.Type {
		t.Fatal("coincident OID adopted or source group mutated", err)
	}

	// Two DEFAULT definitions, a view, and statement syntax can have identical
	// offset/frame/kind/usage without sharing an occurrence or captured result.
	sessionSQL(t, s, "CREATE TABLE sv_quote_origins(a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'))")
	sessionSQL(t, s, "CREATE VIEW sv_quote_origin_view AS SELECT quote_ident('v') AS v")
	table := primaryKeyUnitRelation(t, e, "sv_quote_origins")
	view := primaryKeyUnitRelation(t, e, "sv_quote_origin_view")
	catalog := e.CatalogRead()
	tableRef := e.domain.Ref(catalog.Epoch(), table.address)
	viewRef := e.domain.Ref(catalog.Epoch(), view.address)
	origins := []quoteOrigin{
		{owner: tableRef, attribute: table.attributes[0], definition: table.defaults[table.attributes[0]].OID},
		{owner: tableRef, attribute: table.attributes[1], definition: table.defaults[table.attributes[1]].OID},
		{owner: viewRef, definition: view.view.rule, revision: view.view.quoteRevision},
		{},
	}
	stamped := node.WithOrigin(origins[2])
	targetRef := viewRef
	targetRef.Epoch++
	remapped, err := remapBoundExpression(stamped, func(ref ids.ObjectRef) ids.ObjectRef {
		if ref == viewRef {
			return targetRef
		}
		return ref
	}, nil)
	if err != nil || remapped.(functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]).Origin.owner != targetRef || stamped.Origin != origins[2] {
		t.Fatal("view quote origin remap lost target ownership or mutated source", err)
	}
	stampedRetagged, _, err := retagBoundTypes(binder.bindingCatalog(), stamped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || stampedRetagged.(functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]).Origin != stamped.Origin {
		t.Fatal("view quote origin lost during typed retag", err)
	}
	collision := quoteCaptureCandidate{}
	for i, origin := range origins {
		input := string(rune('a' + i))
		source := literalExpression{expressionInfo: expressionInfo{usage: node.Usage}, value: sqltype.String(node.Usage.Type, input)}
		occurrence := functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]{Info: functionInfo(expressionInfo{usage: node.Usage, pos: parser.Position{Offset: 17}}), Name: "quote_ident", Frame: 1, Args: []BoundExpr{source}}
		ambient := origin
		if origin.owner != (ids.ObjectRef{}) && origin.attribute == 0 {
			occurrence = occurrence.WithOrigin(origin)
			ambient = quoteOrigin{} // Lowered view nodes override caller-root context.
		}
		folded, _, err := foldStringVariadic(&EvalContext{Context: ctx, quoteCaptures: &collision, quoteOrigin: ambient}, occurrence)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := folded.(literalExpression).value.Text()
		if got != input || len(collision.entries) != i+1 {
			t.Fatal("distinct stored origins collided", got, input, collision.entries)
		}
	}
	published := collision.entries
	stableOrigins := quoteCaptureCandidate{entries: published}
	stableOrigins.prune(e.CatalogRead())
	if stableOrigins.private || &stableOrigins.entries[0] != &published[0] {
		t.Fatal("valid origin pruning copied group")
	}
	sessionSQL(t, s, "ALTER TABLE sv_quote_origins RENAME COLUMN a TO renamed")
	stableOrigins.prune(e.CatalogRead())
	if stableOrigins.private {
		t.Fatal("unrelated rename invalidated DEFAULT output")
	}
	sessionSQL(t, s, "ALTER TABLE sv_quote_origins ALTER COLUMN renamed SET DEFAULT quote_ident('new')")
	retired := quoteCaptureCandidate{entries: published}
	retired.prune(e.CatalogRead())
	if !retired.private || len(retired.entries) != 3 || len(published) != 4 || retired.entries[0].origin != origins[1] {
		t.Fatal("default replacement retained old origin or mutated publication")
	}
	sessionSQL(t, s, "CREATE OR REPLACE VIEW sv_quote_origin_view AS SELECT quote_ident('changed') AS v")
	retired.prune(e.CatalogRead())
	if len(retired.entries) != 2 || len(published) != 4 {
		t.Fatal("view replacement retained prior defining revision")
	}
	sealed := quoteCaptureCandidate{entries: published, sealed: true}
	missing := functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]{Info: functionInfo(expressionInfo{usage: node.Usage, pos: parser.Position{Offset: 19}}), Name: "quote_ident", Frame: 1, Args: []BoundExpr{literalExpression{expressionInfo: expressionInfo{usage: node.Usage}, value: sqltype.String(node.Usage.Type, "local")}}}
	_, _, err = foldStringVariadic(&EvalContext{Context: ctx, quoteCaptures: &sealed}, missing)
	if err != nil || sealed.private || len(sealed.entries) != 4 {
		t.Fatal("sealed executor group admitted row-time publication", err)
	}

	t.Run("LoweredViewLayersAndLocalChecks", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		// Equal-length source/view names place both quote calls at the same
		// offset in frame 1. The two definitions must never share a capture.
		for _, query := range []string{
			"CREATE TABLE bv(v text)",
			"INSERT INTO bv VALUES('seed')",
			"CREATE VIEW iv AS SELECT v FROM bv WHERE v<>quote_ident('inval') WITH LOCAL CHECK OPTION",
			"CREATE VIEW ov AS SELECT v FROM iv WHERE v<>quote_ident('outvl') WITH LOCAL CHECK OPTION",
		} {
			sessionSQL(t, session, query)
		}
		for _, query := range []string{
			"INSERT INTO ov VALUES('inval')",
			"INSERT INTO ov VALUES('outvl')",
			"UPDATE ov SET v='inval' WHERE v='seed'",
			"UPDATE ov SET v='outvl' WHERE v='seed'",
		} {
			ast, description := sequencePreparedDescription(t, engine, session, query)
			_, err := sequencePreparedExecute(engine, session, ast, &description)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "44000" {
				t.Fatalf("%s: local check lost its layer: %v", query, err)
			}
			if len(description.quotes) != 2 {
				t.Fatalf("%s: missing layer captures: %+v", query, description.quotes)
			}
			a, b := description.quotes[0], description.quotes[1]
			av, _ := a.value.Text()
			bv, _ := b.value.Text()
			if a.offset != b.offset || a.frame != b.frame || a.origin.owner == b.origin.owner || av == bv || (av != "inval" && av != "outvl") || (bv != "inval" && bv != "outvl") {
				t.Fatalf("lowered equal-offset view calls collided: %+v / %+v", a, b)
			}
		}
		ast, description := sequencePreparedDescription(t, engine, session, "INSERT INTO ov VALUES('valid')")
		if _, err := sequencePreparedExecute(engine, session, ast, &description); err != nil {
			t.Fatal("valid local-check row rejected", err)
		}
		published := description.quotes
		sessionSQL(t, session, "SET quote_all_identifiers=on")
		sessionSQL(t, session, "ALTER TABLE bv ADD COLUMN spare integer")
		if _, err := sequencePreparedExecute(engine, session, ast, &description); err != nil {
			t.Fatal("stored-only analysis replan failed", err)
		}
		if len(description.quotes) != 2 || &description.quotes[0] != &published[0] {
			t.Fatal("analysis-only replan copied or refreshed unchanged view captures")
		}
		sessionSQL(t, session, "CREATE VIEW nv AS SELECT v FROM bv WHERE concat(quote_nullable(v),'')=quote_nullable(v) WITH LOCAL CHECK OPTION")
		nestedAST, nestedDescription := sequencePreparedDescription(t, engine, session, "UPDATE nv SET v='nested' WHERE v='seed'")
		result, err := sequencePreparedExecute(engine, session, nestedAST, &nestedDescription)
		if err != nil || result.Count != 1 || len(nestedDescription.quotes) != 0 {
			t.Fatal("nested row-dependent quotes lost column lowering or were retained", result.Count, err)
		}
		for _, name := range []string{"iv", "ov", "nv"} {
			view := primaryKeyUnitRelation(t, engine, name)
			_, _, predicates, reason := viewWriteShape(view)
			if reason != "" {
				t.Fatal(reason)
			}
			for _, predicate := range predicates {
				walkBoundExpression(predicate, func(expression BoundExpr) {
					if quote, ok := expression.(functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]); ok && quote.Origin != (quoteOrigin{}) {
						t.Fatal("execution lowering stamped the stored source tree")
					}
				})
			}
		}
	})

	t.Run("AnalysisReplanRetiresOnlyRoot", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		sessionSQL(t, session, "CREATE TABLE sv_replan(v text,w text DEFAULT quote_ident('stored'))")
		ast, description := sequencePreparedDescription(t, engine, session, "INSERT INTO sv_replan(v) VALUES(quote_ident('root'))")
		if _, err := sequencePreparedExecute(engine, session, ast, &description); err != nil {
			t.Fatal(err)
		}
		published := description.quotes
		sessionSQL(t, session, "SET quote_all_identifiers=on")
		sessionSQL(t, session, "ALTER TABLE sv_replan ADD COLUMN spare integer")
		if _, err := sequencePreparedExecute(engine, session, ast, &description); err != nil {
			t.Fatal(err)
		}
		if len(published) != 2 || len(description.quotes) != 2 {
			t.Fatal("analysis replan lost root or default capture")
		}
		for _, capture := range description.quotes {
			want := "stored"
			if capture.origin.owner == (ids.ObjectRef{}) {
				want = `"root"`
			}
			got, _ := capture.value.Text()
			if got != want {
				t.Fatalf("analysis replan capture = %q, want %q", got, want)
			}
		}
		for _, capture := range published {
			got, _ := capture.value.Text()
			if got != "root" && got != "stored" {
				t.Fatal("analysis replan mutated published source captures")
			}
		}
	})

	t.Run("StalePublishedGroupDemandFailureRetry", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		sessionSQL(t, session, "SET quote_all_identifiers=off")
		sessionSQL(t, session, "SET bytea_output=hex")
		sessionSQL(t, session, `CREATE TABLE sv_retry(v text,z timestamptz,a text DEFAULT quote_ident('stored'),b text DEFAULT quote_literal('\x00015c'::bytea))`)
		ast, description := sequencePreparedDescription(t, engine, session, "INSERT INTO sv_retry(v,z) VALUES(quote_ident('root'),'now'::timestamptz)")
		result, err := sequencePreparedExecute(engine, session, ast, &description)
		if err != nil || result.Count != 1 {
			t.Fatal("initial capture execution", result.Count, err)
		}
		before := description
		if len(before.quotes) != 3 || len(before.temporals) == 0 || before.analysis == nil {
			t.Fatal("missing initial quote/temporal/analysis publication")
		}
		source := append([]quoteConstant(nil), before.quotes...)
		beforeRelation := primaryKeyUnitRelation(t, engine, "sv_retry")
		aDefinition := beforeRelation.defaults[beforeRelation.attributes[2]].OID
		bDefinition := beforeRelation.defaults[beforeRelation.attributes[3]].OID
		initial := map[string]bool{"root": false, "stored": false, `E'\\x00015c'`: false}
		for _, capture := range source {
			value, err := capture.value.Text()
			seen, known := initial[value]
			if err != nil || !known || seen {
				t.Fatal("unexpected initial retained value", value, err)
			}
			initial[value] = true
		}
		discardUnitApply(t, session, parser.DiscardPlans)
		sessionSQL(t, session, "ALTER TABLE sv_retry ALTER COLUMN b SET DEFAULT quote_literal(1/0)")
		result, err = sequencePreparedExecute(engine, session, ast, &description)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}) || result.Rows != nil {
			t.Fatal("demanded fold failure", result, err)
		}
		if description.planOwner != session.token || description.planGeneration != session.discardPlansGeneration || description.planGeneration == before.planGeneration || description.analysis == before.analysis || len(description.temporals) == 0 || &description.temporals[0] == &before.temporals[0] {
			t.Fatal("failure did not occur after temporal/analysis generation advance")
		}
		if description.quoteGeneration != before.quoteGeneration || len(description.quotes) != len(source) || &description.quotes[0] != &before.quotes[0] {
			t.Fatal("failed private quote refresh replaced publication")
		}
		for i, capture := range source {
			if before.quotes[i] != capture || description.quotes[i] != capture {
				t.Fatal("failed refresh mutated source capture", i)
			}
		}
		rows := sessionSQL(t, session, "SELECT v,a,b FROM sv_retry")
		values := tuples(rows)
		rows.Rows.Close()
		if len(values) != 1 || values[0][0] != "root" || values[0][1] != "stored" || values[0][2] != `E'\\x00015c'` {
			t.Fatal("failed demanded fold changed committed rows", values)
		}
		sessionSQL(t, session, "SET quote_all_identifiers=on")
		sessionSQL(t, session, "SET bytea_output=escape")
		sessionSQL(t, session, `ALTER TABLE sv_retry ALTER COLUMN b SET DEFAULT quote_literal('\x00015c'::bytea)`)
		result, err = sequencePreparedExecute(engine, session, ast, &description)
		if err != nil || result.Count != 1 {
			t.Fatal("successful stale retry", result.Count, err)
		}
		if description.quoteGeneration != session.discardPlansGeneration || description.planGeneration != session.discardPlansGeneration || description.planOwner != session.token || len(description.quotes) != 3 || &description.quotes[0] == &before.quotes[0] {
			t.Fatal("retry did not publish fresh owner-qualified group and stamps")
		}
		relation := primaryKeyUnitRelation(t, engine, "sv_retry")
		if relation.defaults[relation.attributes[2]].OID != aDefinition || relation.defaults[relation.attributes[3]].OID == bDefinition {
			t.Fatal("retry did not preserve a and replace b DEFAULT identity")
		}
		catalog := engine.CatalogRead()
		ref := engine.domain.Ref(catalog.Epoch(), relation.address)
		seen := map[string]bool{}
		for _, capture := range description.quotes {
			value, err := capture.value.Text()
			if err != nil {
				t.Fatal(err)
			}
			want := `"root"`
			if capture.origin.owner != (ids.ObjectRef{}) {
				if capture.origin.owner != ref {
					t.Fatal("retry adopted foreign stored origin")
				}
				switch capture.origin.attribute {
				case relation.attributes[2]:
					want = `"stored"`
				case relation.attributes[3]:
					want = `E'\\000\\001\\\\'`
				default:
					t.Fatal("unexpected retry DEFAULT attribute")
				}
				if capture.origin.definition != relation.defaults[capture.origin.attribute].OID {
					t.Fatal("retry retained obsolete DEFAULT definition")
				}
			}
			if value != want || seen[want] {
				t.Fatalf("retry capture = %q, want distinct %q", value, want)
			}
			seen[want] = true
		}
		for i, capture := range source {
			if before.quotes[i] != capture {
				t.Fatal("successful retry mutated source publication", i)
			}
		}
	})

	t.Run("MergeDefaultOrigins", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		sessionSQL(t, session, "CREATE TABLE sv_merge_origin(id integer,a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'),r text)")
		ast, description := sequencePreparedDescription(t, engine, session, "MERGE INTO sv_merge_origin t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id,r) VALUES(s.id,quote_ident('root'))")
		result, err := sequencePreparedExecute(engine, session, ast, &description)
		if err != nil || result.Count != 1 || len(description.quotes) != 3 {
			t.Fatal("MERGE did not execute demanded defaults", result.Count, description.quotes, err)
		}
		relation := primaryKeyUnitRelation(t, engine, "sv_merge_origin")
		catalog := engine.CatalogRead()
		ref := engine.domain.Ref(catalog.Epoch(), relation.address)
		seen := map[string]bool{}
		for _, capture := range description.quotes {
			value, err := capture.value.Text()
			if err != nil {
				t.Fatal(err)
			}
			want := "root"
			if capture.origin.owner != (ids.ObjectRef{}) {
				if capture.origin.owner != ref || capture.origin.definition != relation.defaults[capture.origin.attribute].OID {
					t.Fatal("MERGE default lost physical origin", capture.origin)
				}
				switch capture.origin.attribute {
				case relation.attributes[1]:
					want = "a"
				case relation.attributes[2]:
					want = "b"
				default:
					t.Fatal("MERGE captured unrelated default")
				}
			}
			if value != want || seen[want] {
				t.Fatal("MERGE origins collided", value, want)
			}
			seen[want] = true
		}
		for _, attribute := range relation.attributes[1:3] {
			node := relation.defaults[attribute].expression.(functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin])
			if node.Origin != (quoteOrigin{}) {
				t.Fatal("MERGE stamped catalog DEFAULT source")
			}
		}
	})

	t.Run("ParameterizedMergeOperationLocal", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		sessionSQL(t, session, "CREATE TABLE sv_local_merge(id integer,a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'),r text)")
		ast, description := sequencePreparedDescription(t, engine, session, "MERGE INTO sv_local_merge t USING (VALUES($1::integer)) s(id) ON t.id=$1::integer WHEN NOT MATCHED THEN INSERT(id,r) VALUES($1::integer,quote_ident('root'))")
		peerAST, peerDescription := sequencePreparedDescription(t, engine, session, "MERGE INTO sv_local_merge t USING (VALUES($1::integer)) s(id) ON t.id=$1::integer WHEN NOT MATCHED THEN INSERT(id,r) VALUES($1::integer,quote_ident('root'))")
		stamp := description.quoteGeneration
		var previous []quoteConstant
		for id := int64(1); id <= 12; id++ {
			on := id%2 == 1
			setting := "off"
			if on {
				setting = "on"
			}
			if id == 9 || id == 11 {
				sessionSQL(t, session, "BEGIN")
				if id == 11 {
					sessionSQL(t, session, "SAVEPOINT sv_local")
				}
				sessionSQL(t, session, "SET LOCAL quote_all_identifiers="+setting)
				discardUnitApply(t, session, parser.DiscardPlans)
			} else {
				if id == 10 {
					sessionSQL(t, session, "ROLLBACK")
				}
				if id == 12 {
					sessionSQL(t, session, "ROLLBACK TO SAVEPOINT sv_local")
				}
				sessionSQL(t, session, "SET quote_all_identifiers="+setting)
			}
			if id == 7 {
				discardUnitApply(t, session, parser.DiscardPlans)
			}
			func() {
				op, err := session.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := engine.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				owner, statement := &description, ast
				if id%2 == 0 {
					owner, statement = &peerDescription, peerAST
				}
				scope.Expected = owner
				result, err := scope.Execute(statement, 1, func(ctx context.Context, p ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
					value, err := sqltype.Integer(p.Types[0].Type, id)
					frame[0] = value
					return err
				})
				if err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				group := scope.quoteCaptures
				if result.Count != 1 || group == nil || !group.sealed || len(group.entries) != 3 || description.quotes != nil || peerDescription.quotes != nil || description.quoteGeneration != stamp || peerDescription.quoteGeneration != stamp {
					scope.Finish(errors.New("unexpected quote publication"))
					t.Fatal("parameterized MERGE retained/shared quotes", result.Count, group, description.quotes)
				}
				if previous != nil && &previous[0] == &group.entries[0] {
					t.Fatal("parameterized execution borrowed prior backing")
				}
				relation := primaryKeyUnitRelation(t, engine, "sv_local_merge")
				catalog := engine.CatalogRead()
				ref := engine.domain.Ref(catalog.Epoch(), relation.address)
				seen := map[string]bool{}
				for _, capture := range group.entries {
					want := "root"
					if capture.origin.owner != (ids.ObjectRef{}) {
						if capture.origin.owner != ref || capture.origin.definition != relation.defaults[capture.origin.attribute].OID {
							t.Fatal("operation-local default origin lost")
						}
						switch capture.origin.attribute {
						case relation.attributes[1]:
							want = "a"
						case relation.attributes[2]:
							want = "b"
						default:
							t.Fatal("unexpected operation-local default")
						}
					}
					if on {
						want = `"` + want + `"`
					}
					value, err := capture.value.Text()
					if err != nil || value != want || seen[want] {
						t.Fatal("parameterized group did not use current settings", value, want, err)
					}
					seen[want] = true
				}
				for _, capture := range previous {
					value, err := capture.value.Text()
					if err != nil || on && strings.Contains(value, `"`) || !on && !strings.Contains(value, `"`) {
						t.Fatal("fresh execution mutated previous group", value, err)
					}
				}
				previous = group.entries
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			}()
		}
		sessionSQL(t, session, "ROLLBACK")
	})

	t.Run("ParameterizedViewOperationLocal", func(t *testing.T) {
		engine := New()
		session := sessionStart(t, engine, nil)
		sessionSQL(t, session, "CREATE TABLE sv_local_view_base(id integer,r text)")
		sessionSQL(t, session, "CREATE VIEW sv_local_view AS SELECT id,r FROM sv_local_view_base WHERE r<>quote_ident('blocked') WITH LOCAL CHECK OPTION")
		ast, description := sequencePreparedDescription(t, engine, session, "INSERT INTO sv_local_view VALUES($1::integer,quote_ident('root'))")
		stamp := description.quoteGeneration
		view := primaryKeyUnitRelation(t, engine, "sv_local_view")
		catalog := engine.CatalogRead()
		origin := quoteOrigin{owner: engine.domain.Ref(catalog.Epoch(), view.address), definition: view.view.rule, revision: view.view.quoteRevision}
		var previous []quoteConstant
		for id := int64(1); id <= 2; id++ {
			setting, root, blocked := "off", "root", "blocked"
			if id == 1 {
				setting, root, blocked = "on", `"root"`, `"blocked"`
			}
			sessionSQL(t, session, "SET quote_all_identifiers="+setting)
			func() {
				op, err := session.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := engine.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				scope.Expected = &description
				result, err := scope.Execute(ast, 1, func(ctx context.Context, p ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
					value, err := sqltype.Integer(p.Types[0].Type, id)
					frame[0] = value
					return err
				})
				if err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				group := scope.quoteCaptures
				if result.Count != 1 || group == nil || !group.sealed || len(group.entries) != 2 || description.quotes != nil || description.quoteGeneration != stamp {
					scope.Finish(errors.New("unexpected view quote publication"))
					t.Fatal("parameterized view retained quote group", group)
				}
				if previous != nil && &previous[0] == &group.entries[0] {
					t.Fatal("view borrowed prior parameterized group")
				}
				seenRoot, seenView := false, false
				for _, capture := range group.entries {
					value, err := capture.value.Text()
					if err != nil {
						t.Fatal(err)
					}
					if capture.origin.owner == (ids.ObjectRef{}) {
						seenRoot = value == root
					} else {
						seenView = value == blocked && capture.origin == origin
					}
				}
				if !seenRoot || !seenView {
					t.Fatal("operation-local root/view origins lost current settings")
				}
				for _, capture := range previous {
					value, _ := capture.value.Text()
					if !strings.Contains(value, `"`) {
						t.Fatal("view mutated previous on-setting group")
					}
				}
				previous = group.entries
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			}()
		}
	})
}

func TestStringVariadicBoundedCancellationAndSizes(t *testing.T) {
	types := formatUnitTypes()
	for _, name := range []string{"concat", "quote_ident", "quote_literal"} {
		text := strings.Repeat("'a", 8192)
		arg := literalExpression{expressionInfo: expressionInfo{usage: types[sqltype.Text].Usage()}, value: sqltype.String(types[sqltype.Text], text)}
		node := functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]{Info: functionInfo(expressionInfo{usage: types[sqltype.Text].Usage()}), Name: name, Args: []BoundExpr{arg}}
		group := quoteCaptureCandidate{}
		ctx := &formatWorkContext{Context: context.Background(), checks: 4}
		var err error
		if name == "concat" {
			_, err = node.Eval(&EvalContext{Context: ctx})
		} else {
			_, _, err = foldStringVariadic(&EvalContext{Context: ctx, quoteCaptures: &group}, node)
		}
		if !errors.Is(err, context.Canceled) || group.private || group.entries != nil {
			t.Fatal("cancellation exposed a scalar or partial capture", name, err)
		}
	}
	// Pure bounded-size diagnostics moved to functions.TestStringVariadicOutputSize;
	// cancellation must still leave this owner's capture group unpublished.
}
