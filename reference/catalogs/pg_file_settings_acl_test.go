package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func pgFileSettingsInitialACL(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, live bool) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	acl := "{" + owner + "=arwdDxtm/" + owner + "}"
	if live {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_file_settings'::regclass", []string{"relacl"}, []string{"TEXT"}, [][]any{{acl}}))
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog.pg_class'::regclass AND objoid='pg_catalog.pg_file_settings'::regclass AND objsubid=0", []string{"privtype", "initprivs"}, []string{"TEXT", "TEXT"}, [][]any{{"i", acl}}))
	routineACL := "{" + owner + "=X/" + owner + "}"
	if live {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=3329", []string{"proacl"}, []string{"TEXT"}, [][]any{{routineACL}}))
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT classoid::bigint,objoid::bigint,objsubid::int4,privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog.pg_proc'::regclass AND objoid=3329 ORDER BY objsubid", []string{"classoid", "objoid", "objsubid", "privtype", "initprivs"}, []string{"INT8", "INT8", "INT4", "TEXT", "TEXT"}, [][]any{{int64(1255), int64(3329), int64(0), "i", routineACL}}))
}

func pgFileSettingsRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	s := fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,proowner::regrole::text,prolang::bigint,procost,prorows,provariadic::bigint,prosupport::text,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,proargdefaults::text,protrftypes::text,prosrc,probin,prosqlbody::text,proconfig::text,proacl::text FROM pg_proc WHERE oid=3329",
		[]string{"oid", "proname", "pronamespace", "proowner", "prolang", "procost", "prorows", "provariadic", "prosupport", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "proargdefaults", "protrftypes", "prosrc", "probin", "prosqlbody", "proconfig", "proacl"},
		[]string{"INT8", "TEXT", "INT8", "TEXT", "INT8", "FLOAT4", "FLOAT4", "INT8", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{int64(3329), "pg_show_all_file_settings", int64(11), owner, int64(12), float64(1), float64(1000), int64(0), "-", "f", false, false, true, true, "v", "s", int64(0), int64(0), int64(2249), "", "{25,23,23,25,25,16,25}", "{o,o,o,o,o,o,o}", "{sourcefile,sourceline,seqno,name,setting,applied,error}", nil, nil, "show_all_file_settings", nil, nil, nil, "{" + owner + "=X/" + owner + "}"}})
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
}
