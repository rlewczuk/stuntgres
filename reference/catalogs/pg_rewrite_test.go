package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func rewriteExec(t *testing.T, p *fixture.Pair, b fixture.Backend, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
	}
}
func rewriteRows(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	stmt, err := b.DB.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: s.Query}, b.Recorder, s)
}
func rewriteCount(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, n int64) {
	t.Helper()
	rewriteRows(t, p, b, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{n}}))
}
func rewriteTreeRows(t *testing.T, b fixture.Backend, rows *sql.Rows) [2]string {
	t.Helper()
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "PG_NODE_TREE" {
			t.Fatal(typ)
		}
	}
	if !rows.Next() {
		t.Fatal("missing rewrite", rows.Err())
	}
	var raw [2]any
	if err := rows.Scan(&raw[0], &raw[1]); err != nil {
		t.Fatal(err)
	}
	var result [2]string
	for i, value := range raw {
		if b.PG {
			v, ok := value.([]byte)
			if !ok {
				t.Fatalf("PostgreSQL tree %T", value)
			}
			result[i] = string(v)
		} else {
			v, ok := value.(string)
			if !ok {
				t.Fatalf("native tree %T", value)
			}
			result[i] = v
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("rewrite multiplicity", rows.Err())
	}
	return result
}
func rewriteTrees(t *testing.T, p *fixture.Pair, b fixture.Backend, relation, name string) [2]string {
	t.Helper()
	q := "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='" + relation + "'::regclass AND rulename='" + name + "'"
	rows, err := b.DB.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	want := rewriteTreeRows(t, b, rows)
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		rows, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := rewriteTreeRows(t, b, rows); got != want {
			t.Fatal("prepared tree differs from direct definition", got, want)
		}
	}
	return want
}

func TestReferencePGRewriteMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE src(v INT)", "CREATE RULE r AS ON INSERT TO src DO NOTHING")
			names := []string{"oid", "rulename", "ev_class", "ev_type", "ev_enabled", "is_instead", "ev_qual", "ev_action"}
			wantTypes := []string{"OID", "NAME", "OID", "CHAR", "CHAR", "BOOL", "PG_NODE_TREE", "PG_NODE_TREE"}
			for _, projection := range []string{"*", strings.Join(names, ",")} {
				for _, empty := range []bool{false, true} {
					q := "SELECT " + projection + " FROM pg_rewrite WHERE ev_class='src'::regclass"
					if empty {
						q += " AND false"
					}
					stmt, err := b.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, prepared := range []bool{false, true, true} {
						var rows *sql.Rows
						if prepared {
							rows, err = stmt.QueryContext(p.Ctx)
						} else {
							rows, err = b.DB.QueryContext(p.Ctx, q)
						}
						if err != nil {
							t.Fatal(err)
						}
						got, err := rows.Columns()
						if err != nil || !reflect.DeepEqual(got, names) {
							t.Fatal(got, err)
						}
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 8 {
							t.Fatal(types, err)
						}
						for i, typ := range types {
							if typ.DatabaseTypeName() != wantTypes[i] {
								t.Fatal(i, typ)
							}
						}
						if !empty {
							if !rows.Next() {
								t.Fatal(rows.Err())
							}
							var oid, owner int64
							var name, event, mode string
							var instead bool
							var qual, action any
							if err := rows.Scan(&oid, &name, &owner, &event, &mode, &instead, &qual, &action); err != nil {
								t.Fatal(err)
							}
							if oid <= 0 || owner <= 0 || name != "r" || event != "3" || mode != "O" || instead || qual == nil || action == nil {
								t.Fatal(oid, owner, name, event, mode, instead, qual, action)
							}
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("unexpected rewrite rows", rows.Err())
						}
						rows.Close()
					}
					stmt.Close()
				}
			}
			var attrs [][]any
			ids := []int64{26, 19, 26, 18, 18, 16, 194, 194}
			for i, name := range names {
				coll := int64(0)
				if i == 1 || i >= 6 {
					coll = 950
				}
				attrs = append(attrs, []any{int64(i + 1), name, ids[i], true, int64(-1), coll})
			}
			rewriteRows(t, p, b, fixture.Select("SELECT attnum,attname,atttypid::bigint,attnotnull,atttypmod,attcollation::bigint FROM pg_attribute WHERE attrelid=2618 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation"}, []string{"INT2", "NAME", "INT8", "BOOL", "INT4", "INT8"}, attrs))
			rewriteRows(t, p, b, fixture.Select("SELECT relhasrules FROM pg_class WHERE oid='src'::regclass", []string{"relhasrules"}, []string{"BOOL"}, [][]any{{true}}))
			if pair := rewriteTrees(t, p, b, "src", "r"); pair[0] != "<>" {
				t.Fatal(pair)
			}
		})
	}
}

func TestReferencePGRewriteTrees(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE src(v INT)", "CREATE TABLE dst(v INT)", "CREATE TABLE base(id INT,v INT)")
			cases := []struct {
				name, sql, event string
				instead          bool
				native, pg       []string
			}{
				{"nothing", "CREATE RULE nothing AS ON INSERT TO src DO ALSO NOTHING", "3", false, []string{"{Nothing event \"INSERT\" pseudo OLD 1 NEW 2}"}, []string{"{QUERY", ":commandType 7"}},
				{"discard", "CREATE RULE discard AS ON DELETE TO src DO INSTEAD NOTHING", "4", true, []string{"{Nothing event \"DELETE\""}, []string{":commandType 7"}},
				{"conditional", "CREATE RULE conditional AS ON UPDATE TO src WHERE NEW.v>OLD.v DO INSTEAD NOTHING", "2", true, []string{"{Nothing event \"UPDATE\""}, []string{":commandType 7"}},
				{"ordered", "CREATE RULE ordered AS ON INSERT TO src DO ALSO (INSERT INTO dst VALUES(NEW.v); SELECT NEW.v)", "3", false, []string{"parser.Insert", "parser.Select", "frame 2"}, []string{":commandType 3", ":commandType 1", ":varno 2"}},
				{"update_dst", "CREATE RULE update_dst AS ON UPDATE TO src DO ALSO UPDATE dst SET v=NEW.v WHERE v=OLD.v", "2", false, []string{"parser.Update", "parser.OperatorExpr"}, []string{":commandType 2"}},
				{"delete_dst", "CREATE RULE delete_dst AS ON DELETE TO src DO ALSO DELETE FROM dst WHERE v=OLD.v", "4", false, []string{"parser.Delete"}, []string{":commandType 4"}},
				{"returning_rule", "CREATE RULE returning_rule AS ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(NEW.v) RETURNING v", "3", true, []string{"parser.Insert", "Returning"}, []string{":commandType 3", ":returningList"}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					rewriteExec(t, p, b, tc.sql)
					pair := rewriteTrees(t, p, b, "src", tc.name)
					if tc.name == "conditional" {
						if b.PG {
							if !strings.Contains(pair[0], ":varno 1") || !strings.Contains(pair[0], ":varno 2") {
								t.Fatal(pair)
							}
						} else {
							if !strings.Contains(pair[0], "frame 1") || !strings.Contains(pair[0], "frame 2") {
								t.Fatal(pair)
							}
						}
					} else if pair[0] != "<>" {
						t.Fatal(pair)
					}
					witnesses := tc.native
					if b.PG {
						witnesses = tc.pg
					}
					for _, w := range witnesses {
						if !strings.Contains(pair[1], w) {
							t.Fatalf("missing %q: %s", w, pair[1])
						}
					}
					if tc.name == "ordered" {
						a, z := "parser.Insert", "parser.Select"
						if b.PG {
							a, z = ":commandType 3", ":commandType 1"
						}
						if strings.Index(pair[1], a) >= strings.Index(pair[1], z) {
							t.Fatal("action order", pair)
						}
					}
					rewriteRows(t, p, b, fixture.Select("SELECT ev_type,ev_enabled,is_instead FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='"+tc.name+"'", []string{"ev_type", "ev_enabled", "is_instead"}, []string{"CHAR", "CHAR", "BOOL"}, [][]any{{tc.event, "O", tc.instead}}))
					switch tc.name {
					case "ordered":
						rewriteEffect(t, p, b, "INSERT INTO src VALUES(5)", 1)
						rewriteRows(t, p, b, fixture.Select("SELECT v FROM src", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
						rewriteRows(t, p, b, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
					case "nothing":
						rewriteEffect(t, p, b, "INSERT INTO src VALUES(1)", 1)
						rewriteCount(t, p, b, "SELECT count(*) FROM src", 1)
					case "discard":
						rewriteEffect(t, p, b, "INSERT INTO src VALUES(1)", 1)
						rewriteEffect(t, p, b, "DELETE FROM src", 0)
						rewriteCount(t, p, b, "SELECT count(*) FROM src", 1)
					case "conditional":
						rewriteEffect(t, p, b, "INSERT INTO src VALUES(1)", 1)
						rewriteEffect(t, p, b, "UPDATE src SET v=2", 0)
						rewriteRows(t, p, b, fixture.Select("SELECT v FROM src", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
						rewriteEffect(t, p, b, "UPDATE src SET v=0", 1)
					case "update_dst":
						rewriteEffect(t, p, b, "INSERT INTO src VALUES(1)", 1)
						rewriteEffect(t, p, b, "INSERT INTO dst VALUES(1)", 1)
						rewriteEffect(t, p, b, "UPDATE src SET v=2", 1)
						rewriteRows(t, p, b, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
					case "delete_dst":
						rewriteEffect(t, p, b, "INSERT INTO src VALUES(1)", 1)
						rewriteEffect(t, p, b, "INSERT INTO dst VALUES(1)", 1)
						rewriteEffect(t, p, b, "DELETE FROM src", 1)
						rewriteCount(t, p, b, "SELECT count(*) FROM dst", 0)
					case "returning_rule":
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("INSERT INTO src VALUES(8) RETURNING v", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
						rewriteCount(t, p, b, "SELECT count(*) FROM src", 0)
						rewriteRows(t, p, b, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
					}
					rewriteExec(t, p, b, "DROP RULE "+tc.name+" ON src")
					rewriteExec(t, p, b, "TRUNCATE src,dst")
				})
			}
			rewriteExec(t, p, b, "CREATE VIEW v(k,label) AS SELECT id,v FROM base WHERE v>0", "CREATE MATERIALIZED VIEW mv(k,label) AS SELECT id,v FROM base WHERE v>0")
			for _, rel := range []string{"v", "mv"} {
				pair := rewriteTrees(t, p, b, rel, "_RETURN")
				if pair[0] != "<>" {
					t.Fatal(pair)
				}
				witnesses := []string{"{RewriteActions{parser.Select", "Where", "Var attnum", `output "id"`, `output "v"`}
				if b.PG {
					// DefineView applies ordinary-view aliases to stored target
					// entries; CREATE MATERIALIZED VIEW keeps defining-query names.
					witnesses = []string{"{QUERY", ":qual", ":resname id", ":resname v"}
					if rel == "v" {
						witnesses = []string{"{QUERY", ":qual", ":resname k", ":resname label"}
					}
				}
				for _, w := range witnesses {
					if !strings.Contains(pair[1], w) {
						t.Fatal(rel, w, pair)
					}
				}
				rewriteRows(t, p, b, fixture.Select("SELECT rulename,ev_type,ev_enabled,is_instead FROM pg_rewrite WHERE ev_class='"+rel+"'::regclass", []string{"rulename", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "CHAR", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", "O", true}}))
				rewriteRows(t, p, b, fixture.Select("SELECT relhasrules FROM pg_class WHERE oid='"+rel+"'::regclass", []string{"relhasrules"}, []string{"BOOL"}, [][]any{{true}}))
				// Relation descriptors independently preserve aliases in both backends.
				rewriteRows(t, p, b, fixture.Select("SELECT k,label FROM "+rel, []string{"k", "label"}, []string{"INT4", "INT4"}, nil))
			}
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rules WHERE tablename IN ('v','mv')", 0)
			rewriteCount(t, p, b, "SELECT count(*) FROM dst", 0)
		})
	}
}

func TestReferencePGRewriteBuiltinDeclarations(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var previous string
			for _, name := range []string{"pg_stat_progress_copy", "pg_stat_progress_create_index"} {
				pair := rewriteTrees(t, p, b, name, "_RETURN")
				if pair[0] != "<>" || pair[1] == previous {
					t.Fatal(pair)
				}
				previous = pair[1]
				if b.PG {
					if !strings.Contains(pair[1], "{QUERY") {
						t.Fatal(pair)
					}
				} else {
					for _, w := range []string{"{BuiltinRewriteDeclaration {parser.Select", "references [", "pg_stat_get_progress_info", "Targets", "Where"} {
						if !strings.Contains(pair[1], w) {
							t.Fatal(w, pair)
						}
					}
				}
				rewriteRows(t, p, b, fixture.Select("SELECT rulename,ev_type,ev_enabled,is_instead,ev_class='"+name+"'::regclass AS linked,pg_get_ruledef(oid) IS NOT NULL AS displayed FROM pg_rewrite WHERE ev_class='"+name+"'::regclass", []string{"rulename", "ev_type", "ev_enabled", "is_instead", "linked", "displayed"}, []string{"NAME", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"_RETURN", "1", "O", true, true, true}}))
			}
		})
	}
}
