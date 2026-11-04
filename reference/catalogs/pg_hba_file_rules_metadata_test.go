package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func hbaInitialACL(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, live bool) {
	t.Helper()
	owner := "postgres"
	if b.PG {
		owner = "stuntgres"
	}
	tableACL, routineACL := "{"+owner+"=arwdDxtm/"+owner+"}", "{"+owner+"=X/"+owner+"}"
	if live {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_hba_file_rules'::regclass", []string{"relacl"}, []string{"TEXT"}, [][]any{{tableACL}}))
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=3401", []string{"proacl"}, []string{"TEXT"}, [][]any{{routineACL}}))
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog.pg_class'::regclass AND objoid='pg_catalog.pg_hba_file_rules'::regclass AND objsubid=0", []string{"privtype", "initprivs"}, []string{"TEXT", "TEXT"}, [][]any{{"i", tableACL}}))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog.pg_proc'::regclass AND objoid=3401 AND objsubid=0", []string{"privtype", "initprivs"}, []string{"TEXT", "TEXT"}, [][]any{{"i", routineACL}}))
}

func TestReferencePGHBAFileRulesMetadata(t *testing.T) {
	p, f := fixture.NewHBAFileScenario(t, fixture.HBARuleSet{})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner := "postgres"
			if b.PG {
				owner = "stuntgres"
			}
			attrs := make([][]any, 11)
			for i, name := range hbaColumns {
				oid, coll := int64(25), int64(100)
				if i == 0 || i == 2 {
					oid, coll = 23, 0
				}
				if i == 4 || i == 5 || i == 9 {
					oid = 1009
				}
				// Function OUT arrays have attndims=0 in the view, independently
				// of rank-one/lower-one values. Root pins this source-led value.
				attrs[i] = []any{int64(i + 1), name, oid, int64(-1), int64(0), coll, false, false}
			}
			steps := []fixture.Step{
				fixture.Select("SELECT c.oid::int4,c.reltype::int4,t.typarray::int4 FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_hba_file_rules'::regclass", []string{"oid", "reltype", "typarray"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(12114), int64(12116), int64(12115)}}),
				fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,atttypmod::int4,attndims::int4,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_hba_file_rules'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attndims", "attcollation", "attnotnull", "attisdropped"}, []string{"INT4", "TEXT", "INT8", "INT4", "INT4", "INT8", "BOOL", "BOOL"}, attrs),
				fixture.Select("SELECT c.relkind::text,c.relnamespace::int4,c.relnatts::int4,c.relowner::regrole::text,t.typname::text,t.typtype::text,t.typrelid=c.oid AS owns_relation,a.typelem=t.oid AS owns_element FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_hba_file_rules'::regclass", []string{"relkind", "relnamespace", "relnatts", "relowner", "typname", "typtype", "owns_relation", "owns_element"}, []string{"TEXT", "INT4", "INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"v", int64(11), int64(11), owner, "pg_hba_file_rules", "c", true, true}}),
				fixture.Select("SELECT oid::bigint,proname::text,pronamespace::bigint,proowner::regrole::text,prolang::bigint,procost,prorows,provariadic::bigint,prosupport::text,prokind::text,prosecdef,proleakproof,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::int4,pronargdefaults::int4,prorettype::bigint,proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,proargdefaults::text,protrftypes::text,prosrc,probin,prosqlbody::text,proconfig::text,proacl::text FROM pg_proc WHERE oid=3401",
					[]string{"oid", "proname", "pronamespace", "proowner", "prolang", "procost", "prorows", "provariadic", "prosupport", "prokind", "prosecdef", "proleakproof", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "pronargdefaults", "prorettype", "proargtypes", "proallargtypes", "proargmodes", "proargnames", "proargdefaults", "protrftypes", "prosrc", "probin", "prosqlbody", "proconfig", "proacl"},
					[]string{"INT8", "TEXT", "INT8", "TEXT", "INT8", "FLOAT4", "FLOAT4", "INT8", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "INT4", "INT4", "INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
					[][]any{{int64(3401), "pg_hba_file_rules", int64(11), owner, int64(12), float64(1), float64(1000), int64(0), "-", "f", false, false, true, true, "v", "s", int64(0), int64(0), int64(2249), "", "{23,25,23,25,1009,1009,25,25,25,1009,25}", "{o,o,o,o,o,o,o,o,o,o,o}", "{rule_number,file_name,line_number,type,database,user_name,address,netmask,auth_method,options,error}", nil, nil, "pg_hba_file_rules", nil, nil, nil, "{" + owner + "=X/" + owner + "}"}}),
				fixture.Select("SELECT pg_get_viewdef('pg_hba_file_rules'::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{" SELECT rule_number,\n    file_name,\n    line_number,\n    type,\n    database,\n    user_name,\n    address,\n    netmask,\n    auth_method,\n    options,\n    error\n   FROM pg_hba_file_rules() a(rule_number, file_name, line_number, type, database, user_name, address, netmask, auth_method, options, error);"}}),
			}
			for _, s := range steps {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			}
			hbaInitialACL(t, p, b, b.DB, true)
			for _, tc := range []struct {
				set  fixture.HBARuleSet
				want [][]any
			}{
				{fixture.HBARuleSet{}, nil},
				{fixture.HBARuleSet{Contents: "# comments\n"}, nil},
				{hbaLocalSet("local all all trust\n", "trust"), [][]any{hbaLocal(1, f.Path(b, ""), 1, "trust")}},
				{fixture.HBARuleSet{Contents: "bad\n", Rules: []stuntgres.HBARule{{FileName: new("{MAIN}"), LineNumber: new(int32(1)), Error: new(`invalid connection type "bad"`)}}}, [][]any{hbaInvalid(f.Path(b, ""), 1, `invalid connection type "bad"`)}},
			} {
				f.Install(t, p.Ctx, b, tc.set)
				for _, prepared := range []bool{false, true} {
					hbaRead(t, p, b, b.DB, hbaSelect, tc.want, prepared)
				}
			}
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all peer map=x\n", "peer", "map=x"))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT array_ndims(database) AS rank,array_lower(database,1) AS lower,array_upper(database,1) AS upper,database[1] AS db,user_name[1] AS actor,options[1] AS option FROM pg_hba_file_rules", []string{"rank", "lower", "upper", "db", "actor", "option"}, []string{"INT4", "INT4", "INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), int64(1), int64(1), "all", "all", "map=x"}}))
		})
	}
}
