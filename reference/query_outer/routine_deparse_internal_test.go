package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 pg_proc.dat and pg_proc.h defaults. These twenty-six rows were
// already represented in pg_proc; inquiries must use their real descriptors,
// without creating callable INTERNAL/vector/trigger providers.
func TestReferenceRoutineDeparseInternalMetadata(t *testing.T) {
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		for _,tc := range []struct{oid int;name,args,result string;volatile bool}{
			{40,"int2vectorin","cstring","int2vector",false},
			{41,"int2vectorout","int2vector","cstring",false},
			{54,"oidvectorin","cstring","oidvector",false},
			{55,"oidvectorout","oidvector","cstring",false},
			{88,"pg_ddl_command_recv","internal","pg_ddl_command",false},
			{2410,"int2vectorrecv","internal","int2vector",false},
			{2411,"int2vectorsend","int2vector","bytea",false},
			{2420,"oidvectorrecv","internal","oidvector",false},
			{2421,"oidvectorsend","oidvector","bytea",false},
			{3717,"prsd_start","internal, integer","internal",false},
			{3718,"prsd_nexttoken","internal, internal, internal","internal",false},
			{3719,"prsd_end","internal","void",false},
			{3720,"prsd_headline","internal, internal, tsquery","internal",false},
			{3721,"prsd_lextype","internal","internal",false},
			{1644,"RI_FKey_check_ins","","trigger",true},
			{1645,"RI_FKey_check_upd","","trigger",true},
			{1646,"RI_FKey_cascade_del","","trigger",true},
			{1647,"RI_FKey_cascade_upd","","trigger",true},
			{1648,"RI_FKey_restrict_del","","trigger",true},
			{1649,"RI_FKey_restrict_upd","","trigger",true},
			{1650,"RI_FKey_setnull_del","","trigger",true},
			{1651,"RI_FKey_setnull_upd","","trigger",true},
			{1652,"RI_FKey_setdefault_del","","trigger",true},
			{1653,"RI_FKey_setdefault_upd","","trigger",true},
			{1654,"RI_FKey_noaction_del","","trigger",true},
			{1655,"RI_FKey_noaction_upd","","trigger",true},
		} {
			name,attributes := tc.name," IMMUTABLE PARALLEL SAFE STRICT\n"
			if tc.volatile { name = `"`+name+`"`;attributes = " PARALLEL SAFE STRICT\n" }
			definition := "CREATE OR REPLACE FUNCTION pg_catalog."+name+"("+tc.args+")\n RETURNS "+tc.result+"\n LANGUAGE internal\n"+attributes+"AS $function$"+tc.name+"$function$\n"
			for i,want := range []string{definition,tc.args,tc.args,tc.result} {
				routineDeparseText(t,p,b,c,fmt.Sprintf("SELECT %s(%d::oid) AS definition",routineDeparseNames[i],tc.oid),want)
			}
		}
	})
}
