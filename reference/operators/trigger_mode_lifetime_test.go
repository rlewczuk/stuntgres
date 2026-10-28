package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerModePartitions(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerDDLPartitionSeed(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE TRIGGER local BEFORE INSERT ON tr_leaf FOR EACH ROW EXECUTE FUNCTION tr_add(7)",
			"CREATE TABLE tr_old(id integer,v integer)",
			"CREATE TABLE tr_inherited() INHERITS(tr_old)",
			"CREATE TRIGGER a BEFORE INSERT ON tr_old FOR EACH ROW EXECUTE FUNCTION tr_add(3)",
			"CREATE TRIGGER a BEFORE INSERT ON tr_inherited FOR EACH ROW EXECUTE FUNCTION tr_add(3)",
			"CREATE TABLE tm_statement(label text)",
			"CREATE FUNCTION tm_statement_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tm_statement VALUES(TG_TABLE_NAME); RETURN NULL; END$$",
			"CREATE TRIGGER s AFTER INSERT ON tr_parent FOR EACH STATEMENT EXECUTE FUNCTION tm_statement_fn()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("ALTER TABLE ONLY tr_parent DISABLE TRIGGER a", 0))
		run(triggerModeCatalog("tr_parent", []any{"a", "D"}, []any{"s", "O"}))
		run(triggerModeCatalog("tr_leaf", []any{"a", "O"}, []any{"local", "O"}))
		run(triggerRows("INSERT INTO tr_parent VALUES(1,5) RETURNING id,v", []any{int64(1), int64(15)}))
		run(fixture.Exec("ALTER TABLE tr_parent DISABLE TRIGGER a", 0))
		run(triggerModeCatalog("tr_mid", []any{"a", "D"}))
		run(triggerModeCatalog("tr_leaf", []any{"a", "D"}, []any{"local", "O"}))
		run(triggerRows("INSERT INTO tr_parent VALUES(2,5) RETURNING id,v", []any{int64(2), int64(12)}))
		run(fixture.Exec("ALTER TABLE tr_leaf ENABLE ALWAYS TRIGGER a", 0))
		run(fixture.Exec("ALTER TABLE tr_parent ADD CONSTRAINT tm_mode_keep CHECK(v >= 0)", 0))
		run(triggerModeCatalog("tr_leaf", []any{"a", "A"}, []any{"local", "O"}))
		run(triggerRows("INSERT INTO tr_parent(id,v) VALUES(3,5) RETURNING id,v", []any{int64(3), int64(15)}))
		run(fixture.Exec("ALTER TABLE tr_parent DISABLE TRIGGER USER", 0))
		run(triggerModeCatalog("tr_leaf", []any{"a", "D"}, []any{"local", "O"}))
		run(triggerRows("INSERT INTO tr_leaf(id,v) VALUES(4,5) RETURNING id,v", []any{int64(4), int64(12)}))
		run(fixture.Select("SELECT label FROM tm_statement ORDER BY label", []string{"label"}, []string{"TEXT"}, [][]any{{"tr_parent"}, {"tr_parent"}, {"tr_parent"}}))
		run(fixture.Exec("CREATE TABLE tr_future PARTITION OF tr_parent FOR VALUES FROM (100) TO (200)", 0))
		run(triggerModeCatalog("tr_future", []any{"a", "D"}))
		run(fixture.Exec("ALTER TABLE tr_old DISABLE TRIGGER ALL", 0))
		run(triggerModeCatalog("tr_inherited", []any{"a", "O"}))
		run(triggerRows("INSERT INTO tr_inherited VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
		// Identity links survive mode changes throughout the three-level tree.
		run(fixture.Select("SELECT c.relname,p.tgname=t.tgname AS same_name FROM pg_trigger t JOIN pg_trigger p ON p.oid=t.tgparentid JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relname IN ('tr_mid','tr_leaf','tr_sibling','tr_future') ORDER BY c.relname", []string{"relname", "same_name"}, []string{"NAME", "BOOL"}, [][]any{{"tr_future", true}, {"tr_leaf", true}, {"tr_mid", true}, {"tr_sibling", true}}))
	})
}

func TestReferenceTriggerModePrepared(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerModeSetup(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "INSERT INTO tm_data VALUES($1,5) RETURNING id,v"
		held := triggerDDLHeld(t, p, b, c, q)
		run(fixture.Exec("PREPARE tm_sql(integer) AS INSERT INTO tm_data VALUES($1,5) RETURNING id,v", 0))
		id := int64(0)
		for _, mode := range triggerModeCases {
			run(fixture.Exec("ALTER TABLE tm_data "+mode.action+" a", 0))
			for _, role := range []string{"origin", "replica", "local"} {
				run(fixture.Exec("SET session_replication_role="+role, 0))
				v := int64(5)
				if triggerModeActive(mode.code, role) {
					v = 105
				}
				id++
				s := triggerRows(q, []any{id, v})
				s.Args = []any{id}
				held(s)
				id++
				run(triggerRows("EXECUTE tm_sql("+triggerModeNumber(id)+")", []any{id, v}))
			}
		}
		run(fixture.Exec("ALTER TABLE tm_data ENABLE TRIGGER a", 0))
		run(fixture.Exec("SET session_replication_role=origin", 0))
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("SAVEPOINT roles", 0))
		run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
		s := triggerRows(q, []any{int64(101), int64(5)})
		s.Args = []any{101}
		held(s)
		run(fixture.Exec("ROLLBACK TO roles", 0))
		s = triggerRows(q, []any{int64(102), int64(105)})
		s.Args = []any{102}
		held(s)
		run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
		run(fixture.Exec("COMMIT", 0))
		s = triggerRows(q, []any{int64(103), int64(105)})
		s.Args = []any{103}
		held(s)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
		s = triggerRows(q, []any{int64(104), int64(5)})
		s.Args = []any{104}
		held(s)
		run(fixture.Exec("ROLLBACK", 0))
		s = triggerRows(q, []any{int64(105), int64(105)})
		s.Args = []any{105}
		held(s)
		run(fixture.Exec("DEALLOCATE tm_sql", 0))
	})
}

func TestReferenceTriggerModeMetadata(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerModeSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TRIGGER rich BEFORE UPDATE OF v ON tm_data FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tm_add('kept')")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Compare the complete logical definition and dependency edges on this
		// backend; no allocator identity is compared to another backend.
		capture := func(q string) [][]any {
			rows, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			var result [][]any
			for rows.Next() {
				values := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range values {
					ptrs[i] = &values[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					t.Fatal(err)
				}
				for i, value := range values {
					if data, ok := value.([]byte); ok {
						values[i] = string(data)
					}
				}
				result = append(result, values)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return result
		}
		queries := []string{
			"SELECT " + triggerModeDefinitionColumns + " FROM pg_trigger WHERE tgrelid='tm_data'::regclass ORDER BY tgname",
			"SELECT d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_trigger t ON t.oid=d.objid WHERE d.classid='pg_trigger'::regclass AND t.tgrelid='tm_data'::regclass ORDER BY d.objid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype",
			"SELECT trigger_name,event_manipulation,action_statement,action_orientation,action_timing,action_condition FROM information_schema.triggers WHERE event_object_table='tm_data' ORDER BY trigger_name,event_manipulation",
		}
		before := make([][][]any, len(queries))
		for i, q := range queries {
			before[i] = capture(q)
		}
		run(fixture.Prepared(fixture.Exec("ALTER TABLE tm_data DISABLE TRIGGER ALL", 0)))
		for i, q := range queries {
			if after := capture(q); !reflect.DeepEqual(before[i], after) {
				t.Fatalf("mode mutated definition %d: before=%#v after=%#v", i, before[i], after)
			}
		}
		run(triggerModeCatalog("tm_data", []any{"a", "D"}, []any{"rich", "D"}))
		run(fixture.Select("SELECT relhastriggers FROM pg_class WHERE oid='tm_data'::regclass", []string{"relhastriggers"}, []string{"BOOL"}, [][]any{{true}}))
		run(triggerRows("SELECT id,v FROM tm_data WHERE false"))
		q := "SELECT id,v FROM tm_data ORDER BY id"
		run(fixture.Exec("INSERT INTO tm_data VALUES(1,5)", 1))
		rows, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		// Native eager rows survive replacement. PG's stream is checked before
		// changing mode on its busy physical connection.
		if !b.PG {
			run(fixture.Exec("ALTER TABLE tm_data ENABLE TRIGGER ALL", 0))
		}
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"id", "v"}) {
			t.Fatal(columns, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "INT4" {
			t.Fatal(types, err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var id, v int64
		if err := rows.Scan(&id, &v); err != nil || id != 1 || v != 5 {
			t.Fatal(id, v, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	})
}

func TestReferenceTriggerModeMixedInheritance(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerDDLPartitionSeed(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tm_old(id integer,v integer)",
			"CREATE TABLE tm_inherited() INHERITS(tm_old)",
			"CREATE TRIGGER a BEFORE INSERT ON tm_old FOR EACH ROW EXECUTE FUNCTION tr_add(3)",
			"CREATE TRIGGER a BEFORE INSERT ON tm_inherited FOR EACH ROW EXECUTE FUNCTION tr_add(3)",
			"CREATE TRIGGER local BEFORE INSERT ON tr_leaf FOR EACH ROW EXECUTE FUNCTION tr_add(7)")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("ALTER TABLE tm_old ADD COLUMN extra integer, DISABLE TRIGGER a", 0))
		run(triggerModeCatalog("tm_old", []any{"a", "D"}))
		run(triggerModeCatalog("tm_inherited", []any{"a", "O"}))
		run(triggerRows("INSERT INTO tm_inherited(id,v) VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
		run(fixture.Exec("ALTER TABLE tr_parent DISABLE TRIGGER a", 0))
		run(fixture.Exec("ALTER TABLE tr_leaf ENABLE ALWAYS TRIGGER a", 0))
		run(fixture.Exec("ALTER TABLE tr_parent ADD CONSTRAINT tm_mode_mixed CHECK(v >= 0), DISABLE TRIGGER a", 0))
		for _, name := range []string{"tr_parent", "tr_mid", "tr_leaf", "tr_sibling"} {
			if name == "tr_leaf" {
				run(triggerModeCatalog(name, []any{"a", "D"}, []any{"local", "O"}))
			} else {
				run(triggerModeCatalog(name, []any{"a", "D"}))
			}
		}
		run(fixture.Select("SELECT c.relname,f.convalidated FROM pg_constraint f JOIN pg_class c ON c.oid=f.conrelid WHERE f.conname='tm_mode_mixed' ORDER BY c.relname", []string{"relname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"tr_leaf", true}, {"tr_mid", true}, {"tr_parent", true}, {"tr_sibling", true}}))
		run(triggerRows("INSERT INTO tr_parent(id,v) VALUES(1,5) RETURNING id,v", []any{int64(1), int64(12)}))
		run(triggerDDLError("ALTER TABLE tr_parent ADD CONSTRAINT tm_mode_failed CHECK(v >= 0), ENABLE TRIGGER a, ENABLE TRIGGER absent", "42704", `trigger "absent" for table "tr_parent" does not exist`, false))
		for _, name := range []string{"tr_parent", "tr_mid", "tr_leaf", "tr_sibling"} {
			if name == "tr_leaf" {
				run(triggerModeCatalog(name, []any{"a", "D"}, []any{"local", "O"}))
			} else {
				run(triggerModeCatalog(name, []any{"a", "D"}))
			}
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_constraint WHERE conname='tm_mode_failed'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceTriggerModeReplacement(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerModeSetup(t, p, b, c)
				triggerDDLPartitionSeed(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				var original int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_trigger WHERE tgrelid='tm_data'::regclass AND tgname='a'").Scan(&original); err != nil {
					t.Fatal(err)
				}
				cloneIDs := func() map[string]int64 {
					rows, err := c.QueryContext(p.Ctx, "SELECT c.relname,t.oid::bigint FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relname IN ('tr_parent','tr_mid','tr_leaf','tr_sibling') AND t.tgname='a' ORDER BY c.relname")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					result := make(map[string]int64)
					for rows.Next() {
						var name string
						var oid int64
						if err := rows.Scan(&name, &oid); err != nil {
							t.Fatal(err)
						}
						result[name] = oid
					}
					if err := rows.Err(); err != nil || len(result) != 4 {
						t.Fatal(result, err)
					}
					return result
				}
				originalClones := cloneIDs()
				for i, action := range []string{"DISABLE TRIGGER", "ENABLE REPLICA TRIGGER", "ENABLE ALWAYS TRIGGER"} {
					run(fixture.Exec("ALTER TABLE tm_data "+action+" a", 0))
					replacement := fixture.Exec("CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_add()", 0)
					replacement.Prepared = prepared
					run(replacement)
					run(triggerModeCatalog("tm_data", []any{"a", "O"}))
					var current int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_trigger WHERE tgrelid='tm_data'::regclass AND tgname='a'").Scan(&current); err != nil || current != original {
						t.Fatal("replacement changed trigger identity", current, original, err)
					}
					run(triggerRows("INSERT INTO tm_data VALUES("+triggerModeNumber(int64(i+1))+",5) RETURNING id,v", []any{int64(i + 1), int64(105)}))
					run(fixture.Exec("ALTER TABLE tr_parent "+action+" a", 0))
					run(fixture.Exec("ALTER TABLE tr_leaf ENABLE REPLICA TRIGGER a", 0))
					replacement = fixture.Exec("CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_parent FOR EACH ROW EXECUTE FUNCTION tr_add(4)", 0)
					replacement.Prepared = prepared
					run(replacement)
					for _, table := range []string{"tr_parent", "tr_mid", "tr_leaf", "tr_sibling"} {
						run(triggerModeCatalog(table, []any{"a", "O"}))
					}
					if got := cloneIDs(); !reflect.DeepEqual(got, originalClones) {
						t.Fatal("replacement changed clone identities", got, originalClones)
					}
					run(triggerRows("INSERT INTO tr_parent VALUES("+triggerModeNumber(int64(i+1))+",5) RETURNING id,v", []any{int64(i + 1), int64(9)}))
				}
			})
		})
	}
}
