package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func spgistControlIDs(t *testing.T, p *fixture.Pair, c *sql.Conn, temporary bool) map[string]int64 {
	t.Helper()
	filter := "n.nspname='spgist_portable'"
	if temporary {
		filter = "n.nspname LIKE 'pg_temp_%' AND c.relname LIKE 'spgist_retained_%'"
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT c.relname,c.oid::bigint FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE "+filter+" ORDER BY c.oid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]int64{}
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// Occupy an observed source relation identity with an actual retained TEMP
// relation, rather than assuming independent engines allocate different OIDs.
func spgistControlCollide(t *testing.T, p *fixture.Pair, c *sql.Conn, source map[string]int64) map[string]int64 {
	t.Helper()
	var maximum int64
	for _, id := range source {
		maximum = max(maximum, id)
	}
	if maximum == 0 {
		t.Fatal("missing source relation identities")
	}
	var retained map[string]int64
	var last int64
	for i := int64(0); i <= maximum; i++ {
		fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE spgist_retained_%d(v integer)", i), 0))
		retained = spgistControlIDs(t, p, c, true)
		var next int64
		for _, id := range retained {
			next = max(next, id)
		}
		if next <= last {
			t.Fatal("retained allocator made no progress", last, next)
		}
		last = next
		collision := false
		for _, sid := range source {
			for _, tid := range retained {
				collision = collision || sid == tid
			}
		}
		if collision {
			fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, fixture.Exec("INSERT INTO pg_temp.spgist_retained_0 VALUES(73)", 1))
			return retained
		}
		if last > maximum {
			break
		}
	}
	t.Fatal("no actual retained relation identity collision", source, retained)
	return nil
}

// Rediscover the installed alias graph at each destination. Numeric identities
// belong to that catalog: a replay may remint them around retained TEMP objects.
func spgistPortableAliasMetadata(fillfactor int) fixture.Step {
	return fixture.Select(`SELECT m.amname,m.amhandler::oid::bigint AS handler,m.oid<>4000 AS alias_method,
 c.opcname,n.nspname,c.opcmethod=m.oid AS class_method,c.opcfamily=f.oid AS class_family,
 f.opfmethod=m.oid AS family_method,f.opfname,
 r.reloptions::text AS options,r.relnatts::integer AS attributes,array_length(i.indclass,1) AS keys,
 i.indkey::text AS key_attributes,
 pg_index_column_has_property(r.oid,1,'distance_orderable') AS distance
 FROM pg_class r JOIN pg_index i ON i.indexrelid=r.oid
 JOIN pg_am m ON m.oid=r.relam JOIN pg_opclass c ON c.oid=i.indclass[0]
 JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_namespace n ON n.oid=c.opcnamespace
 WHERE r.oid='spgist_portable.alias_index'::regclass`,
		[]string{"amname", "handler", "alias_method", "opcname", "nspname", "class_method", "class_family", "family_method", "opfname", "options", "attributes", "keys", "key_attributes", "distance"},
		[]string{"NAME", "INT8", "BOOL", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "NAME", "TEXT", "INT4", "INT4", "TEXT", "BOOL"},
		[][]any{{"spgist_portable_alias", int64(334), true, "alias_ops", "spgist_portable", true, true, true, "alias_family", fmt.Sprintf("{fillfactor=%d}", fillfactor), int64(2), int64(1), "2 1", true}})
}
