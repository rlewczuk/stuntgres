package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityConsistency(t *testing.T) {
	pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pvEmpty(t, p, b, c, "pv_part.heap")
		// The independent native INT4 weight is 44 bytes including line pointer:
		// 185 fit in 8168 bytes; tuple 186 straddles the second conceptual block.
		// No corresponding PostgreSQL block-count equality is presumed.
		var values []string
		for i:=1; i<=185; i++ { values=append(values, fmt.Sprintf("(%d)",i)) }
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES"+strings.Join(values,","), 185))
		bits := pvCoherent(t, p, b, c, "pv_part.heap")
		if !b.PG && len(bits)!=1 { t.Fatal("185 INT4 compact pages",len(bits)) }
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(186)", 1))
		bits = pvCoherent(t, p, b, c, "pv_part.heap")
		if !b.PG && len(bits)!=2 { t.Fatal("186 INT4 compact pages",len(bits)) }
		pvFreeze(t, p, b, c)
		bits = pvCoherent(t, p, b, c, "pv_part.heap")
		for _, v := range bits { if !v.av || !v.af || !v.pd { t.Fatal("quiescent freeze failed",v) } }
		pvRun(t, p, b, c, fixture.Exec("DELETE FROM pv_part.heap WHERE k>100", 86))
		pvCoherent(t, p, b, c, "pv_part.heap")
		pvFreeze(t, p, b, c)
		pvCoherent(t, p, b, c, "pv_part.heap")
		pvRun(t, p, b, c, fixture.Exec("ALTER TABLE pv_part.heap ADD COLUMN payload text DEFAULT 'rewritten'", 0))
		pvCoherent(t, p, b, c, "pv_part.heap")
		pvRun(t, p, b, c, fixture.Exec("TRUNCATE pv_part.heap", 0)); pvEmpty(t, p, b, c, "pv_part.heap")
		// A long TEXT datum exercises real data without presuming TOAST or
		// payload-byte parity: RowAllocation uses a fixed variable-width slot.
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1,$1)", 1, strings.Repeat("x",20000)))
		bits = pvCoherent(t, p, b, c, "pv_part.heap")
		pvFreeze(t, p, b, c)
		bits = pvCoherent(t, p, b, c, "pv_part.heap")
		if !b.PG { for _, v:=range bits { if !v.av || !v.af { t.Fatal("continuation classification",v) } } }
		// A genuinely wide physical row crosses the native payload boundary:
		// 24 header + 152 bitmap + 1200*8 datum slots + 4 pointer = 9780.
		// PostgreSQL independently checks its own physical flag/count coherence.
		columns, datums := make([]string,1200), make([]string,1200)
		for i:=range columns { columns[i]=fmt.Sprintf("c%d integer",i);datums[i]="1" }
		pvRun(t,p,b,c,fixture.Exec("CREATE TABLE pv_part.wide("+strings.Join(columns,",")+")",0))
		pvRun(t,p,b,c,fixture.Exec("INSERT INTO pv_part.wide VALUES("+strings.Join(datums,",")+")",1))
		wide:=pvCoherent(t,p,b,c,"pv_part.wide")
		if !b.PG && len(wide)!=2 { t.Fatal("9780-byte fact lost continuation blocks",wide) }
		pvRun(t,p,b,c,fixture.Exec("VACUUM (FREEZE) pv_part.wide",0))
		wide=pvCoherent(t,p,b,c,"pv_part.wide")
		for _,v:=range wide {if !v.av||!v.af||!v.pd {t.Fatal("frozen wide fact lost continuation flags",v)}}
		pvRun(t, p, b, c, fixture.Exec("TRUNCATE pv_part.heap", 0)); pvEmpty(t, p, b, c, "pv_part.heap")
		pvProbe(t, p, b, c, "pv_part.heap", 4294967294, false, false, false)
		pvEmpty(t, p, b, c, "pv_part.heap")
	})
}

func TestReferencePgvisibilityCatalogHeaps(t *testing.T) {
	pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := make(map[string]int)
		for _, name := range []string{"pg_catalog.pg_class", "pg_catalog.pg_proc"} {
			bits:=pvCoherent(t, p, b, c, name)
			if len(bits)==0 { t.Fatal("nonempty represented catalog has no blocks",name) }
			before[name]=len(bits)
			if !b.PG { for _, v:=range bits { if v.av || v.af || v.pd { t.Fatal("unknown catalog provenance declared frozen",name,v) } } }
		}
		// Real catalog growth must feed the actual producer/byte model. Different
		// widths and native/PG storage models preclude a common physical count.
		for i := range 40 {
			pvRun(t, p, b, c, fixture.Exec(fmt.Sprintf("CREATE TABLE pv_part.catalog_growth_%d(a integer,b text,c bigint,d boolean)",i),0))
			pvRun(t, p, b, c, fixture.Exec(fmt.Sprintf("CREATE FUNCTION pv_part.catalog_function_%d() RETURNS integer LANGUAGE SQL AS 'SELECT %d'",i,i),0))
		}
		for _, name := range []string{"pg_catalog.pg_class", "pg_catalog.pg_proc"} {
			bits:=pvCoherent(t, p, b, c, name)
			if !b.PG && len(bits)<=before[name] { t.Fatal("catalog bytes ignored growth",name,before[name],len(bits)) }
			if !b.PG { for _, v:=range bits { if v.av || v.af || v.pd { t.Fatal("catalog growth fabricated version ownership",name,v) } } }
		}
		for _, name:=range []string{"pg_tables","pg_views"} {
			s:=pvError("SELECT * FROM pv_part.pg_visibility_map_summary($1::regclass)","42809","relation \""+name+"\" is of wrong relation kind","pg_catalog."+name)
			s.Diagnostic.Detail="This operation is not supported for views."
			pvBoth(t,p,b,c,s)
		}
	})
}
