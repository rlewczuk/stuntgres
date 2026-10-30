package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Each disjoint value inventory keeps the complete genuine operation graph
// and both destinations under the original scenario deadline.
func ownershipLtreePathQuerySurvivor(t *testing.T, op ownershipLtreePathOperation, values []ownershipLtreePathValue, foreign bool, state, phase string) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
	fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := fixture.OwnershipRun(t, p, b, c)
		fixture.OwnershipSurvivorRoles(run)
		run(fixture.Exec("CREATE EXTENSION ltree SCHEMA eo_old", 0))
		// Real relocation must preserve the installed nominal/helper graph.
		run(fixture.Exec("ALTER EXTENSION ltree SET SCHEMA eo_new", 0))
		signature := op.signature(false)
		fixture.OwnershipSurvivorACL(run, signature)
		id := fixture.OwnershipID(t, p, c, "SELECT '"+signature+"'::regprocedure::bigint")
		invariant := strings.Replace(fixture.OwnershipSurvivorDeclaration(signature), "p.proname,", "p.oid::bigint,", 1)
		invariant = strings.Replace(invariant, "p.oid='"+signature+"'::regprocedure", "p.oid="+fixture.OwnershipNumber(id), 1)
		before := fixture.OwnershipSnapshot(t, p, c, invariant)
		types := []string{"ltree"}
		for _, typ := range op.types {
			if typ == "lquery" || typ == "ltxtquery" {
				types = append(types, typ)
			}
		}
		targets := []string{"FUNCTION " + signature}
		if op.operator != "" {
			targets = append(targets, "OPERATOR "+op.operatorSignature())
		}
		// Preserve both actual reciprocal scalar query operators and callees,
		// while each direction owns its disjoint value inventory.
		if op.operator == "~" || op.operator == "@" {
			for _, other := range ownershipLtreePathOperations() {
				if other.operator != op.operator || other.name == op.name {
					continue
				}
				targets = append(targets, "FUNCTION "+other.signature(false), "OPERATOR "+other.operatorSignature())
			}
		}
		for _, typ := range types {
			targets = append(targets, "TYPE eo_new."+typ)
		}
		// Retain real codec and selectivity callees through their normal edges,
		// not guessed helper names. The independent pg_type-based edge assertions
		// below must pass before this discovery can hide a missing dependency.
		checkCodecs := func() {
			var dependencies ownershipLtreeDependencies
			ownershipLtreePathCodecDependencies(t, p, c, &dependencies, types)
			dependencies.check(run)
		}
		checkCodecs()
		var subjects []string
		for _, target := range targets {
			if strings.HasPrefix(target, "TYPE ") {
				subjects = append(subjects, "(d.classid='pg_type'::regclass AND d.objid='"+strings.TrimPrefix(target, "TYPE ")+"'::regtype)")
			}
			if strings.HasPrefix(target, "OPERATOR ") {
				subjects = append(subjects, "(d.classid='pg_operator'::regclass AND d.objid='"+strings.TrimPrefix(target, "OPERATOR ")+"'::regoperator)")
			}
		}
		query := `SELECT DISTINCT p.oid::regprocedure::text FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_depend em ON em.classid='pg_proc'::regclass AND em.objid=p.oid AND em.refclassid='pg_extension'::regclass AND em.deptype='e' JOIN pg_extension e ON e.oid=em.refobjid WHERE d.refclassid='pg_proc'::regclass AND d.deptype='n' AND e.extname='ltree' AND (` + strings.Join(subjects, " OR ") + ")"
		rows, err := c.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		seen := make(map[string]bool)
		for _, target := range targets {
			seen[target] = true
		}
		for rows.Next() {
			var sig string
			if err := rows.Scan(&sig); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			target := "FUNCTION " + sig
			if !seen[target] {
				targets = append(targets, target)
				seen[target] = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		// Establish current owners before capturing the genuine graph. Membership,
		// rename and origin retirement below must not replace any of its objects.
		for _, typ := range types {
			run(fixture.Exec("ALTER TYPE eo_new."+typ+" OWNER TO eo_survivor_owner", 0))
		}
		for _, target := range targets {
			if strings.HasPrefix(target, "OPERATOR ") {
				run(fixture.Exec("ALTER "+target+" OWNER TO eo_survivor_owner", 0))
			}
		}
		var sourceQueries, helperQueries []string
		var sourceGraph, helpers [][][]any
		for _, target := range targets {
			var q string
			switch {
			case strings.HasPrefix(target, "TYPE "):
				identity := fixture.OwnershipID(t, p, c, "SELECT '"+strings.TrimPrefix(target, "TYPE ")+"'::regtype::bigint")
				q = `SELECT t.oid::bigint,t.typnamespace::bigint,t.typowner::bigint,t.typacl::text,t.typarray::bigint,t.typelem::bigint,t.typinput::bigint,t.typoutput::bigint,t.typreceive::bigint,t.typsend::bigint,t.typmodin::bigint,t.typmodout::bigint,t.typanalyze::bigint,t.typsubscript::bigint,t.typlen,t.typbyval,t.typalign,t.typstorage,t.typtype,t.typcategory FROM pg_type t WHERE t.oid=` + fixture.OwnershipNumber(identity) + ` OR t.oid=(SELECT typarray FROM pg_type WHERE oid=` + fixture.OwnershipNumber(identity) + `) ORDER BY t.oid`
			case strings.HasPrefix(target, "OPERATOR "):
				identity := fixture.OwnershipID(t, p, c, "SELECT '"+strings.TrimPrefix(target, "OPERATOR ")+"'::regoperator::bigint")
				q = `SELECT oid::bigint,oprnamespace::bigint,oprowner::bigint,oprleft::bigint,oprright::bigint,oprresult::bigint,oprcode::bigint,oprcom::bigint,oprnegate::bigint,oprrest::bigint,oprjoin::bigint,oprcanhash,oprcanmerge FROM pg_operator WHERE oid=` + fixture.OwnershipNumber(identity)
			case strings.HasPrefix(target, "FUNCTION "):
				sig := strings.TrimPrefix(target, "FUNCTION ")
				if sig == signature {
					continue
				}
				q = fixture.OwnershipSurvivorDeclaration(sig)
				helperQueries = append(helperQueries, q)
				helpers = append(helpers, fixture.OwnershipSnapshot(t, p, c, q))
				q = strings.Replace(q, "SELECT ", "SELECT p.oid::bigint,", 1)
			}
			sourceQueries = append(sourceQueries, q)
			snapshot := fixture.OwnershipSnapshot(t, p, c, q)
			if len(snapshot) == 0 {
				t.Fatal("missing pre-mutation graph", target)
			}
			sourceGraph = append(sourceGraph, snapshot)
		}
		type witness struct {
			held fixture.HeldStatement
			step fixture.Step
		}
		var witnesses []witness
		var directWitnesses []witness
		var stored []fixture.Step
		for index, value := range values {
			for _, operator := range []bool{false, true} {
				if operator && op.operator == "" {
					continue
				}
				s := op.step(value, false, operator, true)
				directWitnesses = append(directWitnesses, witness{fixture.CrosstabHeld(t, p, c, s.Query), s})
				// Prepare the stored identity before mutation: unlike original-name
				// SQL, this remains valid after ordinary PostgreSQL rename invalidation.
				literal := op.step(value, false, operator, false)
				view := fmt.Sprintf("eo_old.lp_%d_%t", index, operator)
				run(fixture.Exec("CREATE VIEW "+view+" AS "+literal.Query, 0))
				run(fixture.Exec("GRANT SELECT ON "+view+" TO eo_survivor_acl", 0))
				literal.Query = "SELECT * FROM " + view
				stored = append(stored, literal)
				witnesses = append(witnesses, witness{fixture.CrosstabHeld(t, p, c, literal.Query), literal})
			}
		}
		operand := ""
		if op.operator != "" {
			typ := ownershipLtreePathType(op.types[0])
			operand = "eo_old.lp_operand(" + typ + ")"
			run(fixture.Exec("CREATE FUNCTION eo_old.lp_operand(v "+typ+") RETURNS "+typ+" LANGUAGE SQL IMMUTABLE AS 'SELECT $1'", 0))
			// Keep an independent non-NULL nested occurrence in every inventory,
			// even when its disjoint values begin with a strict-NULL operand.
			nestedValue := op.cases[0]
			s := op.step(nestedValue, false, true, false)
			// Wrap the explicit left operand, inside any result-to-text cast.
			arg := "'" + strings.ReplaceAll(fmt.Sprint(nestedValue.args[0]), "'", "''") + "'::" + typ
			s.Query = strings.Replace(s.Query, arg, "eo_old.lp_operand("+arg+")", 1)
			run(fixture.Exec("CREATE VIEW eo_old.lp_nested AS "+s.Query, 0))
			run(fixture.Exec("GRANT SELECT ON eo_old.lp_nested TO eo_survivor_acl", 0))
			s.Query = "SELECT * FROM eo_old.lp_nested"
			stored = append(stored, s)
			witnesses = append(witnesses, witness{fixture.CrosstabHeld(t, p, c, s.Query), s})
		}
		fixture.OwnershipSurvivorMutation(t, p, c, run, "ltree", signature)
		for _, target := range targets {
			run(fixture.Exec("ALTER EXTENSION ltree DROP "+target, 0))
		}
		fixture.OwnershipSurvivorNonmember(run, "ltree", signature)
		checkCodecs()
		for _, w := range directWitnesses {
			s := w.step
			s.Prepared = false
			fixture.RunStep(t, p.Ctx, w.held, b.Recorder, s)
			if err := w.held.Statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, w := range witnesses {
			s := w.step
			s.Prepared = false
			fixture.RunStep(t, p.Ctx, w.held, b.Recorder, s)
		}
		run(fixture.Exec("ALTER FUNCTION "+signature+" RENAME TO eo_path_"+op.name, 0))
		signature = op.signature(true)
		targets[0] = "FUNCTION " + signature
		if got := fixture.OwnershipID(t, p, c, "SELECT '"+signature+"'::regprocedure::bigint"); got != id {
			t.Fatal("rename replaced installed procedure", got, id)
		}
		for _, target := range targets {
			run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD `+target, 0))
		}
		fixture.OwnershipSurvivorMemberRestriction(run, signature, "uuid-ossp")
		checkCodecs()
		run(fixture.Exec("DROP EXTENSION ltree", 0))
		if !foreign {
			for _, target := range targets {
				run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP `+target, 0))
			}
		}
		fixture.OwnershipUnchanged(t, p, c, invariant, before)
		checkCodecs()
		for i, q := range sourceQueries {
			fixture.OwnershipUnchanged(t, p, c, q, sourceGraph[i])
		}
		for _, w := range witnesses {
			s := w.step
			s.Prepared = false
			fixture.RunStep(t, p.Ctx, w.held, b.Recorder, s)
			if err := w.held.Statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
		var graphQueries []string
		for _, typ := range types {
			graphQueries = append(graphQueries, `SELECT n.nspname,t.typname,r.rolname,t.typacl::text,t.typelem::regtype::text,t.typarray::regtype::text,t.typinput::regprocedure::text,t.typoutput::regprocedure::text,t.typreceive::regprocedure::text,t.typsend::regprocedure::text,t.typmodin::regprocedure::text,t.typmodout::regprocedure::text,t.typanalyze::regprocedure::text,t.typsubscript::regprocedure::text,t.typlen,t.typbyval,t.typalign,t.typstorage,t.typtype,t.typcategory,an.nspname,a.typname,ar.rolname,a.typelem::regtype::text,a.typarray::regtype::text,a.typinput::regprocedure::text,a.typoutput::regprocedure::text,a.typreceive::regprocedure::text,a.typsend::regprocedure::text,a.typmodin::regprocedure::text,a.typmodout::regprocedure::text,a.typanalyze::regprocedure::text,a.typsubscript::regprocedure::text,a.typlen,a.typbyval,a.typalign,a.typstorage,a.typtype,a.typcategory,a.typacl::text FROM pg_type t JOIN pg_type a ON a.oid=t.typarray JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_roles r ON r.oid=t.typowner JOIN pg_namespace an ON an.oid=a.typnamespace JOIN pg_roles ar ON ar.oid=a.typowner WHERE t.oid='eo_new.`+typ+`'::regtype`)
		}
		for _, target := range targets {
			if !strings.HasPrefix(target, "OPERATOR ") {
				continue
			}
			operator := strings.TrimPrefix(target, "OPERATOR ")
			graphQueries = append(graphQueries, `SELECT n.nspname,o.oprname,r.rolname,o.oprleft::regtype::text,o.oprright::regtype::text,o.oprresult::regtype::text,o.oprcode::regprocedure::text,o.oprcom::regoperator::text,o.oprnegate::regoperator::text,o.oprrest::regproc::text,o.oprjoin::regproc::text,o.oprcanhash,o.oprcanmerge FROM pg_operator o JOIN pg_namespace n ON n.oid=o.oprnamespace JOIN pg_roles r ON r.oid=o.oprowner WHERE o.oid='`+operator+`'::regoperator`)
		}
		graph := make([][][]any, len(graphQueries))
		for i, q := range graphQueries {
			graph[i] = fixture.OwnershipSnapshot(t, p, c, q)
			if len(graph[i]) != 1 {
				t.Fatal("missing actual nominal/operator graph")
			}
		}
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			r := fixture.OwnershipRunWithRecorder(t, p, conn, recorder)
			calls := func() {
				for _, value := range values {
					for _, operator := range []bool{false, true} {
						if operator && op.operator == "" {
							continue
						}
						for _, prepared := range []bool{false, true} {
							r(op.step(value, true, operator, prepared))
						}
					}
				}
				for _, s := range stored {
					r(s)
					r(fixture.Prepared(s))
				}
			}
			calls()
			r(fixture.Exec("SET ROLE eo_survivor_acl", 0))
			calls()
			r(fixture.Exec("RESET ROLE", 0))
			r(fixture.OwnershipCount("SELECT count(*) FROM pg_extension WHERE extname='ltree'", 0))
			dependencies := &ownershipLtreeDependencies{}
			dependencies.links(r, signature, foreign)
			for i, q := range graphQueries {
				fixture.OwnershipUnchanged(t, p, conn, q, graph[i])
			}
			ownershipLtreePathDependencies(r, dependencies, op, signature, types, foreign)
			ownershipLtreePathCodecDependencies(t, p, conn, dependencies, types)
			for i, q := range helperQueries {
				fixture.OwnershipUnchanged(t, p, conn, q, helpers[i])
			}
			member := ""
			if foreign {
				member = "uuid-ossp"
			}
			for _, target := range targets {
				if !strings.HasPrefix(target, "FUNCTION ") {
					continue
				}
				sig := strings.TrimPrefix(target, "FUNCTION ")
				dependencies.member(r, "pg_proc", "'"+sig+"'::regprocedure", member)
			}
			if operand != "" {
				ownershipLtreeNestedDependencies(dependencies, "eo_old.lp_nested", operand, signature, op.operatorSignature())
			}
			dependencies.check(r)
			// Remove only the current e edge in a rolled-back transaction so
			// normal consumer restrictions are exercised even in the foreign case.
			r(fixture.Exec("BEGIN", 0))
			if foreign {
				r(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION `+signature, 0))
			}
			r(fixture.Error("DROP FUNCTION "+signature+" RESTRICT", "2BP01"))
			r(fixture.Exec("ROLLBACK", 0))
			restoredLinks := &ownershipLtreeDependencies{}
			restoredLinks.links(r, signature, foreign)
			restoredLinks.check(r)
			r(stored[0])
			r(fixture.Prepared(stored[0]))
		}
		var retainedRows []*sql.Rows
		if phase != "source" && !b.PG {
			// Capture one real source resource per destination before either reset.
			for range 2 {
				oldRows, err := c.QueryContext(p.Ctx, stored[0].Query)
				if err != nil {
					t.Fatal(err)
				}
				defer oldRows.Close()
				retainedRows = append(retainedRows, oldRows)
			}
		}
		var signatures []string
		for _, target := range targets {
			if strings.HasPrefix(target, "FUNCTION ") {
				signatures = append(signatures, strings.TrimPrefix(target, "FUNCTION "))
			}
		}
		if operand != "" {
			signatures = append(signatures, operand)
		}
		ownershipLtreeHierarchyControls(t, p, b, c, phase, state, signatures, check)
		for _, oldRows := range retainedRows {
			metadata, err := oldRows.ColumnTypes()
			if err != nil || len(metadata) != 1 || metadata[0].Name() != "v" || metadata[0].DatabaseTypeName() != stored[0].Types[0] {
				t.Fatal("retained path metadata", err)
			}
			if !oldRows.Next() {
				t.Fatal("missing retained path row", oldRows.Err())
			}
			var got any
			if err := oldRows.Scan(&got); err != nil {
				t.Fatal(err)
			}
			if _, ok := fixture.CompareRows([][]any{{fixture.Normalize(got)}}, stored[0].Rows, false); !ok {
				t.Fatal("retained path value", got, stored[0].Rows)
			}
			if oldRows.Next() || oldRows.Err() != nil {
				t.Fatal("retained path EOF", oldRows.Err())
			}
		}
	})
}

// PostgreSQL GenerateTypeDependencies records normal dependencies on unpinned
// input/output procedures. These installed scalar codecs are not pinned; array
// codecs are builtins and do not imply corresponding pg_depend rows.
func ownershipLtreePathCodecDependencies(t *testing.T, p *fixture.Pair, conn *sql.Conn, dependencies *ownershipLtreeDependencies, types []string) {
	t.Helper()
	for _, typ := range types {
		var identity, input, output int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint,typinput::bigint,typoutput::bigint FROM pg_type WHERE oid='eo_new."+typ+"'::regtype").Scan(&identity, &input, &output); err != nil {
			t.Fatal(err)
		}
		if identity == 0 || input == 0 || output == 0 || input == output {
			t.Fatal("missing distinct installed type codec identities", typ, identity, input, output)
		}
		// Expected targets come from the actual type declaration, independently
		// of the dependency rows and the helper-retention discovery query.
		scope := "classid='pg_type'::regclass AND objid=" + fixture.OwnershipNumber(identity) + " AND refclassid='pg_proc'::regclass AND refobjid IN (" + fixture.OwnershipNumber(input) + "," + fixture.OwnershipNumber(output) + ")"
		// Total cardinality plus both exact rows is the same finite edge bag:
		// duplicates, extra subobjects and wrong dependency kinds still fail.
		dependencies.count(scope, 2)
		for _, codec := range []int64{input, output} {
			dependencies.count(scope+" AND objsubid=0 AND refobjid="+fixture.OwnershipNumber(codec)+" AND refobjsubid=0 AND deptype='n'", 1)
		}
	}
}

func ownershipLtreePathDependencies(run func(fixture.Step), dependencies *ownershipLtreeDependencies, op ownershipLtreePathOperation, signature string, types []string, foreign bool) {
	result := op.result
	if result == "text" && op.name != "ltree2text" {
		result = "eo_new.ltree"
	}
	run(fixture.Select("SELECT prorettype='"+result+"'::regtype AS result,proisstrict,provolatile,proparallel,proretset,prosrc,probin FROM pg_proc WHERE oid='"+signature+"'::regprocedure", []string{"result", "proisstrict", "provolatile", "proparallel", "proretset", "prosrc", "probin"}, []string{"BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "TEXT", "TEXT"}, [][]any{{true, true, "i", "s", false, op.symbol, "$libdir/ltree"}}))
	for _, typ := range types {
		for index, identity := range []string{"'eo_new." + typ + "'::regtype", "(SELECT typarray FROM pg_type WHERE oid='eo_new." + typ + "'::regtype)"} {
			member := ""
			if foreign {
				member = "uuid-ossp"
			}
			dependencies.member(run, "pg_type", identity, member)
			run(fixture.OwnershipCount("SELECT count(*) FROM pg_type WHERE oid="+identity+" AND typowner=(SELECT oid FROM pg_roles WHERE rolname='eo_survivor_owner')", 1))
			dependencies.count("classid='pg_type'::regclass AND objid="+identity+" AND refclassid='pg_type'::regclass AND refobjid='eo_new."+typ+"'::regtype AND deptype='i'", int64(index))
		}
	}
	seen := make(map[string]bool)
	for _, typ := range op.types {
		if !strings.HasPrefix(typ, "ltree") && typ != "lquery" && typ != "ltxtquery" {
			continue
		}
		if seen[typ] {
			continue
		}
		seen[typ] = true
		dependencies.count("classid='pg_proc'::regclass AND objid='"+signature+"'::regprocedure AND refclassid='pg_type'::regclass AND refobjid='"+ownershipLtreePathType(typ)+"'::regtype AND deptype='n'", 1)
	}
	for _, operator := range []bool{false, true} {
		if operator && op.operator == "" {
			continue
		}
		view := fmt.Sprintf("eo_old.lp_0_%t", operator)
		for _, class := range []string{"pg_proc", "pg_operator"} {
			if class == "pg_operator" && op.operator == "" {
				continue
			}
			identity := "'" + signature + "'::regprocedure"
			if class == "pg_operator" {
				identity = "'" + op.operatorSignature() + "'::regoperator"
			}
			want := int64(0)
			if (class == "pg_operator") == operator {
				want = 1
			}
			ownershipLtreeViewDependency(dependencies, view, class, identity, want)
		}
	}
	if op.operator != "" {
		identity := "'" + op.operatorSignature() + "'::regoperator"
		run(fixture.OwnershipCount("SELECT count(*) FROM pg_operator WHERE oid="+identity+" AND oprcode='"+signature+"'::regprocedure", 1))
		dependencies.count("classid='pg_operator'::regclass AND objid="+identity+" AND refclassid='pg_proc'::regclass AND refobjid='"+signature+"'::regprocedure AND deptype='n'", 1)
		dependencies.count("classid='pg_operator'::regclass AND objid="+identity+" AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'", 1)
		member := ""
		if foreign {
			member = "uuid-ossp"
		}
		dependencies.member(run, "pg_operator", identity, member)
	}
}
