package reference_test

import (
	"database/sql"
	"encoding/binary"
	"slices"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// A literal REL_18_6 heap image: one line pointer and, when requested, a
// 24-byte tuple header followed by the little-endian int4 value 1. It is not
// generated from stuntgres storage or from the decoder under test.
func nativeBodyHeapPage(tuple bool) []byte {
	page := make([]byte, 60)
	binary.LittleEndian.PutUint16(page[12:], 28)
	binary.LittleEndian.PutUint16(page[14:], 32)
	binary.LittleEndian.PutUint16(page[16:], 60)
	binary.LittleEndian.PutUint16(page[18:], 0x2004)
	if tuple {
		binary.LittleEndian.PutUint32(page[24:], 32|1<<15|28<<17)
		binary.LittleEndian.PutUint32(page[32:], 1)
		binary.LittleEndian.PutUint16(page[48:], 1)
		binary.LittleEndian.PutUint16(page[50:], 1)
		page[54] = 24
		binary.LittleEndian.PutUint32(page[56:], 1)
	}
	return page
}

func TestReferenceSepgsqlSimulationNativeBodies(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"native_body_reader"})
	var mu sync.Mutex
	var events []stuntgres.SepgsqlAuditEvent
	setup := func(run sepgsqlRun) {
		for _, name := range []string{"cube", "earthdistance", "pageinspect", "pg_freespacemap"} {
			run(fixture.Exec("CREATE EXTENSION "+name, 0))
		}
		run(fixture.Exec("CREATE TABLE native_body_earth(id integer,e earth,d double precision)", 0))
		run(fixture.Exec("INSERT INTO native_body_earth VALUES(1,ll_to_earth(0,0),0),(2,NULL,0)", 2))
		run(fixture.Exec("CREATE TABLE native_body_heap(v integer)", 0))
		run(fixture.Exec("CREATE TABLE native_body_empty(v integer)", 0))
		run(fixture.Exec("INSERT INTO native_body_heap VALUES(1)", 1))
	}
	both := func(run sepgsqlRun, step fixture.Step) { run(step); run(fixture.Prepared(step)) }
	const attrs = "SELECT t_attrs FROM heap_page_item_attrs($1,'native_body_heap'::regclass)"
	const attrsCount = "SELECT count(*) AS count FROM heap_page_item_attrs($1,'native_body_heap'::regclass)"
	const badAttrs = "SELECT count(*) AS count FROM heap_page_item_attrs($1,4294967295::oid::regclass)"
	badBitmap := nativeBodyHeapPage(true)
	binary.LittleEndian.PutUint16(badBitmap[50:], 9) // bitmap needs two bytes; hoff has room for only one
	binary.LittleEndian.PutUint16(badBitmap[52:], 1) // HEAP_HASNULL
	bitmapFailure := fixture.ImplicitError(badAttrs, fixture.Diagnostic{Code: "XX001", Message: "t_bits string must not be NULL", Context: "SQL function \"heap_page_item_attrs\" statement 1\nSQL function \"heap_page_item_attrs\" statement 1"}, badBitmap)
	ordinary := func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		both(run, fixture.Select("SELECT latitude(e) AS latitude,longitude(e) AS longitude,earth_distance(e,e) AS distance FROM native_body_earth WHERE id=1", []string{"latitude", "longitude", "distance"}, []string{"FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(0), float64(0), float64(0)}}))
		both(run, fixture.Select("SELECT cube_ll_coord(earth_box(e,d),1) AS coordinate FROM native_body_earth WHERE id=1", []string{"coordinate"}, []string{"FLOAT8"}, [][]any{{float64(6378168)}}))
		both(run, fixture.CubeSelect(backend, "SELECT earth_box(e,d) AS box FROM native_body_earth WHERE id=2", []string{"box"}, [][]any{{nil}}))
		both(run, fixture.Select(attrs, []string{"t_attrs"}, []string{"_BYTEA"}, [][]any{{`{"\\x01000000"}`}}, nativeBodyHeapPage(true)))
		both(run, fixture.Select(attrs, []string{"t_attrs"}, []string{"_BYTEA"}, [][]any{{nil}}, nativeBodyHeapPage(false)))
		both(run, bitmapFailure) // bitmap diagnostics precede missing relation OID
		for _, query := range []string{
			"SELECT count(*) AS count FROM pg_freespace(NULL::regclass)",
			"SELECT count(*) AS count FROM pg_freespace(4294967295::oid::regclass)",
			"SELECT count(*) AS count FROM pg_freespace('native_body_empty'::regclass)",
		} {
			both(run, sepgsqlCount(query, 0))
		}
		both(run, sepgsqlCount("SELECT count(*) AS count FROM pg_freespace('native_body_heap'::regclass)", 1))
		both(run, fixture.Select("SELECT pg_freespace('native_body_empty'::regclass,0) AS available", []string{"available"}, []string{"INT2"}, [][]any{{int64(0)}}))
		// The SQL wrappers are NONSTRICT; only their actual heap C child is STRICT.
		// For a non-superuser NULL page succeeds, while nonNULL emptiness still
		// reaches the C superuser check before page decoding or relation lookup.
		run(fixture.Exec("CREATE ROLE native_body_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO native_body_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION native_body_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		both(run, fixture.Select(attrsCount, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, nil))
		both(run, fixture.ImplicitError(attrsCount, fixture.Diagnostic{Code: "42501", Message: "must be superuser to use raw page functions", Context: "SQL function \"heap_page_item_attrs\" statement 1\nSQL function \"heap_page_item_attrs\" statement 1"}, []byte{}))
		both(run, fixture.ImplicitError("SELECT tuple_data_split(4294967295::oid,NULL::bytea,1,99,'bad')", fixture.Diagnostic{Code: "42501", Message: "must be superuser to use raw page functions"}))
	}
	sepgsqlCase(t, ordinary, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_procedure", Allow: []string{"execute"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT quote_ident(current_database())").Scan(&database); err != nil {
			t.Fatal(err)
		}
		labels := func(signatures []string, label string) {
			for _, signature := range signatures {
				run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+label+"'", 0))
			}
		}
		labels([]string{"gc_to_sec(double precision)", "cube_enlarge(cube,double precision,integer)", "heap_page_items(bytea)", "tuple_data_split(oid,bytea,integer,integer,text,boolean)", "pg_freespace(regclass,bigint)", "pg_catalog.pg_relation_size(regclass)"}, sepgsqlS)
		// All actual native protocol counts below are simulation assertions, not
		// claims about PostgreSQL SQL inlining, optimizer or AVC multiplicity.
		entries := func(step fixture.Step, want map[string]int) {
			t.Helper()
			mu.Lock()
			events = nil
			mu.Unlock()
			run(step)
			mu.Lock()
			observed := slices.Clone(events)
			mu.Unlock()
			got := make(map[string]int)
			for _, event := range observed {
				if event.Subject != sepgsqlR || event.Target != sepgsqlS || event.Class != "db_procedure" || !slices.Contains(event.Required, "entrypoint") || len(event.Denied) != 0 {
					continue
				}
				for name := range want {
					namespace := "public"
					if name == "pg_relation_size" {
						namespace = "pg_catalog"
					}
					if event.Name == database+"."+namespace+"."+name {
						got[name]++
					}
				}
			}
			for name, count := range want {
				if got[name] != count {
					t.Fatalf("%s actual entered body count: got%d want%d: %#v", name, got[name], count, observed)
				}
			}
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		}
		run(sepgsqlSet(sepgsqlR))
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT cube_ll_coord(earth_box(e,d),1) AS coordinate FROM native_body_earth WHERE id=1", []string{"coordinate"}, []string{"FLOAT8"}, [][]any{{float64(6378168)}})
			if prepared {
				step = fixture.Prepared(step)
			}
			entries(step, map[string]int{"gc_to_sec": 1, "cube_enlarge": 1})
			step = fixture.Select(attrs, []string{"t_attrs"}, []string{"_BYTEA"}, [][]any{{`{"\\x01000000"}`}}, nativeBodyHeapPage(true))
			if prepared {
				step = fixture.Prepared(step)
			}
			entries(step, map[string]int{"heap_page_items": 2, "tuple_data_split": 1})
			step = fixture.Select(attrsCount, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, make([]byte, 24))
			if prepared {
				step = fixture.Prepared(step)
			}
			entries(step, map[string]int{"heap_page_items": 1, "tuple_data_split": 0})
			step = fixture.Select(attrsCount, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, nil)
			if prepared {
				step = fixture.Prepared(step)
			}
			entries(step, map[string]int{"heap_page_items": 0, "tuple_data_split": 0})
			step = sepgsqlCount("SELECT count(*) AS count FROM pg_freespace('native_body_heap'::regclass)", 1)
			if prepared {
				step = fixture.Prepared(step)
			}
			entries(step, map[string]int{"pg_relation_size": 1, "pg_freespace": 1})
			step = sepgsqlCount("SELECT count(*) AS count FROM pg_freespace('native_body_empty'::regclass)", 0)
			if prepared {
				step = fixture.Prepared(step)
			}
			entries(step, map[string]int{"pg_relation_size": 1, "pg_freespace": 0})
			step = bitmapFailure
			if prepared {
				step = fixture.Prepared(step)
			}
			entries(step, map[string]int{"heap_page_items": 1, "tuple_data_split": 1})
		}
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		labels([]string{"earth_box(earth,double precision)"}, sepgsqlS)
		run(sepgsqlSet(sepgsqlR))
		// Row/expanded NULL center: no outer FHET and no cube body. The demanded
		// conversion still executes its own nested SQL/kernel work under T.
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT earth_box(e,d) AS box FROM native_body_earth WHERE id=2", []string{"box"}, []string{"CUBE"}, [][]any{{nil}})
			if prepared {
				step = fixture.Prepared(step)
			}
			entries(step, map[string]int{"earth_box": 0, "gc_to_sec": 1, "cube_enlarge": 0})
		}
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		labels([]string{"earth_box(earth,double precision)"}, sepgsqlP)
		labels([]string{"tuple_data_split(oid,bytea,integer,integer,text,boolean)"}, sepgsqlU)
		run(sepgsqlSet(sepgsqlR))
		denied := fixture.ImplicitError(attrsCount, fixture.Diagnostic{Code: "42501", Message: "SELinux: security policy violation", Context: "SQL function \"heap_page_item_attrs\" statement 1\nSQL function \"heap_page_item_attrs\" statement 1"}, nativeBodyHeapPage(false))
		both(run, denied) // NONSTRICT split must enter even when raw_data is NULL.
		both(run, fixture.Select(attrsCount, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, nil))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	})
}
