package reference_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func aiosRoutineMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner := "postgres"; if b.PG { owner = "stuntgres" }
	acl := "{"+owner+"=X/"+owner+",pg_read_all_stats=X/"+owner+"}"
	want := [][]any{{int64(6399),"pg_get_aios",owner,acl,int64(12),int64(2249),true,"v",true,"r","{"+strings.Join(aiosNames,",")+"}","{23,23,20,25,25,20,20,25,21,23,25,25,16,16,16}","{o,o,o,o,o,o,o,o,o,o,o,o,o,o,o}","pg_get_aios",float64(100),float64(1),int64(0)}}
	s := fixture.Select("SELECT p.oid::bigint,p.proname::text,r.rolname::text,p.proacl::text,p.prolang::bigint,p.prorettype::bigint,p.proretset,p.provolatile::text,p.proisstrict,p.proparallel::text,p.proargnames::text,p.proallargtypes::text,p.proargmodes::text,p.prosrc,p.prorows,p.procost,p.pronargs::integer FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=6399", []string{"oid","proname","rolname","proacl","prolang","prorettype","proretset","provolatile","proisstrict","proparallel","proargnames","proallargtypes","proargmodes","prosrc","prorows","procost","pronargs"}, []string{"INT8","TEXT","TEXT","TEXT","INT8","INT8","BOOL","TEXT","BOOL","TEXT","TEXT","TEXT","TEXT","TEXT","FLOAT4","FLOAT4","INT4"}, want)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s); fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	shmemDefaultACL(t, p, b.DB, "pg_aios", b.PG)
}

func aiosDefinitions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	defer conn.ExecContext(context.Background(), "RESET quote_all_identifiers")
	for _, quote := range []bool{false,true} {
		setting := "off"; if quote { setting = "on" }
		pgSequenceExec(t, p, b, conn, "SET quote_all_identifiers="+setting)
		// Independent released field list and pinned PG18.6 presentation observations.
		names := append([]string(nil), aiosNames...)
		function := "pg_get_aios"
		if quote { function = `"pg_get_aios"`; for i := range names { names[i] = `"`+names[i]+`"` } }
		definition := " SELECT "+strings.Join(names,",\n    ")+"\n   FROM "+function+"() "+function+"("+strings.Join(names,", ")+");"
		for _, pretty := range []bool{false,true} {
			s := fixture.Select("SELECT pg_get_viewdef($1::regclass,$2::boolean)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{definition}}, "pg_catalog.pg_aios", pretty)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s); fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(s))
		}
		s := fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_aios'", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s); fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(s))
	}
}
