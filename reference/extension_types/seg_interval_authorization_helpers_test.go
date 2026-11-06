package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const segIntervalMemberOwner = "seg_interval_member_owner"
const segIntervalAllowed = "seg_interval_allowed"
const segIntervalDenied = "seg_interval_denied"
const segIntervalAuthorizationQuery = "SELECT id,seg_overlap(a,b) AS via_function,a && b AS via_operator FROM seg_interval_authorization ORDER BY id"
const segIntervalAuthorizationIdentities = "SELECT 'public.seg'::regtype::oid::bigint,'public.seg_overlap(public.seg,public.seg)'::regprocedure::oid::bigint,'public.&&(public.seg,public.seg)'::regoperator::oid::bigint"

func segIntervalAuthorizationOwnerScript(t *testing.T, script string) string {
	t.Helper()
	start, end, data := fixture.CatalogExtensionOwnerDirective(t, script, "seg", true)
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, member := range members {
		if string(member["declaration"]) != "8" {
			continue
		}
		if changed || string(member["kind"]) != `"procedure"` || string(member["name"]) != `"seg_overlap"` || string(member["signature"]) != `"public.seg, public.seg"` {
			t.Fatal("actual selected overlap owner target", member)
		}
		member["owner"] = json.RawMessage(`"` + segIntervalMemberOwner + `"`)
		changed = true
	}
	if !changed {
		t.Fatal("missing genuine selected overlap owner declaration")
	}
	var err error
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return script[:start] + "-- stuntgres-extension-owners-v3 " + base64.StdEncoding.EncodeToString(data) + script[end:]
}

func segIntervalAuthorizationSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
	for _, role := range []string{segIntervalMemberOwner, segIntervalAllowed, segIntervalDenied} {
		run(fixture.Exec("CREATE ROLE "+role, 0))
	}
	run(fixture.Exec("CREATE TABLE seg_interval_authorization(id integer NOT NULL,a seg,b seg)", 0))
	run(fixture.Exec("INSERT INTO seg_interval_authorization VALUES (1,'0 .. 1','1 .. 2'),(2,NULL,NULL),(3,'0 .. 1','2 .. 3')", 3))
	if b.PG {
		run(fixture.Exec("ALTER FUNCTION public.seg_overlap(public.seg,public.seg) OWNER TO "+segIntervalMemberOwner, 0))
	} else {
		// Follow the existing public named-owner Restore seam. This exercises
		// genuine member reownership without introducing an ALTER implementation.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(segIntervalAuthorizationOwnerScript(t, dump.String()))); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		"GRANT USAGE ON SCHEMA public TO " + segIntervalMemberOwner + "," + segIntervalAllowed + "," + segIntervalDenied,
		"GRANT SELECT ON seg_interval_authorization TO " + segIntervalAllowed + "," + segIntervalDenied,
		"REVOKE EXECUTE ON FUNCTION public.seg_overlap(public.seg,public.seg) FROM PUBLIC," + segIntervalDenied,
		"GRANT EXECUTE ON FUNCTION public.seg_overlap(public.seg,public.seg) TO " + segIntervalAllowed,
	} {
		run(fixture.Exec(query, 0))
	}
}

func segIntervalAuthorizationCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	query := "SELECT r.rolname AS owner,p.proowner<>(SELECT extowner FROM pg_extension WHERE extname='seg') AS distinct_owner,p.proacl IS NOT NULL AS explicit_acl,has_function_privilege('public',p.oid,'EXECUTE') AS public_exec,has_function_privilege('" + segIntervalMemberOwner + "',p.oid,'EXECUTE') AS owner_exec,has_function_privilege('" + segIntervalAllowed + "',p.oid,'EXECUTE') AS allowed_exec,has_function_privilege('" + segIntervalDenied + "',p.oid,'EXECUTE') AS denied_exec FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='public.seg_overlap(public.seg,public.seg)'::regprocedure::oid"
	segIntervalBoth(t, p, b, c, fixture.Select(query, []string{"owner", "distinct_owner", "explicit_acl", "public_exec", "owner_exec", "allowed_exec", "denied_exec"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{segIntervalMemberOwner, true, true, false, true, true, false}}))
	segIntervalBoth(t, p, b, c, fixture.Select("SELECT rolname,rolsuper,rolcanlogin FROM pg_roles WHERE rolname IN ('"+segIntervalAllowed+"','"+segIntervalDenied+"','"+segIntervalMemberOwner+"') ORDER BY rolname", []string{"rolname", "rolsuper", "rolcanlogin"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{segIntervalAllowed, false, false}, {segIntervalDenied, false, false}, {segIntervalMemberOwner, false, false}}))
	segIntervalBoth(t, p, b, c, fixture.Select("SELECT proname,pronargs::integer AS nargs,prorettype::integer AS result,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,prosrc,probin,prolang=(SELECT oid FROM pg_language WHERE lanname='c') AS c_language FROM pg_proc WHERE oid='public.seg_overlap(public.seg,public.seg)'::regprocedure::oid", []string{"proname", "nargs", "result", "proisstrict", "volatility", "parallel", "prosrc", "probin", "c_language"}, []string{"NAME", "INT4", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"seg_overlap", int64(2), int64(16), true, "i", "s", "seg_overlap", "$libdir/seg", true}}))
	segIntervalBoth(t, p, b, c, fixture.Select("SELECT oprname,oprcode::oid='public.seg_overlap(public.seg,public.seg)'::regprocedure::oid AS backer,oprleft='public.seg'::regtype::oid AS left_type,oprright='public.seg'::regtype::oid AS right_type,oprowner=(SELECT extowner FROM pg_extension WHERE extname='seg') AS default_owner FROM pg_operator WHERE oid='public.&&(public.seg,public.seg)'::regoperator::oid", []string{"oprname", "backer", "left_type", "right_type", "default_owner"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"&&", true, true, true, true}}))
	var untouched []string
	for _, name := range segIntervalRoutines {
		if name != "seg_overlap" {
			untouched = append(untouched, queryIntLiteral("public."+name+"(public.seg,public.seg)")+"::regprocedure::oid")
		}
	}
	segIntervalBoth(t, p, b, c, fixture.Select("SELECT proname,proowner=(SELECT extowner FROM pg_extension WHERE extname='seg') AS default_owner FROM pg_proc WHERE oid IN ("+strings.Join(untouched, ",")+") ORDER BY proname", []string{"proname", "default_owner"}, []string{"NAME", "BOOL"}, [][]any{{"seg_contained", true}, {"seg_contains", true}, {"seg_left", true}, {"seg_over_left", true}, {"seg_over_right", true}, {"seg_right", true}, {"seg_same", true}}))
	segIntervalBoth(t, p, b, c, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1255 AND objid='public.seg_overlap(public.seg,public.seg)'::regprocedure::oid AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='seg')", []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
}

func segIntervalAuthorizationRows() [][]any {
	return [][]any{{int64(1), true, true}, {int64(2), nil, nil}, {int64(3), false, false}}
}

func segIntervalAuthorizationVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	segIntervalAuthorizationCatalog(t, p, b, c)
	segIntervalBoth(t, p, b, c, segIntervalSelect(b, segIntervalAuthorizationQuery, []string{"id", "via_function", "via_operator"}, []string{"INT4", "BOOL", "BOOL"}, segIntervalAuthorizationRows(), false))
	var held []fixture.HeldStatement
	for _, expression := range []string{"seg_overlap(a,b)", "a && b"} {
		query := "SELECT " + expression + " AS v FROM seg_interval_authorization WHERE id=$1::integer"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query})
	}
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
	run(fixture.Exec("SET ROLE "+segIntervalAllowed, 0))
	for _, h := range held {
		for _, input := range []struct {
			id   int
			want any
		}{{1, true}, {2, nil}, {3, false}} {
			step := segIntervalSelect(b, h.SQL, []string{"v"}, []string{"BOOL"}, [][]any{{input.want}}, false, input.id)
			segIntervalBoth(t, p, b, c, step)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, step)
		}
	}
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("SET ROLE "+segIntervalDenied, 0))
	want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function seg_overlap")
	for _, h := range held {
		for _, id := range []int{1, 2} {
			// Both rows require invocation admission: a nullable column is not a
			// planning-time literal NULL, even when its current cell is SQL NULL.
			step := fixture.ErrorDiagnostic(h.SQL, want, id)
			step.SelectRows = true
			step.ErrorStage = "execute"
			segIntervalBoth(t, p, b, c, step)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, step)
		}
	}
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("SET ROLE "+segIntervalAllowed, 0))
	for _, h := range held {
		fixture.RunStep(t, p.Ctx, h, b.Recorder, segIntervalSelect(b, h.SQL, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, false, 2))
		fixture.RunStep(t, p.Ctx, h, b.Recorder, segIntervalSelect(b, h.SQL, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, false, 1))
	}
	run(fixture.Exec("RESET ROLE", 0))
	segIntervalAuthorizationCatalog(t, p, b, c)
}

func segIntervalAuthorizationIDs(t *testing.T, p *fixture.Pair, c *sql.Conn) [3]int64 {
	t.Helper()
	var ids [3]int64
	if err := c.QueryRowContext(p.Ctx, segIntervalAuthorizationIdentities).Scan(&ids[0], &ids[1], &ids[2]); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id <= 0 {
			t.Fatal("invalid authorization provider identity", ids)
		}
	}
	return ids
}
