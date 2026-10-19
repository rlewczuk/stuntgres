package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLockOccurrenceNormalization(t *testing.T) {
	for _, tc := range []struct {
		q     string
		modes []lock.Mode
		waits []parser.RowLockWait
	}{
		{"SELECT a.id FROM t a JOIN t b USING(id) FOR UPDATE OF a,a SKIP LOCKED FOR SHARE OF a NOWAIT", []lock.Mode{lock.ForUpdate, 0}, []parser.RowLockWait{parser.RowLockNowait, parser.RowLockBlock}},
		{"SELECT a.id FROM t a JOIN t b USING(id) FOR SHARE OF a NOWAIT FOR UPDATE OF a SKIP LOCKED", []lock.Mode{lock.ForUpdate, 0}, []parser.RowLockWait{parser.RowLockNowait, parser.RowLockBlock}},
		{"SELECT a.id FROM t a JOIN t b USING(id) FOR SHARE OF a FOR KEY SHARE OF a SKIP LOCKED FOR UPDATE OF b NOWAIT", []lock.Mode{lock.ForShare, lock.ForUpdate}, []parser.RowLockWait{parser.RowLockSkip, parser.RowLockNowait}},
		{"SELECT j.id FROM (t JOIN u USING(id)) j FOR UPDATE OF t", []lock.Mode{lock.ForUpdate, 0}, []parser.RowLockWait{parser.RowLockBlock, parser.RowLockBlock}},
		{"SELECT x.id FROM t CROSS JOIN (VALUES(1)) x(id) FOR SHARE OF x", []lock.Mode{0}, []parser.RowLockWait{parser.RowLockBlock}},
		{"SELECT a.id FROM t a JOIN t b USING(id) FOR KEY SHARE FOR UPDATE OF b SKIP LOCKED", []lock.Mode{lock.ForKeyShare, lock.ForUpdate}, []parser.RowLockWait{parser.RowLockBlock, parser.RowLockSkip}},
	} {
		raw := statement(t, tc.q).(*parser.Select)
		marks, err := queryLockTargets(raw.From, raw.Locking)
		if err != nil || len(marks) != len(tc.modes) {
			t.Fatal(tc.q, marks, err)
		}
		for i, mark := range marks {
			if mark.mode != tc.modes[i] || mark.wait != tc.waits[i] {
				t.Fatal(tc.q, i, mark)
			}
		}
		again, err := queryLockTargets(raw.From, raw.Locking)
		if err != nil || !reflect.DeepEqual(marks, again) {
			t.Fatal("normalization mutated original clauses", marks, again, err)
		}
	}
	raw := statement(t, "SELECT id FROM t").(*parser.Select)
	if marks, err := queryLockTargets(raw.From, raw.Locking); marks != nil || err != nil {
		t.Fatal("ordinary source gained manifest", marks, err)
	}
}

func TestQueryLockNullableNormalizedDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, mode string }{
		{"SELECT a.k FROM a LEFT JOIN b USING(k) FOR KEY SHARE OF b FOR UPDATE OF b", "FOR UPDATE"},
		{"SELECT a.k FROM a FULL JOIN b USING(k) FOR SHARE OF a FOR UPDATE OF b", "FOR SHARE"},
		{"SELECT a.k FROM a FULL JOIN b USING(k) FOR UPDATE OF b FOR SHARE OF a", "FOR UPDATE"},
		{"SELECT a.k FROM a FULL JOIN b USING(k) FOR KEY SHARE OF b,a FOR UPDATE OF b", "FOR UPDATE"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			e, query, _ := queryJoinsFixture(t, tc.q)
			evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
			root, err := preflightQuery(&evaluation, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			waitsDiagnostic(t, validateQueryJoinExecution(&evaluation, root, e.CatalogRead(), 0), "0A000", tc.mode+" cannot be applied to the nullable side of an outer join")
		})
	}
}

func TestQueryLockSelectedACLAndViewPrincipals(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER mark_reader", "CREATE TABLE t(id INT,v INT)", "CREATE TABLE u(id INT,v INT)", "CREATE VIEW owned AS SELECT a.id FROM t a JOIN u b USING(id) FOR UPDATE OF a NOWAIT", "CREATE VIEW invoker WITH(security_invoker=true) AS SELECT a.id FROM t a JOIN u b USING(id) FOR UPDATE OF a NOWAIT"} {
		roleExecute(t, e, op, q)
	}
	roleExecute(t, e, op, "CREATE SEQUENCE s")
	reader, _ := e.Roles().Lookup("mark_reader")
	grantor := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true}
		if rights != 0 {
			acl.Entries = []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}
		}
		if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	table, other := roleTableRef(t, e, "t"), roleTableRef(t, e, "u")
	grant(table, authorization.SelectPrivilege)
	grant(other, authorization.SelectPrivilege)
	grant(roleTableRef(t, e, "s"), authorization.SelectPrivilege)
	column := table
	column.Address.SubID = 2
	grant(column, authorization.UpdatePrivilege)
	grant(roleTableRef(t, e, "owned"), authorization.SelectPrivilege)
	grant(roleTableRef(t, e, "invoker"), authorization.SelectPrivilege)
	check := func(q string) error {
		t.Helper()
		e.mu.Lock()
		defer e.mu.Unlock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			return err
		}
		auth.State.CurrentUser = reader.Ref
		binder := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		binder.queryAuth = &auth
		raw := statement(t, q).(*parser.Select)
		if _, err = binder.bindSelect(raw, false); err != nil {
			t.Fatal("executor ACL leaked into binding", q, err)
		}
		return auth.checkQuerySources(binder.querySources)
	}
	for _, q := range []string{"SELECT b.v FROM t a JOIN u b USING(id) FOR UPDATE OF a NOWAIT", "SELECT id FROM owned", "SELECT id FROM invoker"} {
		if err := check(q); err != nil {
			t.Fatal(q, err)
		}
	}
	// Reference-kind rejection is separate from physical-target UPDATE authority.
	if err := check("SELECT a.id FROM t a CROSS JOIN s FOR UPDATE OF a"); err != nil {
		t.Fatal("unselected sequence acquired invented UPDATE requirement", err)
	}
	waitsDiagnostic(t, check("SELECT a.id FROM t a JOIN u b USING(id) FOR UPDATE OF b"), "42501", "permission denied for table u")
	grant(column, 0)
	waitsDiagnostic(t, check("SELECT id FROM invoker"), "42501", "permission denied for table t")
	if err := check("SELECT id FROM owned"); err != nil {
		t.Fatal("owner view inherited invoker ACL", err)
	}
	grant(column, authorization.UpdatePrivilege)
	if err := check("SELECT id FROM invoker"); err != nil {
		t.Fatal("regrant retained stale executor ACL", err)
	}
}

func TestQueryLockPendingTargetsRemainOccurrenceOwned(t *testing.T) {
	inputs := &querySource{sources: []*querySource{{occurrence: 0}, {occurrence: 1}}}
	inputs.locks = &queryLockContext{leaves: []queryLockLeaf{
		{leaf: queryLeaf{relation: &catalog.Relation{RelationKind: catalog.TableRelation}}},
		{leaf: queryLeaf{relation: &catalog.Relation{RelationKind: catalog.TableRelation}}},
	}}
	marks := []queryRowMark{{mode: lock.ForUpdate, wait: parser.RowLockSkip}, {mode: lock.ForShare, wait: parser.RowLockNowait}}
	pending := appendPendingRowMarks(nil, marks, inputs)
	if len(pending) != 2 || !reflect.DeepEqual(pending[0].occurrences, []int{0}) || !reflect.DeepEqual(pending[1].occurrences, []int{1}) || pending[0].mark != marks[0] || pending[1].mark != marks[1] {
		t.Fatal(pending)
	}
	pending[0].occurrences[0] = 99
	if inputs.sources[0].occurrence != 0 || marks[0].mode != lock.ForUpdate {
		t.Fatal("execution pending state mutated bound/source identity")
	}
}

func TestPGSequencesEffectiveRowMarks(t *testing.T) {
	e := New()
	view := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12048})
	table := &catalog.Relation{RelationKind: catalog.TableRelation}
	requested := queryRowMark{mode: lock.ForUpdate, wait: parser.RowLockNowait, diagnosticMode: lock.ForUpdate, order: 1}
	locks := &queryLockContext{leaves: []queryLockLeaf{
		{leaf: queryLeaf{relation: view, mark: requested}, mark: effectiveQueryRowMark(view, requested)},
		{leaf: queryLeaf{relation: table, mark: requested}, mark: effectiveQueryRowMark(table, requested)},
	}}
	// A transparent stored/derived expansion may contain both the SQL-only
	// catalog occurrence and a genuinely marked table. Preserve its identities.
	expanded := &querySource{materialized: true, occurrences: []int{0, 1}}
	inputs := &querySource{sources: []*querySource{expanded}, locks: locks}
	pending := appendPendingRowMarks(nil, []queryRowMark{requested}, inputs)
	if len(pending) != 1 || !reflect.DeepEqual(pending[0].occurrences, []int{1}) || pending[0].mark != requested {
		t.Fatal("effective expansion changed ordinary physical mark", pending)
	}
	if !reflect.DeepEqual(expanded.occurrences, []int{0, 1}) || locks.leaves[0].leaf.mark != requested || locks.leaves[0].mark != (queryRowMark{}) {
		t.Fatal("effective decision mutated SQL marks or source identities")
	}
	inputs.sources[0] = &querySource{occurrence: 0}
	if pending := appendPendingRowMarks(nil, []queryRowMark{requested}, inputs); len(pending) != 0 {
		t.Fatal("SQL-only catalog mark reached physical arbitration", pending)
	}
}
