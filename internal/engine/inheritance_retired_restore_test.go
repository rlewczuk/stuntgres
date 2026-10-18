package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func retiredRepairSQL(table, name, kind string, local, beforeLocal bool, before int, rows string) string {
	return fmt.Sprintf("UPDATE pg_catalog.pg_constraint SET conislocal = %t, coninhcount = %d + (SELECT count(DISTINCT (retired.column1, retired.column6, retired.column7)) FROM (VALUES %s) AS retired) WHERE conrelid = 'public.%s'::regclass AND conname = '%s' AND contype = '%s' AND conkey = ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = 'public.%s'::regclass AND attname = 'a')] AND conislocal = %t AND coninhcount = %d AND EXISTS (SELECT 1 FROM pg_catalog.pg_inherits WHERE inhrelid = 'public.%s'::regclass AND inhparent = 'public.p'::regclass)", local, before, rows, table, name, kind, table, beforeLocal, before, table)
}
func retiredRow(kind, lifetime, oid string) string {
	return "('public.p'::regclass, 'a', 1, '" + kind + "', '" + map[string]string{"n": "nn", "c": "ck"}[kind] + "', '" + lifetime + "', '" + oid + "')"
}
func retiredRaw(t *testing.T, q string) *parser.Update {
	t.Helper()
	return statement(t, q).(*parser.Update)
}

func TestRestoreConstraintHistorySyntax(t *testing.T) {
	good := retiredRepairSQL("c", "nn", "n", false, false, 1, retiredRow("n", "0:7", "77"))
	parsed, err := restoreRetiredSyntax(retiredRaw(t, good))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.local || parsed.beforeLocal || parsed.before != 1 || parsed.name != "nn" || len(parsed.columns) != 1 || parsed.columns[0] != "a" || len(parsed.rows) != 1 || parsed.rows[0].archive != 0 || parsed.rows[0].lifetime != 7 || parsed.rows[0].attribute != 1 || parsed.rows[0].constraint != 77 {
		t.Fatal("seven-field canonical carrier decoded incorrectly", parsed)
	}
	identitySQL := "(retired.column1, retired.column6, retired.column7)"
	countSQL := "count(DISTINCT " + identitySQL + ")"
	for name, change := range map[string][2]string{
		"schema": {"pg_catalog.pg_constraint", "public.pg_constraint"}, "catalog": {"pg_catalog.pg_constraint", "pg_catalog.pg_class"},
		"only": {"UPDATE pg_catalog", "UPDATE ONLY pg_catalog"}, "alias": {"pg_constraint SET", "pg_constraint AS target SET"},
		"literal_padding":        {"1 + (SELECT count(DISTINCT (retired.column1, retired.column6, retired.column7)) FROM (VALUES " + retiredRow("n", "0:7", "77") + ") AS retired)", "2"},
		"count_all":              {"count(DISTINCT (retired.column1, retired.column6, retired.column7))", "count(*)"},
		"count_filter":           {countSQL, countSQL + " FILTER (WHERE true)"},
		"count_order":            {countSQL, "count(DISTINCT " + identitySQL + " ORDER BY retired.column1)"},
		"count_window":           {countSQL, countSQL + " OVER ()"},
		"count_within":           {countSQL, "count(" + identitySQL + ") WITHIN GROUP (ORDER BY retired.column1)"},
		"count_variadic":         {countSQL, "count(VARIADIC " + identitySQL + ")"},
		"count_schema":           {countSQL, "pg_catalog." + countSQL},
		"count_foreign_schema":   {countSQL, "public." + countSQL},
		"count_extra_arguments":  {countSQL, "count(DISTINCT " + identitySQL + ", retired.column1)"},
		"count_other_aggregate":  {countSQL, "sum(DISTINCT " + identitySQL + ")"},
		"count_all_modifier":     {countSQL, "count(ALL " + identitySQL + ")"},
		"count_without_distinct": {countSQL, "count(" + identitySQL + ")"},
		"count_explicit_row":     {countSQL, "count(DISTINCT ROW(retired.column1, retired.column6, retired.column7))"},
		"count_scalar_identity":  {countSQL, "count(DISTINCT retired.column1)"},
		"count_extra_identity":   {countSQL, "count(DISTINCT (retired.column1, retired.column6, retired.column7, retired.column2))"},
		"payload_identity":       {"retired.column7)", "retired.column2)"}, "alias_columns": {"AS retired)", "AS retired(x))"},
		"missing_field": {"'0:7', '77'", "'0:7'"}, "extra_field": {"'0:7', '77'", "'0:7', '77', 'extra'"},
		"zero_rank": {"'a', 1, 'n'", "'a', 0, 'n'"}, "large_rank": {"'a', 1, 'n'", "'a', 1601, 'n'"},
		"wrong_kind": {"1, 'n'", "1, 'f'"}, "mixed_kind": {"1, 'n'", "1, 'c'"},
		"empty_column": {"'a', 1", "'', 1"}, "empty_name": {"'n', 'nn'", "'n', ''"},
		"bad_namespace": {"'0:7'", "'00:7'"}, "zero_lifetime": {"'0:7'", "'0:0'"}, "bad_lifetime": {"'0:7'", "'0:07'"},
		"zero_oid": {"'77'", "'0'"}, "noncanonical_oid": {"'77'", "'00077'"}, "overflow_oid": {"'77'", "'4294967296'"},
		"parameter": {"'77'", "$1"}, "unqualified_parent": {"'public.p'::regclass", "'p'::regclass"},
		"before_count": {"AND coninhcount = 1", "AND coninhcount = 2"}, "empty_key": {"ARRAY[(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = 'public.c'::regclass AND attname = 'a')]", "ARRAY[]"},
		"foreign_key_relation": {"attrelid = 'public.c'", "attrelid = 'public.p'"}, "key_bounds": {"AND attname = 'a')]", "AND attname = 'a' LIMIT 1)]"},
		"or": {"AND conname", "OR conname"}, "edge_child": {"inhrelid = 'public.c'", "inhrelid = 'public.q'"},
		"edge_parent": {"inhparent = 'public.p'", "inhparent = 'public.q'"}, "edge_target": {"pg_catalog.pg_inherits", "public.pg_inherits"},
		"extra_assignment": {"SET conislocal", "SET convalidated = true, conislocal"},
	} {
		t.Run(name, func(t *testing.T) {
			bad := strings.Replace(good, change[0], change[1], 1)
			if bad == good {
				t.Fatal("ineffective negative mutation", name)
			}
			if _, err := restoreRetiredSyntax(retiredRaw(t, bad)); err == nil {
				t.Fatal("noncanonical carrier admitted", name)
			}
		})
	}
	// SQL grammar forbids combining DISTINCT with WITHIN and does not retain
	// DISTINCT for star/VARIADIC calls. Exercise each private AST guard also
	// against an otherwise exact, genuinely parsed DISTINCT carrier so a
	// missing modifier gate cannot hide behind those other shape differences.
	for name, mutate := range map[string]func(*parser.AggregateExpr){
		"star":            func(call *parser.AggregateExpr) { call.Star = true },
		"variadic":        func(call *parser.AggregateExpr) { call.Variadic = true },
		"within":          func(call *parser.AggregateExpr) { call.Within = true },
		"within_position": func(call *parser.AggregateExpr) { call.WithinPos = call.Pos },
		"filter_position": func(call *parser.AggregateExpr) { call.FilterPos = call.Pos },
	} {
		t.Run("aggregate_ast_"+name, func(t *testing.T) {
			raw := retiredRaw(t, good)
			sum := raw.Assignments[1].Value.(parser.OperatorExpr)
			sub := sum.Args[1].(parser.SubqueryExpr)
			call := sub.Query.Targets[0].(parser.AggregateExpr)
			mutate(&call)
			sub.Query.Targets[0] = call
			if _, err := restoreRetiredSyntax(raw); err == nil {
				t.Fatal("noncanonical modifier admitted on exact DISTINCT aggregate", name)
			}
		})
	}
	for name, q := range map[string]string{"returning": good + " RETURNING conname", "extra_predicate": good + " AND true", "from": strings.Replace(good, " WHERE conrelid", " FROM public.p WHERE conrelid", 1), "cte": "WITH unused AS(SELECT 1) " + good} {
		t.Run(name, func(t *testing.T) {
			if _, err := restoreRetiredSyntax(retiredRaw(t, q)); err == nil {
				t.Fatal("unbounded canonical syntax admitted")
			}
		})
	}
	for _, before := range []int{-1, 32767, 32768} {
		bounded := retiredRepairSQL("c", "nn", "n", false, false, before, retiredRow("n", "0:7", "77"))
		if _, err := restoreRetiredSyntax(retiredRaw(t, bounded)); err == nil {
			t.Fatal("out-of-range contribution total admitted", before)
		}
	}
	// CHECK and remapped archival namespaces share the same exact grammar.
	check := retiredRepairSQL("c", "ck", "c", false, false, 1, retiredRow("c", "91:7", "78"))
	if decoded, err := restoreRetiredSyntax(retiredRaw(t, check)); err != nil || !decoded.check || decoded.rows[0].archive != 91 {
		t.Fatal("canonical CHECK/archive rejected", decoded, err)
	}
}

func retiredImportFixture(t *testing.T) (*Engine, *Operation, *restoredHistoryAuthority) {
	t.Helper()
	source := New()
	ownershipUnitExec(t, source, "CREATE ROLE history_reader LOGIN")
	e := source.restoreStage()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	// p.b is rank 1, occupying the archived source's old rank with another name.
	// Genuine q supplies the only current a/NN/CHECK provider.
	for _, q := range []string{
		"CREATE TABLE public.p(b INT)",
		"CREATE TABLE public.q(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))",
		"CREATE TABLE public.c() INHERITS(public.p,public.q)",
		"CREATE TABLE public.d() INHERITS(public.p,public.q)",
		"CREATE TABLE public.f() INHERITS(public.p,public.q)",
		"CREATE TABLE public.unrelated(a INT CONSTRAINT nn NOT NULL)",
	} {
		roleExecute(t, e, op, q)
	}
	a := &restoredHistoryAuthority{facts: make(map[ids.OID]restoredHistoricalFact), archives: make(map[uint64]uint64), sources: make(map[retiredSourceIdentity]retiredSourceDescription), nextArchive: e.retiredArchiveHighWater()}
	refs := make(map[ids.ObjectAddress]ids.ObjectRef)
	for _, name := range []string{"c", "d", "f"} {
		r := retiredUnitTable(t, e, name)
		refs[r.address] = e.catalog.Domain.Ref(e.CatalogRead().Epoch(), r.address)
	}
	e.captureHistoricalFacts(refs, a.facts)
	return e, op, a
}
func retiredAuthorityClone(a *restoredHistoryAuthority) *restoredHistoryAuthority {
	return &restoredHistoryAuthority{facts: maps.Clone(a.facts), archives: maps.Clone(a.archives), sources: maps.Clone(a.sources), nextArchive: a.nextArchive}
}
func retiredAuthorityEqual(a, b *restoredHistoryAuthority) bool {
	return a.nextArchive == b.nextArchive && maps.Equal(a.archives, b.archives) && maps.Equal(a.sources, b.sources) && maps.EqualFunc(a.facts, b.facts, func(x, y restoredHistoricalFact) bool {
		return x.ref == y.ref && x.oid == y.oid && x.name == y.name && x.check == y.check && slices.Equal(x.attributes, y.attributes) && sameGroupExpression(x.expression, y.expression) && x.notValid == y.notValid && x.noInherit == y.noInherit && x.local == y.local && x.count == y.count
	})
}
func retiredResourceCounts(e *Engine) [7]int {
	e.mu.Lock()
	counts := [7]int{len(e.visibility), len(e.indexBuildWaits), len(e.exclusionPending)}
	e.mu.Unlock()
	lockState := e.locks.Inspect()
	counts[3] = len(lockState.Owners)
	counts[4] = len(lockState.Sessions)
	counts[5] = len(lockState.Resources)
	counts[6] = lockState.KeyBuckets
	return counts
}
func retiredReject(t *testing.T, e *Engine, op *Operation, q string, a *restoredHistoryAuthority) {
	t.Helper()
	before := e.catalog.state
	stores := maps.Clone(e.storage)
	capture := retiredAuthorityClone(a)
	resources := retiredResourceCounts(e)
	if err := e.restoreRetiredConstraint(op, retiredRaw(t, q), a); err == nil {
		t.Fatal("forged repair gained authority", q)
	}
	if e.catalog.state != before || !reflect.DeepEqual(stores, e.storage) || !retiredAuthorityEqual(a, capture) || retiredResourceCounts(e) != resources {
		t.Fatal("rejected repair changed catalog/store/authority/resources")
	}
}

func TestRestoreConstraintHistoryAuthority(t *testing.T) {
	e, op, a := retiredImportFixture(t)
	q := retiredRepairSQL("c", "nn", "n", false, false, 1, retiredRow("n", "0:7", "77"))
	old := retiredUnitTable(t, e, "c")
	oid := old.NotNullConstraints[0].OID
	for name, mutate := range map[string]func(*restoredHistoricalFact){
		"epoch": func(f *restoredHistoricalFact) { f.ref.Epoch++ }, "owner": func(f *restoredHistoricalFact) { f.ref = New().catalog.Domain.Ref(f.ref.Epoch, f.ref.Address) },
		"oid": func(f *restoredHistoricalFact) { f.oid++ }, "name": func(f *restoredHistoricalFact) { f.name = "wrong" },
		"kind": func(f *restoredHistoricalFact) { f.check = true }, "key": func(f *restoredHistoricalFact) { f.attributes = []int32{1} },
		"validity": func(f *restoredHistoricalFact) { f.notValid = true }, "noinherit": func(f *restoredHistoricalFact) { f.noInherit = true },
		"before_local": func(f *restoredHistoricalFact) { f.local = true }, "before_count": func(f *restoredHistoricalFact) { f.count++ },
	} {
		t.Run(name, func(t *testing.T) {
			bad := retiredAuthorityClone(a)
			f := bad.facts[oid]
			mutate(&f)
			bad.facts[oid] = f
			retiredReject(t, e, op, q, bad)
		})
	}
	missing := retiredAuthorityClone(a)
	delete(missing.facts, oid)
	retiredReject(t, e, op, q, missing)
	exhausted := retiredAuthorityClone(a)
	exhausted.nextArchive = ^uint64(0)
	retiredReject(t, e, op, q, exhausted)
	for _, bad := range []string{
		strings.Replace(q, "conname = 'nn'", "conname = 'missing'", 1),
		strings.Replace(q, "attname = 'a'", "attname = 'b'", 1),
		strings.ReplaceAll(q, "public.c", "public.unrelated"),
		strings.ReplaceAll(q, "public.p", "public.unrelated"),
		strings.Replace(q, "AND conislocal = false", "AND conislocal = true", 1),
		retiredRepairSQL("c", "nn", "n", false, false, 1, retiredRow("n", "0:7", "77")+", "+retiredRow("n", "0:7", "77")),
	} {
		retiredReject(t, e, op, bad, a)
	}
	targets := e.importTargets
	e.importTargets = nil
	retiredReject(t, e, op, q, a)
	e.importTargets = targets
	foreign := New()
	other, done := roleOperation(t, foreign, "postgres")
	defer done()
	retiredReject(t, e, other, q, a)
	copied := *op
	retiredReject(t, e, &copied, q, a)
	expired, expiredDone := roleOperation(t, e, "postgres")
	expiredDone()
	retiredReject(t, e, expired, q, a)
	reader, readerDone := roleOperation(t, e, "history_reader")
	defer readerDone()
	retiredReject(t, e, reader, q, a)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION history_reader")
	retiredReject(t, e, op, q, a)
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "CREATE TEMP TABLE history_temp() INHERITS(public.p,public.q)")
	temp := retiredUnitTable(t, e, "history_temp")
	tempRef := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), temp.address)
	tempFacts := make(map[ids.OID]restoredHistoricalFact)
	captureHistoricalRelation(e.CatalogRead(), tempRef, tempFacts)
	if len(tempFacts) != 0 {
		t.Fatal("TEMP facts entered permanent logical-import pairing")
	}
	badTemp := retiredAuthorityClone(a)
	tempFact := badTemp.facts[oid]
	tempFact.ref = tempRef
	tempFact.oid = temp.NotNullConstraints[0].OID
	badTemp.facts[tempFact.oid] = tempFact
	tempSQL := strings.ReplaceAll(q, "public.c", "pg_temp.history_temp")
	retiredReject(t, e, op, tempSQL, badTemp)
	// The public executor still cannot borrow the private exact-import authority.
	beforePublic := e.catalog.state
	if _, err := e.Execute(op, retiredRaw(t, q), 0, nil); err == nil || e.catalog.state != beforePublic {
		t.Fatal("public catalog UPDATE gained historical repair authority", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled, cancelDone, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelDone()
	cancel()
	before := e.catalog.state
	if err := e.restoreRetiredConstraint(canceled, retiredRaw(t, q), a); !errors.Is(err, context.Canceled) || e.catalog.state != before {
		t.Fatal("canceled importer repair published", err)
	}
	if err := e.restoreRetiredConstraint(op, retiredRaw(t, q), a); err != nil {
		t.Fatal("actual paired NN repair rejected", err)
	}
	current := retiredUnitTable(t, e, "c")
	p := current.Inheritance.NotNull[current.NotNullConstraints[0].Attribute]
	if p.Local || p.count() != 2 || len(p.Origins) != 1 || len(p.Retired) != 1 || p.Retired[0].Archive == 0 || !p.Retired[0].Replayed || p.Retired[0].Attribute != 1 || p.Retired[0].Parent.Address != retiredUnitTable(t, e, "p").address {
		t.Fatal("archival rank was treated as current b/live identity", p)
	}
	if !reflect.DeepEqual(current.Columns, old.Columns) || !reflect.DeepEqual(current.NotNullConstraints, old.NotNullConstraints) || !reflect.DeepEqual(current.CheckConstraints, old.CheckConstraints) || current.revision != old.revision+1 || len(old.Inheritance.NotNull[current.NotNullConstraints[0].Attribute].Retired) != 0 {
		t.Fatal("repair changed anything beyond lineage/locality/revision")
	}
	retiredReject(t, e, op, q, a)
	// One old fact can support siblings and both repeated canonical repairs share
	// the namespace mapping. Conflicting descriptions for that identity cannot.
	sibling := retiredRepairSQL("d", "nn", "n", false, false, 1, retiredRow("n", "0:7", "77"))
	for _, change := range [][2]string{
		{"'a', 1, 'n'", "'wrong', 1, 'n'"},
		{"'a', 1, 'n'", "'a', 2, 'n'"},
		{"'n', 'nn'", "'n', 'wrong'"},
	} {
		retiredReject(t, e, op, strings.Replace(sibling, change[0], change[1], 1), a)
	}
	if err := e.restoreRetiredConstraint(op, retiredRaw(t, sibling), a); err != nil {
		t.Fatal("consistent shared source rejected", err)
	}
	d := retiredUnitTable(t, e, "d")
	dh := d.Inheritance.NotNull[d.NotNullConstraints[0].Attribute].Retired[0]
	if dh.Archive != p.Retired[0].Archive {
		t.Fatal("one imported source namespace mapped inconsistently")
	}
	// Distinct source epochs/OIDs remain distinct even if their numeric old OID
	// happens to equal a live provider. Archive namespace prevents live aliasing.
	liveQ := retiredUnitTable(t, e, "q").NotNullConstraints[0].OID
	check := retiredRepairSQL("c", "ck", "c", false, false, 1, retiredRow("c", "0:7", "78"))
	checkOID := old.CheckConstraints[0].OID
	for name, mutate := range map[string]func(*restoredHistoricalFact){
		"recipe":   func(f *restoredHistoricalFact) { f.expression = nil },
		"validity": func(f *restoredHistoricalFact) { f.notValid = true },
	} {
		t.Run("check_"+name, func(t *testing.T) {
			bad := retiredAuthorityClone(a)
			fact := bad.facts[checkOID]
			mutate(&fact)
			bad.facts[checkOID] = fact
			retiredReject(t, e, op, check, bad)
		})
	}
	if err := e.restoreRetiredConstraint(op, retiredRaw(t, check), a); err != nil {
		t.Fatal(err)
	}
	ck := retiredUnitTable(t, e, "c").CheckConstraints[0]
	saved := a.facts[retiredUnitTable(t, e, "f").NotNullConstraints[0].OID]
	distinct := retiredRepairSQL("f", "nn", "n", false, false, 1, retiredRow("n", "0:8", fmt.Sprint(liveQ))+", "+retiredRow("n", "0:9", fmt.Sprint(liveQ)))
	if err := e.restoreRetiredConstraint(op, retiredRaw(t, distinct), a); err != nil {
		t.Fatal("archival identities falsely aliased current OID namespace", err)
	}
	f := retiredUnitTable(t, e, "f")
	fp := f.Inheritance.NotNull[saved.attributes[0]]
	if fp.count() != 3 || len(fp.Retired) != 2 || fp.Retired[0].Lifetime == fp.Retired[1].Lifetime || fp.Retired[0].Archive != dh.Archive {
		t.Fatal("distinct archived lifetimes collapsed", fp)
	}
	if ck.NotValid || ck.NoInherit || ck.OID != old.CheckConstraints[0].OID {
		t.Fatal("CHECK repair changed validity/kind/identity")
	}
	if err := validateInheritance(e.CatalogRead(), f); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreConstraintHistoryAtomicity(t *testing.T) {
	e, op := retiredRuntimeFixture(t)
	roleExecute(t, e, op, "INSERT INTO c(a,b) VALUES(2,20)")
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	before := e.catalog.state
	stores := maps.Clone(e.storage)
	rows := make(map[ids.ObjectAddress][]rowVersion)
	resources := retiredResourceCounts(e)
	for address, store := range stores {
		rows[address] = slices.Clone(store.versions)
	}
	for name, stream := range map[string]string{
		"late_missing_relation": dump.String() + "; CREATE TABLE impossible() INHERITS(missing)",
		"late_enforcement":      dump.String() + "; INSERT INTO public.c(a,b) VALUES(0,20)",
	} {
		t.Run(name, func(t *testing.T) {
			if err := e.Restore(context.Background(), strings.NewReader(stream)); err == nil {
				t.Fatal("late invalid import succeeded")
			}
			if e.catalog.state != before || !reflect.DeepEqual(stores, e.storage) || retiredResourceCounts(e) != resources {
				t.Fatal("failed import replaced original catalog/store/resources")
			}
			for address, store := range stores {
				if !reflect.DeepEqual(rows[address], store.versions) {
					t.Fatal("failed import changed eager physical rows")
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Restore(ctx, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) || e.catalog.state != before || retiredResourceCounts(e) != resources {
		t.Fatal("canceled actual history stream replaced catalog/resources", err)
	}
	// Every actual redump/import receives fresh archival namespace authority, while
	// archived fields remain old and current references/own identities are remapped.
	var prior uint64
	for range 2 {
		if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		c := retiredUnitTable(t, e, "c")
		h := c.Inheritance.NotNull[c.NotNullConstraints[0].Attribute].Retired[0]
		if h.Archive <= prior || h.Archive == 0 || h.Parent.Epoch != e.CatalogRead().Epoch() || h.Own != c.NotNullConstraints[0].OID || !h.Replayed {
			t.Fatal("redump/import reused archive/live namespace", h)
		}
		prior = h.Archive
		dump.Reset()
		if err := e.Dump(context.Background(), &dump); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRestoreConstraintHistoryPrimarySupport(t *testing.T) {
	e, op, a := retiredImportFixture(t)
	roleExecute(t, e, op, "ALTER TABLE ONLY d ADD CONSTRAINT d_pk PRIMARY KEY(a)")
	d := retiredUnitTable(t, e, "d")
	oid := d.NotNullConstraints[0].OID
	ref := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), d.address)
	captureHistoricalRelation(e.CatalogRead(), ref, a.facts)
	before := a.facts[oid]
	q := retiredRepairSQL("d", "nn", "n", false, before.local, before.count, retiredRow("n", "0:7", "77"))
	if err := e.restoreRetiredConstraint(op, retiredRaw(t, q), a); err != nil {
		t.Fatal("ordinary validated NN with genuine primary support rejected", err)
	}
	current := retiredUnitTable(t, e, "d")
	if current.NotNullConstraints[0].OID != oid || current.NotNullConstraints[0].NoInherit || current.NotNullConstraints[0].NotValid || len(current.UniqueConstraints) != 1 || !current.UniqueConstraints[0].Primary {
		t.Fatal("history repair borrowed NO INHERIT prohibition or damaged real primary enforcement")
	}
	if err := validateInheritance(e.CatalogRead(), current); err != nil {
		t.Fatal(err)
	}
}
