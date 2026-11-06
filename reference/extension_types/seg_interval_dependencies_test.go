package reference_test

import (
	"bytes"
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const segIntervalDependencyProcedure = "'public.seg_overlap(public.seg,public.seg)'::regprocedure::oid"
const segIntervalDependencyOperator = "'public.&&(public.seg,public.seg)'::regoperator::oid"
const segIntervalDependencyIdentities = "SELECT 'public.seg'::regtype::oid::bigint," + segIntervalDependencyProcedure + "::bigint," + segIntervalDependencyOperator + "::bigint,'public.seg_interval_dependency_view'::regclass::oid::bigint,'public.seg_interval_dependency_data'::regclass::oid::bigint,(SELECT oid::bigint FROM pg_extension WHERE extname='seg')"

func segIntervalDependencySetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE seg_interval_dependency_data(id integer NOT NULL,a text,b text)", 0),
		fixture.Exec("INSERT INTO seg_interval_dependency_data VALUES (1,'0 .. 1','1 .. 2'),(2,'0 .. 1','2 .. 3'),(3,NULL,NULL)", 3),
		fixture.Exec("CREATE VIEW seg_interval_dependency_view AS SELECT id,seg_overlap(a::seg,b::seg) AS via_function,a::seg && b::seg AS via_operator FROM seg_interval_dependency_data", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
}

func segIntervalDependencyEdges() []fixture.Step {
	rewrite := "(SELECT oid FROM pg_rewrite WHERE ev_class='public.seg_interval_dependency_view'::regclass::oid AND rulename='_RETURN')"
	extension := "(SELECT oid FROM pg_extension WHERE extname='seg')"
	var steps []fixture.Step
	for _, edge := range []struct{ class, object, referenceClass, reference, kind string }{
		{"2618", rewrite, "1247", "'public.seg'::regtype::oid", "n"},
		{"2618", rewrite, "1255", segIntervalDependencyProcedure, "n"},
		{"2618", rewrite, "2617", segIntervalDependencyOperator, "n"},
		{"2617", segIntervalDependencyOperator, "1255", segIntervalDependencyProcedure, "n"},
		{"1255", segIntervalDependencyProcedure, "3079", extension, "e"},
		{"2617", segIntervalDependencyOperator, "3079", extension, "e"},
		{"1247", "'public.seg'::regtype::oid", "3079", extension, "e"},
	} {
		steps = append(steps, fixture.Select("SELECT deptype,objsubid,refobjsubid FROM pg_depend WHERE classid="+edge.class+" AND objid="+edge.object+" AND refclassid="+edge.referenceClass+" AND refobjid="+edge.reference, []string{"deptype", "objsubid", "refobjsubid"}, []string{"CHAR", "INT4", "INT4"}, [][]any{{edge.kind, int64(0), int64(0)}}))
	}
	return steps
}

func segIntervalDependencyRead(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) [6]int64 {
	t.Helper()
	segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT id,via_function,via_operator FROM seg_interval_dependency_view ORDER BY id", []string{"id", "via_function", "via_operator"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), false, false}, {int64(3), nil, nil}}, true))
	segIntervalBoth(t, p, b, c, fixture.Select("SELECT id,a,b FROM seg_interval_dependency_data ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "0 .. 1", "1 .. 2"}, {int64(2), "0 .. 1", "2 .. 3"}, {int64(3), nil, nil}}))
	for _, step := range segIntervalDependencyEdges() {
		segIntervalBoth(t, p, b, c, step)
	}
	ids := segIntervalDependencyIDs(t, p, c)
	// This is the complete set of this rule's normal references to genuine
	// seg-owned members, not just three individually filtered positive edges.
	// REL_18_6 dependency.c records CoerceViaIO's result type, not a hidden
	// I/O procedure. The explicit call and operator supply the other two.
	rewrite := "(SELECT oid FROM pg_rewrite WHERE ev_class='public.seg_interval_dependency_view'::regclass::oid AND rulename='_RETURN')"
	extension := "(SELECT oid FROM pg_extension WHERE extname='seg')"
	candidates := "SELECT d.refclassid::bigint,d.refobjid::bigint,d.objsubid,d.refobjsubid FROM pg_depend d " +
		"JOIN pg_depend m ON m.classid=d.refclassid AND m.objid=d.refobjid " +
		"WHERE d.classid=2618 AND d.objid=" + rewrite + " AND d.deptype='n' " +
		"AND m.deptype='e' AND m.refclassid=3079 AND m.refobjid=" + extension +
		" ORDER BY d.refclassid,d.refobjid"
	segIntervalBoth(t, p, b, c, fixture.Select(candidates, []string{"refclassid", "refobjid", "objsubid", "refobjsubid"}, []string{"INT8", "INT8", "INT4", "INT4"}, [][]any{
		{int64(1247), ids[0], int64(0), int64(0)},
		{int64(1255), ids[1], int64(0), int64(0)},
		{int64(2617), ids[2], int64(0), int64(0)},
	}))
	return ids
}

func segIntervalDependencyIDs(t *testing.T, p *fixture.Pair, c *sql.Conn) [6]int64 {
	t.Helper()
	var ids [6]int64
	if err := c.QueryRowContext(p.Ctx, segIntervalDependencyIdentities).Scan(&ids[0], &ids[1], &ids[2], &ids[3], &ids[4], &ids[5]); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id <= 0 {
			t.Fatal("invalid dependency object identity", ids)
		}
	}
	return ids
}

func segIntervalDependencyVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reminted bool) {
	t.Helper()
	before := segIntervalDependencyRead(t, p, b, c)
	var graph segIntervalDependencyGraph
	detail := "view seg_interval_dependency_view depends on operator &&(seg,seg)"
	if !reminted {
		// The genuine type/I/O/array construction boundary precedes every
		// selected predicate/operator. Preserve the fixed released parent.
		if !(before[0] < before[1] && before[1] < before[2]) {
			t.Fatal("fresh selected declarations lost released identity order", before)
		}
		for _, step := range segIntervalConstructionChecks("public") {
			segIntervalBoth(t, p, b, c, step)
		}
	} else {
		// Selective native remint is not order-preserving. Assert the complete
		// released provider graph before predicting its first-reached rule.
		graph = segIntervalDependencyGraphRead(t, p, b, c, before)
		detail = "view seg_interval_dependency_view depends on " + graph.firstRuleParent(t)
	}
	// REL_18_6 dependency.c protects EXTENSION members even for an individual
	// DROP, independently of the normal stored-consumer edges asserted above.
	for _, failure := range []struct{ sql, message, detail, hint string }{
		{"DROP FUNCTION public.seg_overlap(public.seg,public.seg) RESTRICT", "cannot drop function seg_overlap(seg,seg) because extension seg requires it", "", "You can drop extension seg instead."},
		{"DROP OPERATOR public.&& (public.seg,public.seg) RESTRICT", "cannot drop operator &&(seg,seg) because extension seg requires it", "", "You can drop extension seg instead."},
		{"DROP EXTENSION seg RESTRICT", "cannot drop extension seg because other objects depend on it", detail, "Use DROP ... CASCADE to drop the dependent objects too."},
	} {
		want := fixture.ControlsReferenceDiagnostic("2BP01", failure.message)
		want.Detail = failure.detail
		want.Hint = failure.hint
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic(failure.sql, want)
			step.Prepared = prepared
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			if after := segIntervalDependencyIDs(t, p, c); after != before {
				t.Fatal("failed DROP changed selected catalog identities", before, after)
			}
			segIntervalDependencyRead(t, p, b, c)
			if !reminted {
				for _, step := range segIntervalConstructionChecks("public") {
					segIntervalBoth(t, p, b, c, step)
				}
			}
			if reminted {
				if after := segIntervalDependencyGraphRead(t, p, b, c, before); after != graph {
					t.Fatal("failed DROP changed provider graph identities", graph, after)
				}
			}
		}
	}
}

func TestReferenceSegIntervalDependencies(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		segIntervalDependencySetup(t, p, b, c)
		segIntervalDependencyVerify(t, p, b, c, false)
		if b.PG {
			return
		} // Native replacement has no PostgreSQL control equivalent.
		original := segIntervalDependencyIDs(t, p, c)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		segIntervalDependencyVerify(t, p, b, c, false)
		fresh := fixture.NewFixture(t, false, true)
		owner, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		segIntervalRetainCollisions(t, fresh, owner, [3]int64{original[0], original[1], original[2]})
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		destination := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		remapped := segIntervalDependencyIDs(t, fresh, owner)
		for i := range 3 {
			if remapped[i] == original[i] {
				t.Fatal("selected type/procedure/operator not reminted", original, remapped)
			}
		}
		segIntervalDependencyVerify(t, fresh, destination, owner, true)
		var again bytes.Buffer
		if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatal("dependency restrictions or remint changed deterministic dump")
		}
	})
}

// This graph is specific to the represented SEG provider and its one view.
// Stock PostgreSQL has additional members; only the native independent-restore
// phase uses this cut of nineteen scalar declarations and their generated array.
type segIntervalDependencyAddress struct {
	class, object, sub int64
}

type segIntervalDependencyEdge struct {
	dependent, referenced segIntervalDependencyAddress
	kind                  string
}

type segIntervalDependencyGraph struct {
	extension, scalar, input, output, array, rule, view segIntervalDependencyAddress
	routines, operators                                 [8]segIntervalDependencyAddress
}

func (g segIntervalDependencyGraph) members() []segIntervalDependencyAddress {
	members := []segIntervalDependencyAddress{g.scalar, g.input, g.output}
	members = append(members, g.routines[:]...)
	return append(members, g.operators[:]...)
}

func (g segIntervalDependencyGraph) edges() []segIntervalDependencyEdge {
	// REL_18_6 pg_type.c GenerateTypeDependencies: callbacks and implicit
	// array; pg_proc.c ProcedureCreate: deduplicated signature types;
	// pg_operator.c makeOperatorDependencies: operand type and backer, not
	// commutator/negator. The view has exactly the three asserted normal refs.
	var edges []segIntervalDependencyEdge
	add := func(from, to segIntervalDependencyAddress, kind string) {
		edges = append(edges, segIntervalDependencyEdge{from, to, kind})
	}
	for _, member := range g.members() {
		add(member, g.extension, "e")
	}
	// AdmitInstallationCompanions gives the genuine array e membership after
	// sealing the nineteen scalar recipe roles; it is not another recipe slot.
	add(g.array, g.extension, "e")
	add(g.scalar, g.input, "n")
	add(g.scalar, g.output, "n")
	add(g.input, g.scalar, "n")
	add(g.output, g.scalar, "n")
	for i, routine := range g.routines {
		add(routine, g.scalar, "n")
		add(g.operators[i], g.scalar, "n")
		add(g.operators[i], routine, "n")
	}
	add(g.array, g.scalar, "i")
	add(g.rule, g.scalar, "n")
	add(g.rule, g.routines[5], "n")  // seg_overlap
	add(g.rule, g.operators[5], "n") // &&
	return edges                     // 52 edges: 20 e, 31 n, 1 i.
}

func segIntervalDependencyGraphRead(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, ids [6]int64) segIntervalDependencyGraph {
	t.Helper()
	g := segIntervalDependencyGraph{
		extension: segIntervalDependencyAddress{3079, ids[5], 0},
		scalar:    segIntervalDependencyAddress{1247, ids[0], 0},
		input:     segIntervalDependencyAddress{class: 1255},
		output:    segIntervalDependencyAddress{class: 1255},
		array:     segIntervalDependencyAddress{class: 1247},
		rule:      segIntervalDependencyAddress{class: 2618},
		view:      segIntervalDependencyAddress{1259, ids[3], 0},
	}
	query := "SELECT 'public.seg_in(pg_catalog.cstring)'::regprocedure::oid::bigint," +
		"'public.seg_out(public.seg)'::regprocedure::oid::bigint," +
		"(SELECT typarray::bigint FROM pg_type WHERE oid='public.seg'::regtype::oid)," +
		"(SELECT oid::bigint FROM pg_rewrite WHERE ev_class='public.seg_interval_dependency_view'::regclass::oid AND rulename='_RETURN')"
	if err := c.QueryRowContext(p.Ctx, query).Scan(&g.input.object, &g.output.object, &g.array.object, &g.rule.object); err != nil {
		t.Fatal(err)
	}
	for i, name := range segIntervalRoutines {
		g.routines[i].class, g.operators[i].class = 1255, 2617
		query = "SELECT " + queryIntLiteral("public."+name+"(public.seg,public.seg)") + "::regprocedure::oid::bigint," +
			queryIntLiteral("public."+segIntervalOperators[i]+"(public.seg,public.seg)") + "::regoperator::oid::bigint"
		if err := c.QueryRowContext(p.Ctx, query).Scan(&g.routines[i].object, &g.operators[i].object); err != nil {
			t.Fatal(err)
		}
	}
	if g.routines[5].object != ids[1] || g.operators[5].object != ids[2] {
		t.Fatal("selected consumer identities do not match provider roles", ids, g)
	}
	targets := append(g.members(), g.extension, g.array)
	seen := make(map[segIntervalDependencyAddress]bool)
	for _, address := range append(append([]segIntervalDependencyAddress{}, targets...), g.rule, g.view) {
		// Full addresses permit genuine cross-catalog OID collisions.
		if address.object <= 0 || seen[address] {
			t.Fatal("invalid or repeated provider graph address", address, g)
		}
		seen[address] = true
	}
	number := func(id int64) string { return strconv.FormatInt(id, 10) }
	// Reuse S1's exact signatures, C/source, strictness, namespaces, owners,
	// operator backers and genuine selected membership, not dependency names.
	for _, step := range segIntervalCatalogChecks("public") {
		segIntervalBoth(t, p, b, c, step)
	}
	owner := "(SELECT extowner FROM pg_extension WHERE extname='seg')"
	segIntervalBoth(t, p, b, c, fixture.Select(
		"SELECT typname,typtype,typnamespace='public'::regnamespace::oid AS namespace,typowner="+owner+" AS owner,typinput::oid::bigint AS input,typoutput::oid::bigint AS output,typarray::bigint FROM pg_type WHERE oid="+number(g.scalar.object),
		[]string{"typname", "typtype", "namespace", "owner", "input", "output", "typarray"},
		[]string{"NAME", "CHAR", "BOOL", "BOOL", "INT8", "INT8", "INT8"},
		[][]any{{"seg", "b", true, true, g.input.object, g.output.object, g.array.object}}))
	segIntervalBoth(t, p, b, c, fixture.Select(
		"SELECT typname,typelem::bigint,typnamespace='public'::regnamespace::oid AS namespace,typowner="+owner+" AS owner FROM pg_type WHERE oid="+number(g.array.object),
		[]string{"typname", "typelem", "namespace", "owner"}, []string{"NAME", "INT8", "BOOL", "BOOL"},
		[][]any{{"_seg", g.scalar.object, true, true}}))
	for _, io := range []struct {
		address segIntervalDependencyAddress
		name    string
		result  int64
	}{{g.input, "seg_in", g.scalar.object}, {g.output, "seg_out", 2275}} {
		segIntervalBoth(t, p, b, c, fixture.Select(
			"SELECT proname,pronargs::integer AS nargs,prorettype::bigint AS result,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,prokind::text AS kind,prosrc,probin,prolang=(SELECT oid FROM pg_language WHERE lanname='c') AS language,pronamespace='public'::regnamespace::oid AS namespace,proowner="+owner+" AS owner FROM pg_proc WHERE oid="+number(io.address.object),
			[]string{"proname", "nargs", "result", "proisstrict", "volatility", "parallel", "kind", "prosrc", "probin", "language", "namespace", "owner"},
			[]string{"NAME", "INT4", "INT8", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"},
			[][]any{{io.name, int64(1), io.result, true, "i", "s", "f", io.name, "$libdir/seg", true, true, true}}))
	}
	segIntervalBoth(t, p, b, c, fixture.Select(
		"SELECT oid::bigint,ev_class::bigint,rulename FROM pg_rewrite WHERE ev_class="+number(g.view.object)+" AND rulename='_RETURN'",
		[]string{"oid", "ev_class", "rulename"}, []string{"INT8", "INT8", "NAME"},
		[][]any{{g.rule.object, g.view.object, "_RETURN"}}))
	segIntervalBoth(t, p, b, c, fixture.Select(
		"SELECT relkind,relnamespace='public'::regnamespace::oid AS namespace FROM pg_class WHERE oid="+number(g.view.object),
		[]string{"relkind", "namespace"}, []string{"CHAR", "BOOL"}, [][]any{{"v", true}}))

	columns := []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}
	types := []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}
	projection := "SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE "
	row := func(edge segIntervalDependencyEdge) []any {
		return []any{edge.dependent.class, edge.dependent.object, edge.dependent.sub, edge.referenced.class, edge.referenced.object, edge.referenced.sub, edge.kind}
	}
	var clauses []string
	for _, class := range []int64{3079, 1247, 1255, 2617} {
		var objects []string
		for _, address := range targets {
			if address.class == class {
				objects = append(objects, number(address.object))
			}
		}
		clauses = append(clauses, "(refclassid="+number(class)+" AND refobjid IN ("+strings.Join(objects, ",")+"))")
	}
	var want [][]any
	for _, edge := range g.edges() {
		want = append(want, row(edge))
	}
	// No dependent, deptype or subobject filter: unexpected members, consumers
	// and routes must fail complete multiplicity equality rather than vanish.
	step := fixture.Select(projection+strings.Join(clauses, " OR "), columns, types, want)
	step.Bag = true
	segIntervalBoth(t, p, b, c, step)
	// Assert the unique internal owner independently of the incoming cut.
	segIntervalBoth(t, p, b, c, fixture.Select(
		projection+"classid=2618 AND objid="+number(g.rule.object)+" AND deptype='i'",
		columns, types, [][]any{row(segIntervalDependencyEdge{g.rule, g.view, "i"})}))
	return g
}

func (g segIntervalDependencyGraph) firstRuleParent(t *testing.T) string {
	t.Helper()
	// Build only from the released, source-specified expected graph, after its
	// complete public equality check. Never learn traversal from actual rows.
	incoming := make(map[segIntervalDependencyAddress][]segIntervalDependencyEdge)
	for _, edge := range g.edges() {
		incoming[edge.referenced] = append(incoming[edge.referenced], edge)
	}
	for _, edges := range incoming {
		sort.Slice(edges, func(i, j int) bool {
			a, b := edges[i].dependent, edges[j].dependent
			// REL_18_6 dependency.c object_address_comparator.
			if a.object != b.object {
				return a.object > b.object
			}
			if a.class != b.class {
				return a.class < b.class
			}
			return uint32(a.sub) < uint32(b.sub)
		})
	}
	seen := make(map[segIntervalDependencyAddress]bool)
	var visit func(segIntervalDependencyAddress) (segIntervalDependencyAddress, bool)
	visit = func(address segIntervalDependencyAddress) (segIntervalDependencyAddress, bool) {
		if seen[address] {
			return segIntervalDependencyAddress{}, false
		}
		if address == g.array && !seen[g.scalar] {
			// A->T is internal even though A also belongs to E. Released
			// owner redirection enters T without stacking A; T can then
			// visit A through the already asserted internal edge.
			return visit(g.scalar)
		}
		seen[address] = true // Includes the genuine type/I/O cycles.
		for _, edge := range incoming[address] {
			if edge.dependent == g.rule {
				if edge.kind != "n" {
					t.Fatal("rule reached through unexpected dependency", edge)
				}
				// R internally belongs to V. Released traversal redirects to
				// V with this same caller, so this is V's diagnostic parent.
				return address, true
			}
			if parent, found := visit(edge.dependent); found {
				return parent, true
			}
		}
		return segIntervalDependencyAddress{}, false
	}
	// E is the sole original root; do not premark/flatten all e members.
	parent, found := visit(g.extension)
	if !found {
		t.Fatal("complete SEG graph did not reach the consumer rule", g)
	}
	switch parent {
	case g.scalar:
		return "type seg"
	case g.routines[5]:
		return "function seg_overlap(seg,seg)"
	case g.operators[5]:
		return "operator &&(seg,seg)"
	default:
		t.Fatal("unexpected first rule parent", parent)
		return ""
	}
}
