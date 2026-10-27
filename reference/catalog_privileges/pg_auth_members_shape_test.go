package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAuthMembersShape(t *testing.T) {
	pgGroupScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_auth_members WHERE FALSE", "SELECT * FROM pg_auth_members WHERE FALSE", "SELECT * FROM pg_auth_members LIMIT 0"} {
			authMembersBoth(t, p, b, b.DB, q, nil)
		}
		seed := authMembersRead(t, p, b, b.DB, "SELECT * FROM pg_auth_members WHERE roleid=3374")
		if len(seed) != 1 { t.Fatal(seed) }
		authMembersBoth(t, p, b, b.DB, "SELECT * FROM pg_auth_members WHERE oid=$1", seed, seed[0].oid)
		authMembersBoth(t, p, b, b.DB, "SELECT * FROM pg_auth_members WHERE oid=$1", nil, nil)
		pgRolesBoth(t, p, b, fixture.Select("SELECT oid,relnamespace,relkind,reltype,relisshared,reltablespace,relhasindex,relnatts FROM pg_class WHERE oid=1261", []string{"oid","relnamespace","relkind","reltype","relisshared","reltablespace","relhasindex","relnatts"}, []string{"OID","OID","CHAR","OID","BOOL","OID","BOOL","INT2"}, [][]any{{int64(1261),int64(11),"r",int64(2843),true,int64(1664),true,int64(7)}}))
		var attrs [][]any
		for i, name := range authMembersColumns {
			typ := int64(26)
			if i >= 4 { typ = 16 }
			attrs = append(attrs, []any{int64(i+1), name, typ, true, int64(-1), int64(0), int64(0)})
		}
		pgRolesBoth(t, p, b, fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid=1261 AND attnum>0 ORDER BY attnum", []string{"attnum","attname","atttypid","attnotnull","atttypmod","attcollation","attndims"}, []string{"INT2","NAME","OID","BOOL","INT4","OID","INT2"}, attrs))
		pgRolesBoth(t, p, b, fixture.Select("SELECT oid,typrelid,typarray,typelem FROM pg_type WHERE oid=2843 OR oid=10058 ORDER BY oid", []string{"oid","typrelid","typarray","typelem"}, []string{"OID","OID","OID","OID"}, [][]any{{int64(2843),int64(1261),int64(10058),int64(0)},{int64(10058),int64(0),int64(0),int64(2843)}}))
		pgRolesBoth(t, p, b, fixture.Select("SELECT schemaname,tablename,tablespace,hasindexes FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_auth_members'", []string{"schemaname","tablename","tablespace","hasindexes"}, []string{"NAME","NAME","NAME","BOOL"}, [][]any{{"pg_catalog","pg_auth_members","pg_global",true}}))
		pgRolesBoth(t, p, b, fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_auth_members'", []string{"viewname"}, []string{"NAME"}, nil))
	})
}
