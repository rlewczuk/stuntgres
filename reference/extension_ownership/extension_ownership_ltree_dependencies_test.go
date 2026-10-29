package reference_test

import (
	"fmt"
	"strings"

	"stuntgres/reference/fixture"
)

// One batch belongs to one unchanged check stage on one actual destination.
// Each independently expected count remains a separate FILTER column, including
// zero counts. Nothing is retained across SQL mutations, Reset or Restore.
type ownershipLtreeDependencies struct {
	columns []string
	names   []string
	types   []string
	counts  []any
}

func (d *ownershipLtreeDependencies) count(predicate string, want int64) {
	name := fmt.Sprintf("edge_%d", len(d.columns))
	d.columns = append(d.columns, "count(*) FILTER (WHERE "+predicate+") AS "+name)
	d.names = append(d.names, name)
	d.types = append(d.types, "INT8")
	d.counts = append(d.counts, want)
}

func (d *ownershipLtreeDependencies) check(run func(fixture.Step)) {
	run(fixture.Select("SELECT "+strings.Join(d.columns, ",")+" FROM pg_depend", d.names, d.types, [][]any{d.counts}))
}

// Keep the original one-object cardinality witness, then assert the exact e-edge
// count and receiver separately in the same finite live dependency projection.
func (d *ownershipLtreeDependencies) member(run func(fixture.Step), catalog, identity, member string) {
	run(ownershipCount("SELECT count(*) FROM "+catalog+" WHERE oid="+identity, 1))
	predicate := "classid='" + catalog + "'::regclass AND objid=" + identity + " AND refclassid='pg_extension'::regclass AND deptype='e'"
	want := int64(0)
	if member != "" {
		want = 1
	}
	d.count(predicate, want)
	if member != "" {
		d.count(predicate+" AND refobjid=(SELECT oid FROM pg_extension WHERE extname='"+member+"')", 1)
	}
}

func (d *ownershipLtreeDependencies) links(run func(fixture.Step), signature string, foreign bool) {
	member := ""
	if foreign {
		member = "uuid-ossp"
	}
	identity := "'" + signature + "'::regprocedure"
	d.member(run, "pg_proc", identity, member)
	d.count("classid='pg_proc'::regclass AND objid="+identity+" AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'", 1)
	run(fixture.Select(`SELECT r.rolname,has_function_privilege('eo_survivor_acl',p.oid,'EXECUTE') AS allowed,p.proacl IS NOT NULL AS explicit_acl,has_function_privilege('public',p.oid,'EXECUTE') AS public_allowed FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='`+signature+`'::regprocedure`, []string{"rolname", "allowed", "explicit_acl", "public_allowed"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"eo_survivor_owner", true, true, false}}))
}

// PostgreSQL REL_18_6 dependency.c find_expr_references_walker records funcid
// for FuncExpr and opno for OpExpr, then visits their actual arguments. An
// operator's implementation procedure is a separate operator dependency, not
// an independently written function occurrence in its consuming view.
func ownershipLtreeViewDependency(d *ownershipLtreeDependencies, view, class, identity string, count int64) {
	d.count("classid='pg_rewrite'::regclass AND objid IN (SELECT oid FROM pg_rewrite WHERE ev_class='"+view+"'::regclass) AND refclassid='"+class+"'::regclass AND refobjid="+identity+" AND deptype='n'", count)
}

func ownershipLtreeCallDependencies(d *ownershipLtreeDependencies, directView, operatorView, signature, operator string) {
	procedure := "'" + signature + "'::regprocedure"
	op := "'" + operator + "'::regoperator"
	ownershipLtreeViewDependency(d, directView, "pg_proc", procedure, 1)
	ownershipLtreeViewDependency(d, operatorView, "pg_operator", op, 1)
	ownershipLtreeViewDependency(d, directView, "pg_operator", op, 0)
	ownershipLtreeViewDependency(d, operatorView, "pg_proc", procedure, 0)
}

func ownershipLtreeNestedDependencies(d *ownershipLtreeDependencies, view, operand, signature, operator string) {
	ownershipLtreeViewDependency(d, view, "pg_operator", "'"+operator+"'::regoperator", 1)
	ownershipLtreeViewDependency(d, view, "pg_proc", "'"+operand+"'::regprocedure", 1)
	ownershipLtreeViewDependency(d, view, "pg_proc", "'"+signature+"'::regprocedure", 0)
}
