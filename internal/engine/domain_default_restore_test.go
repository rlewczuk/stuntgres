package engine

import (
	"encoding/base64"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDomainDefaultReplayAdmissionInternal(t *testing.T) {
	e := New().restoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.replay_required AS integer NOT NULL"))
	domainUnitTable(t, e, op, "CREATE TABLE public.replay_domain(v public.replay_required,w integer)")
	source := statement(t, "INSERT INTO public.replay_domain(v,w) VALUES(NULL,1)").(*parser.Insert)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
	}()
	if err := scope.gate(source); err != nil {
		t.Fatal(err)
	}
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	ref, err := e.resolveRelationReference(auth, source.Name, false)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		t.Fatal("missing destination")
	}
	identity := []string{"public", "replay_required"}
	valid := domainReplayRecord{Schema: "public", Table: "replay_domain", Cells: []domainReplayCell{{Column: "v", Value: &domainReplayNode{Kind: "domain_null", Type: &identity}}}}
	capture, err := e.captureDomainReplay(scope, source, valid, auth, relation)
	if err != nil || capture == nil || len(capture.cells) != 1 || !capture.cells[0].value.IsNull() || capture.cells[0].value.Type() != relation.Columns[0].Usage.Type {
		t.Fatal("exact domain NULL admission", capture, err)
	}
	for _, query := range []string{
		"INSERT INTO public.replay_domain(v,w) VALUES(CAST(NULL AS public.replay_required),1)",
		"INSERT INTO public.replay_domain(w) VALUES(NULL)",
		"INSERT INTO public.replay_domain(v,v) VALUES(NULL,NULL)",
	} {
		raw := statement(t, query).(*parser.Insert)
		if _, err := e.captureDomainReplay(scope, raw, valid, auth, relation); err == nil {
			t.Fatal("invalid destination admitted", query)
		}
	}
	ordinary := valid
	ordinary.Cells = []domainReplayCell{{Column: "v", Value: &domainReplayNode{Kind: "null"}}}
	if _, err := e.captureDomainReplay(scope, source, ordinary, auth, relation); err == nil {
		t.Fatal("fresh NULL acquired same-domain provenance")
	}
	foreign := valid
	wrong := []string{"other", "replay_required"}
	foreign.Cells = []domainReplayCell{{Column: "v", Value: &domainReplayNode{Kind: "domain_null", Type: &wrong}}}
	if _, err := e.captureDomainReplay(scope, source, foreign, auth, relation); err == nil {
		t.Fatal("foreign identity admitted")
	}
	forged := *relation
	if _, err := e.captureDomainReplay(scope, source, valid, auth, &forged); err == nil {
		t.Fatal("noncatalog destination admitted")
	}
	// A legitimate session, superuser and AST do not grant native import authority.
	normal := New()
	normalOp, normalDone := roleOperation(t, normal, "postgres")
	defer normalDone()
	normalAuth, err := normalOp.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normal.captureDomainReplay(&StatementScope{op: normalOp}, source, valid, normalAuth, nil); err == nil {
		t.Fatal("ordinary engine acquired native replay authority")
	}
}

func TestDomainDefaultReplayPairingInternal(t *testing.T) {
	record := domainReplayRecord{Schema: "public", Table: "t", Cells: []domainReplayCell{{Column: "v", Value: &domainReplayNode{Kind: "null"}}}}
	directive, err := encodeDomainReplay(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		text  string
		valid bool
	}{
		{"-- é\n" + directive + "INSERT INTO public.t(v) VALUES(NULL);", true},
		{directive + "INSERT INTO public.other(v) VALUES(NULL);", false},
		{directive + "SELECT 1; INSERT INTO public.t(v) VALUES(NULL);", false},
		{directive + "-- intervening\nINSERT INTO public.t(v) VALUES(NULL);", false},
		{directive + directive + "INSERT INTO public.t(v) VALUES(NULL);", false},
		{directive + "INSERT INTO public.t(v) VALUES(NULL),(NULL);", false},
		{directive + "INSERT INTO public.t(v) VALUES(NULL) RETURNING v;", false},
		{directive, false},
	} {
		var records []positionedDomainReplay
		statements, err := parser.ParseWithOptions(tc.text, parser.Options{StandardConformingStrings: true, LineComment: func(comment string, pos parser.Position) error {
			record, found, err := decodeDomainReplay(comment)
			if found && err == nil {
				records = append(records, positionedDomainReplay{record: record, end: pos.Offset + len(comment)})
			}
			return err
		}})
		if err != nil {
			t.Fatal(tc.text, err)
		}
		pairs, err := pairDomainReplay(tc.text, statements, records)
		if tc.valid && (err != nil || len(pairs) != 1) || !tc.valid && err == nil {
			t.Fatal(tc.text, pairs, err)
		}
	}
	valid := `{"schema":"public","table":"t","cells":[{"column":"v","value":{"kind":"null"}}]}`
	if _, found, err := decodeDomainReplay(domainReplayMarker + " " + base64.StdEncoding.EncodeToString([]byte(valid))); err != nil || !found {
		t.Fatal("valid wire record", found, err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"schema":"public"`, `"schema":"public","schema":"public"`, 1),
		strings.Replace(valid, `"kind":"null"`, `"kind":"null","kind":"null"`, 1),
		strings.Replace(valid, `"kind":"null"`, `"kind":"null","unknown":true`, 1),
		strings.Replace(valid, `"schema"`, `"Schema"`, 1),
		`{"schema":"public","table":"t","cells":null}`,
		valid + valid,
		strings.Replace(valid, `"kind":"null"`, `"kind":"array","dimensions":[{"lower":1}],"elements":[]`, 1),
	} {
		if _, _, err := decodeDomainReplay(domainReplayMarker + " " + base64.StdEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Fatal("malformed wire record admitted", raw)
		}
	}
}

func TestDomainDefaultReplayDeferredChecksInternal(t *testing.T) {
	e := New().restoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	// Restore adds captured CHECK recipes after rows, so the live declared
	// domain legitimately has no constraints at this exact import position.
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.replay_deferred AS integer"))
	table := domainUnitTable(t, e, op, "CREATE TABLE public.deferred_rows(v public.replay_deferred)")
	source := statement(t, "INSERT INTO public.deferred_rows VALUES(NULL)").(*parser.Insert)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
	}()
	if err := scope.gate(source); err != nil {
		t.Fatal(err)
	}
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	identity := []string{"public", "replay_deferred"}
	record := domainReplayRecord{Schema: "public", Table: "deferred_rows", Cells: []domainReplayCell{{Column: "v", Value: &domainReplayNode{Kind: "domain_null", Type: &identity}}}}
	capture, err := e.captureDomainReplay(scope, source, record, auth, table)
	if err != nil {
		t.Fatal("deferred CHECK declaration lost existing domain NULL provenance", err)
	}
	if value := capture.cells[0].value; !value.IsNull() || value.Type() != table.Columns[0].Usage.Type {
		t.Fatal("lost authentic declared domain identity", value)
	}
}

func TestDomainDefaultReplayRoutineManifestInternal(t *testing.T) {
	e := New().restoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	text := "pg_catalog.timezone('UTC'::text,'-infinity'::timestamptz)"
	source := statement(t, "SELECT "+text)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
	}()
	if err := scope.gate(source); err != nil {
		t.Fatal(err)
	}
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	typ, ok := auth.Catalog.LookupType(sqltype.Timestamp)
	if !ok {
		t.Fatal("missing timestamp")
	}
	c := EvalContext{evalStatement: evalStatement{Context: op.Context(), statementScope: scope, catalog: auth.Catalog, Input: scope.input, Output: scope.output}, evalFrame: evalFrame{Scope: 1, Frame: 1}, evalAdmission: evalAdmission{routineAuthority: &auth}}
	c.Input.ExactSnapshot = true
	c.Input.DomainChecks = &c
	value, count, err := restoreDomainReplayNode(&c, e, op, auth, typ.Usage(), &domainReplayNode{Kind: "scalar", Scalar: &text})
	if err != nil || count != 0 {
		t.Fatal("closed endpoint expression lost its original binding manifest", count, err)
	}
	output, err := typ.OutputWithEnvironment(op.Context(), value, scope.output)
	if err != nil || output != "-infinity" {
		t.Fatal(output, err)
	}
	// The leaf's local original manifest must not leak into its enclosing
	// domain CHECK recipe or authorize a later unrelated expression.
	if c.routineRecipe || c.routineChecks != nil || c.routineReferences != nil {
		t.Fatal("scalar lifetime escaped its local evaluation")
	}
}
