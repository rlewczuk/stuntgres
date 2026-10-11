package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Expectations derive from REL_18_0 parse_utilcmd.c transformIndexConstraint,
// tablecmds.c ATExecAddIndexConstraint and the ALTER TABLE manual, not native output.
func usingIndexScenario(t *testing.T, body func(*usingIndexCase)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c := &usingIndexCase{t: t, p: p, b: b, prepared: prepared, namespace: fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)}
					_, c.conn, c.recorder = fixture.NoticeReferencePool(t, p, b.PG, &c.notices)
					body(c)
				})
			}
		})
	}
}

type usingIndexCase struct {
	t         *testing.T
	p         *fixture.Pair
	b         fixture.Backend
	conn      *sql.Conn
	recorder  *fixture.Recorder
	prepared  bool
	namespace int64
	notices   []fixture.Diagnostic
}

func (c *usingIndexCase) run(s fixture.Step) {
	c.t.Helper()
	if c.prepared {
		s = fixture.Prepared(s)
	}
	fixture.RunStep(c.t, c.p.Ctx, c.conn, c.recorder, s)
}
func (c *usingIndexCase) exec(q string, n int64) { c.t.Helper(); c.run(fixture.Exec(q, n)) }
func (c *usingIndexCase) oid(name string) int64 {
	c.t.Helper()
	var id int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1 AND relnamespace=$2", name, c.namespace).Scan(&id); err != nil || id <= 0 {
		c.t.Fatal(name, id, err)
	}
	return id
}
func (c *usingIndexCase) ids(name string) [3]int64 {
	c.t.Helper()
	var ids [3]int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid,conrelid FROM pg_constraint WHERE conname=$1 AND (contype='p' OR contype='u')", name).Scan(&ids[0], &ids[1]); err != nil {
		c.t.Fatal(err)
	}
	var namespace int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT relnamespace FROM pg_class WHERE oid=$1", ids[1]).Scan(&namespace); err != nil || namespace <= 0 {
		c.t.Fatal("owning namespace", namespace, err)
	}
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1 AND relnamespace=$2 AND relkind='i'", name, namespace).Scan(&ids[2]); err != nil {
		c.t.Fatal("backing identity", err)
	}
	// Ownership is observed through the real internal dependency, not inferred
	// solely from matching names. Extra PostgreSQL inquiry columns are separate.
	c.run(fixture.Select("SELECT objid::bigint FROM pg_depend WHERE classid=1259 AND objid=$1 AND refclassid=2606 AND refobjid=$2 AND deptype='i'", []string{"objid"}, []string{"INT8"}, [][]any{{ids[2]}}, ids[2], ids[0]))
	if c.b.PG {
		c.run(fixture.Select("SELECT conindid::bigint FROM pg_constraint WHERE oid=$1", []string{"conindid"}, []string{"INT8"}, [][]any{{ids[2]}}, ids[0]))
	}
	if ids[0] <= 0 || ids[1] <= 0 || ids[2] <= 0 || ids[0] == ids[1] || ids[0] == ids[2] || ids[1] == ids[2] {
		c.t.Fatal("constraint/table/index identities", ids)
	}
	return ids
}
func (c *usingIndexCase) rows() {
	c.t.Helper()
	c.run(fixture.Select("SELECT a,b,payload FROM ux_t ORDER BY a NULLS LAST,b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}}))
}
func (c *usingIndexCase) setup(index string) {
	c.t.Helper()
	c.exec("CREATE TABLE ux_t(a INT,b TEXT,payload TEXT)", 0)
	c.exec("INSERT INTO ux_t VALUES(1,'x','kept'),(2,'y',NULL)", 2)
	c.exec(index, 0)
}
func usingIndexDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

// PostgreSQL 18.0 pins named-constraint errors at CONSTRAINT, unnamed at kind.
func usingIndexConstraintPosition(q string) string {
	token := "CONSTRAINT"
	if !strings.Contains(q, token) {
		token = "UNIQUE"
		if strings.Contains(q, "PRIMARY KEY") {
			token = "PRIMARY"
		}
	}
	return fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
}
func (c *usingIndexCase) quiet() {
	c.t.Helper()
	if len(c.notices) != 0 {
		c.t.Fatalf("unexpected notices: %#v", c.notices)
	}
}
func usingIndexRename(old, new string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`ALTER TABLE / ADD CONSTRAINT USING INDEX will rename index "%s" to "%s"`, old, new)}
}
func (c *usingIndexCase) noticesWant(want ...fixture.Diagnostic) {
	c.t.Helper()
	if len(c.notices) != len(want) || len(want) > 0 && !reflect.DeepEqual(c.notices, want) {
		c.t.Fatalf("notices got %#v want %#v", c.notices, want)
	}
}
func (c *usingIndexCase) failure(q string, want fixture.Diagnostic, syntax bool, notices ...fixture.Diagnostic) {
	c.t.Helper()
	c.notices = nil
	s := fixture.ErrorDiagnostic(q, want)
	s.ErrorStage = "execute"
	if syntax && c.prepared {
		s.ErrorStage = "prepare"
	}
	c.run(s)
	c.noticesWant(notices...)
}
func (c *usingIndexCase) catalog(rows [][]any) {
	c.t.Helper()
	c.run(fixture.PrimaryKeyReferenceCatalog("ux_t", rows))
}
func (c *usingIndexCase) attributes(rows [][]any) {
	c.t.Helper()
	names, types, nullable := make([]string, len(rows)), make([]string, len(rows)), make([]bool, len(rows))
	for i, row := range rows {
		names[i] = row[0].(string)
		switch row[1].(int64) {
		case 23:
			types[i] = "INT4"
		case 20:
			types[i] = "INT8"
		case 25:
			types[i] = "TEXT"
		case 1043:
			types[i] = "VARCHAR"
		default:
			c.t.Fatalf("unrepresented expected attribute type %v", row[1])
		}
		nullable[i] = !row[2].(bool)
	}
	fixture.TableDDLMetadata(c.t, c.p.Ctx, c.conn, c.b.PG, c.prepared, "SELECT * FROM ux_t WHERE FALSE", names, types, nullable, 0)
	if !c.b.PG {
		return
	}
	c.run(fixture.Select("SELECT attname,atttypid::bigint,attnotnull FROM pg_attribute WHERE attrelid='ux_t'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "atttypid", "attnotnull"}, []string{"NAME", "INT8", "BOOL"}, rows))
}
func (c *usingIndexCase) indexAttributes(name string, rows [][]any) {
	c.t.Helper()
	// Native physical index columns are descriptor witnesses, not an exported
	// pg_attribute relation. Keep common real width and exact server extras.
	c.run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname=$1 AND relnamespace=$2 AND relkind='i'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(len(rows))}}, name, c.namespace))
	if !c.b.PG {
		return
	}
	c.run(fixture.Select("SELECT attname,atttypid::bigint FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid"}, []string{"NAME", "INT8"}, rows, name))
}
func (c *usingIndexCase) notNullIDs() []int64 {
	c.t.Helper()
	r, err := c.conn.QueryContext(c.p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid::bigint='ux_t'::regclass::bigint AND contype='n' ORDER BY conname")
	if err != nil {
		c.t.Fatal(err)
	}
	defer r.Close()
	var result []int64
	for r.Next() {
		var id int64
		if err := r.Scan(&id); err != nil || id <= 0 {
			c.t.Fatal(id, err)
		}
		result = append(result, id)
	}
	if err := r.Err(); err != nil {
		c.t.Fatal(err)
	}
	return result
}

// Runtime IDs are allocations, not goldens. Capture the original complete group
// to assert failed publication leaves its identities, descriptors and data intact.
func (c *usingIndexCase) snapshot() [][][]any {
	c.t.Helper()
	var result [][][]any
	namespaceArgs := []any{c.namespace}
	queries := []struct {
		sql  string
		args []any
	}{
		{"SELECT relname,oid,relkind,relnatts FROM pg_class WHERE relnamespace=$1 ORDER BY relname", namespaceArgs},
		{"SELECT c.conname,c.oid,c.contype,c.conrelid,c.conbin,pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relnamespace=$1 ORDER BY c.conname,c.oid", namespaceArgs},
		{"SELECT d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_class r ON r.oid=d.objid WHERE d.classid=1259 AND r.relnamespace=$1 ORDER BY d.objid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype", namespaceArgs},
		{"SELECT d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_constraint c ON c.oid=d.objid JOIN pg_class r ON r.oid=c.conrelid WHERE d.classid=2606 AND r.relnamespace=$1 ORDER BY d.objid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype", namespaceArgs},
		{"SELECT * FROM ux_t ORDER BY b NULLS LAST", nil},
	}
	if c.b.PG {
		queries = append(queries, struct {
			sql  string
			args []any
		}{
			"SELECT c.conname,c.conindid,c.conkey,c.condeferrable,c.condeferred FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relnamespace=$1 ORDER BY c.conname,c.oid",
			namespaceArgs,
		}, struct {
			sql  string
			args []any
		}{
			"SELECT attname,atttypid,attnotnull FROM pg_attribute WHERE attrelid='ux_t'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", nil,
		}, struct {
			sql  string
			args []any
		}{
			"SELECT r.relname,a.attnum,a.attname,a.atttypid FROM pg_attribute a JOIN pg_class r ON r.oid=a.attrelid WHERE r.relnamespace=$1 AND r.relkind='i' AND a.attnum>0 ORDER BY r.relname,a.attnum", namespaceArgs,
		})
	}
	for _, q := range queries {
		r, err := c.conn.QueryContext(c.p.Ctx, q.sql, q.args...)
		if err != nil {
			c.t.Fatal(err)
		}
		cols, err := r.Columns()
		if err != nil {
			r.Close()
			c.t.Fatal(err)
		}
		if q.sql == "SELECT * FROM ux_t ORDER BY b NULLS LAST" {
			columnTypes, err := r.ColumnTypes()
			if err != nil {
				r.Close()
				c.t.Fatal(err)
			}
			var metadata [][]any
			for _, column := range columnTypes {
				nullable, known := column.Nullable()
				length, lengthKnown := column.Length()
				precision, scale, decimalKnown := column.DecimalSize()
				metadata = append(metadata, []any{column.Name(), column.DatabaseTypeName(), nullable, known, length, lengthKnown, precision, scale, decimalKnown})
			}
			result = append(result, metadata)
		}
		var bag [][]any
		for r.Next() {
			v := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range v {
				dest[i] = &v[i]
			}
			if err := r.Scan(dest...); err != nil {
				r.Close()
				c.t.Fatal(err)
			}
			for i, x := range v {
				if b, ok := x.([]byte); ok {
					v[i] = string(b)
				}
			}
			bag = append(bag, v)
		}
		err = r.Err()
		r.Close()
		if err != nil {
			c.t.Fatal(err)
		}
		result = append(result, bag)
	}
	return result
}
func (c *usingIndexCase) unchanged(want [][][]any) {
	c.t.Helper()
	if got := c.snapshot(); !reflect.DeepEqual(got, want) {
		c.t.Fatalf("failed statement published state: got %#v want %#v", got, want)
	}
}
func usingIndexCatalogDuplicate(parent int64, name string) fixture.Diagnostic {
	d := usingIndexDiagnostic("23505", `duplicate key value violates unique constraint "pg_constraint_conrelid_contypid_conname_index"`)
	d.Detail = fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", parent, name)
	d.Schema, d.Table, d.Constraint = "pg_catalog", "pg_constraint", "pg_constraint_conrelid_contypid_conname_index"
	return d
}

func TestReferenceUsingIndexEligibility(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		for _, key := range []string{"a", "a,b", "a int4_ops ASC NULLS LAST,b text_ops ASC NULLS LAST", "a,b COLLATE \"default\"", "(a),b"} {
			for _, name := range []string{"", "ux_idx", "ux_owned"} {
				t.Run(kind+"/"+key+"/"+name, func(t *testing.T) {
					usingIndexScenario(t, func(c *usingIndexCase) {
						c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(" + key + ") INCLUDE(payload)")
						parent, index := c.oid("ux_t"), c.oid("ux_idx")
						prefix := ""
						final := name
						if name != "" {
							prefix = "CONSTRAINT " + name + " "
						} else {
							final = "ux_idx"
						}
						c.notices = nil
						c.exec("ALTER TABLE ux_t ADD "+prefix+kind+" USING INDEX ux_idx", 0)
						if name == "ux_owned" {
							if !reflect.DeepEqual(c.notices, []fixture.Diagnostic{usingIndexRename("ux_idx", name)}) {
								c.t.Fatalf("rename notice %#v", c.notices)
							}
						} else {
							c.quiet()
						}
						ids := c.ids(final)
						if ids[1] != parent || ids[2] != index {
							c.t.Fatal("attachment replaced existing identity", parent, index, ids)
						}
						c.rows()
						c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t VALUES(1,'x','different')", fixture.UniqueReferenceDuplicate("ux_t", final, func() string {
							if key == "a" {
								return "Key (a)=(1) already exists."
							}
							return "Key (a, b)=(1, x) already exists."
						}())))
						c.exec("INSERT INTO ux_t VALUES(3,'z',NULL)", 1)
						c.run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relnamespace=$1 ORDER BY relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{final, "i"}, {"ux_t", "r"}}, c.namespace))
					})
				})
			}
		}
	}
	// Preserve the original boundary's prepared scalar attachment and post-attach reuse.
	t.Run("original_using_index", func(t *testing.T) {
		usingIndexScenario(t, func(c *usingIndexCase) {
			c.exec("CREATE TABLE t(a INT)", 0)
			c.exec("CREATE UNIQUE INDEX prebuilt ON t(a)", 0)
			original := c.oid("prebuilt")
			c.run(fixture.Prepared(fixture.Exec("ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY USING INDEX prebuilt", 0)))
			if c.ids("pk")[2] != original {
				c.t.Fatal("original index lost")
			}
			c.exec("INSERT INTO t VALUES(1)", 1)
			c.run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	})
	t.Run("folded_true_predicate", func(t *testing.T) {
		usingIndexScenario(t, func(c *usingIndexCase) {
			// Pinned 18.0 removes this tautology; spelling WHERE is not eligibility.
			c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a) WHERE TRUE")
			old := c.oid("ux_idx")
			c.notices = nil
			c.exec("ALTER TABLE ux_t ADD UNIQUE USING INDEX ux_idx", 0)
			c.quiet()
			if c.ids("ux_idx")[2] != old {
				c.t.Fatal("folded predicate attachment replaced index")
			}
			c.rows()
			c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t VALUES(1,'other',NULL)", fixture.UniqueReferenceDuplicate("ux_t", "ux_idx", "Key (a)=(1) already exists.")))
		})
	})
}

func TestReferenceUsingIndexDiagnostics(t *testing.T) {
	const detail = "Cannot create a primary key or unique constraint using such an index."
	cases := []struct {
		name, setup, index, code, message, detail string
		noPosition                                bool
	}{
		{"missing", "", "absent", "42704", `index "absent" does not exist`, "", false},
		{"nonindex", "CREATE TABLE ux_idx(k INT)", "ux_idx", "42809", `"ux_idx" is not an index`, "", true},
		{"wrong_parent", "CREATE TABLE other(k INT); CREATE UNIQUE INDEX ux_idx ON other(k)", "ux_idx", "55000", `index "ux_idx" does not belong to table "ux_t"`, "", false},
		{"associated_wrong_parent", "CREATE TABLE other(k INT CONSTRAINT ux_idx UNIQUE)", "ux_idx", "55000", `index "ux_idx" is already associated with a constraint`, "", false},
		{"nonunique_expression", "CREATE INDEX ux_idx ON ux_t((a+1)) WHERE a>0", "ux_idx", "42809", `"ux_idx" is not a unique index`, detail, false},
		{"expression_partial", "CREATE UNIQUE INDEX CONCURRENTLY ux_idx ON ux_t((a+1)) WHERE a>0", "ux_idx", "42809", `index "ux_idx" contains expressions`, detail, false},
		{"partial", "CREATE UNIQUE INDEX ux_idx ON ux_t(a) WHERE a>0", "ux_idx", "42809", `"ux_idx" is a partial index`, detail, false},
		{"hash", "CREATE INDEX ux_idx ON ux_t USING hash(a)", "ux_idx", "42809", `"ux_idx" is not a unique index`, detail, false},
		{"descending", "CREATE UNIQUE INDEX ux_idx ON ux_t(a DESC)", "ux_idx", "42809", `index "ux_idx" column number 1 does not have default sorting behavior`, detail, false},
		{"nulls_first", "CREATE UNIQUE INDEX ux_idx ON ux_t(a ASC NULLS FIRST)", "ux_idx", "42809", `index "ux_idx" column number 1 does not have default sorting behavior`, detail, false},
		{"pattern", "CREATE UNIQUE INDEX ux_idx ON ux_t(b text_pattern_ops)", "ux_idx", "42809", `index "ux_idx" column number 1 does not have default sorting behavior`, detail, false},
		{"different_collation", "CREATE UNIQUE INDEX ux_idx ON ux_t(b COLLATE \"C\")", "ux_idx", "42809", `index "ux_idx" column number 1 does not have default sorting behavior`, detail, false},
		{"second_key_sort", "CREATE UNIQUE INDEX ux_idx ON ux_t(a,b DESC)", "ux_idx", "42809", `index "ux_idx" column number 2 does not have default sorting behavior`, detail, false},
		{"repeat", "CREATE UNIQUE INDEX ux_idx ON ux_t(a,a)", "ux_idx", "42701", `column "a" appears twice in unique constraint`, "", false},
	}
	for _, x := range cases {
		t.Run(x.name, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.setup("CREATE UNIQUE INDEX healthy ON ux_t(a,b)")
				if x.name != "expression_partial" || c.b.PG {
					for _, q := range strings.Split(x.setup, "; ") {
						if q != "" {
							c.exec(q, 0)
						}
					}
				}
				if x.name == "expression_partial" && !c.b.PG {
					// Unique concurrent expressions are represented, but partial concurrent
					// builds are not. The independent partial case still pins rejection.
					c.exec("CREATE UNIQUE INDEX CONCURRENTLY ux_idx ON ux_t((a+1))", 0)
				}
				var original int64
				if x.index != "absent" {
					original = c.oid(x.index)
				}
				originalState := c.snapshot()
				q := "ALTER TABLE ux_t ADD CONSTRAINT \"é_owned\" UNIQUE USING INDEX " + x.index
				want := usingIndexDiagnostic(x.code, x.message)
				want.Detail = x.detail
				if !x.noPosition {
					want.Position = usingIndexConstraintPosition(q)
				}
				c.failure(q, want, false)
				if original != 0 && c.oid(x.index) != original {
					c.t.Fatal("eligibility rejection lost original object")
				}
				c.unchanged(originalState)
				c.rows()
				c.run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='é_owned'", []string{"conname"}, []string{"NAME"}, nil))
				c.exec("ALTER TABLE ux_t ADD UNIQUE USING INDEX healthy", 0)
				c.exec("INSERT INTO ux_t VALUES(3,'z','reused')", 1)
			})
		})
	}
	t.Run("invalid_unique", func(t *testing.T) {
		usingIndexScenario(t, func(c *usingIndexCase) {
			c.exec("CREATE TABLE ux_t(a INT,b TEXT,payload TEXT)", 0)
			c.exec("INSERT INTO ux_t VALUES(1,'x','kept'),(1,'y',NULL)", 2)
			_, err := c.conn.ExecContext(c.p.Ctx, "CREATE UNIQUE INDEX CONCURRENTLY ux_idx ON ux_t(a)")
			if err == nil {
				c.t.Fatal("invalid index fixture unexpectedly succeeded")
			}
			original := c.snapshot()
			want := usingIndexDiagnostic("55000", `index "ux_idx" is not valid`)
			want.Position = "22"
			c.failure("ALTER TABLE ux_t ADD UNIQUE USING INDEX ux_idx", want, false)
			c.unchanged(original)
			c.run(fixture.Select("SELECT a,b,payload FROM ux_t ORDER BY b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "kept"}, {int64(1), "y", nil}}))
			c.exec("CREATE UNIQUE INDEX healthy ON ux_t(a,b)", 0)
			c.exec("ALTER TABLE ux_t ADD UNIQUE USING INDEX healthy", 0)
			c.exec("INSERT INTO ux_t VALUES(2,'z',NULL)", 1)
		})
	})
	t.Run("repeated_primary_key", func(t *testing.T) {
		usingIndexScenario(t, func(c *usingIndexCase) {
			c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,a)")
			q := "ALTER TABLE ux_t ADD PRIMARY KEY USING INDEX ux_idx"
			want := usingIndexDiagnostic("42701", `column "a" appears twice in primary key constraint`)
			want.Position = usingIndexConstraintPosition(q)
			s := fixture.ErrorDiagnostic(q, want)
			s.ErrorStage = "execute"
			c.run(s)
			c.rows()
		})
	})
	for _, tail := range []string{"garbage", "INCLUDE(payload)", ".qualified", "NOT DEFERRABLE nonsense", "DEFERRABLE nonsense"} {
		t.Run("suffix/"+tail, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a)")
				original := c.snapshot()
				token, position := "garbage", "48"
				switch tail {
				case "INCLUDE(payload)":
					token = "INCLUDE"
				case ".qualified":
					token = "."
				case "NOT DEFERRABLE nonsense":
					token, position = "nonsense", "63"
				case "DEFERRABLE nonsense":
					token, position = "nonsense", "59"
				}
				want := usingIndexDiagnostic("42601", fmt.Sprintf(`syntax error at or near "%s"`, token))
				want.Position = position
				c.failure("ALTER TABLE ux_t ADD UNIQUE USING INDEX ux_idx "+tail, want, true)
				c.unchanged(original)
				c.rows()
			})
		})
	}
	t.Run("read_only", func(t *testing.T) {
		usingIndexScenario(t, func(c *usingIndexCase) {
			c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a)")
			c.exec("BEGIN READ ONLY", 0)
			s := fixture.ErrorDiagnostic("ALTER TABLE ux_t ADD UNIQUE USING INDEX ux_idx", usingIndexDiagnostic("25006", "cannot execute ALTER TABLE in a read-only transaction"))
			s.ErrorStage = "execute"
			c.run(s)
			c.exec("ROLLBACK", 0)
			c.rows()
		})
	})
	t.Run("owner", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ux_actor"})
		usingIndexScenario(t, func(c *usingIndexCase) {
			c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b)")
			original := c.oid("ux_idx")
			c.exec("CREATE ROLE ux_actor", 0)
			// SQL ACL management is an explicit real-server setup boundary.
			if c.b.PG {
				c.exec("GRANT USAGE ON SCHEMA public TO ux_actor", 0)
			}
			q := "ALTER TABLE ux_t ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx"
			held, err := c.conn.PrepareContext(c.p.Ctx, q)
			if err != nil {
				c.t.Fatal(err)
			}
			defer held.Close()
			c.exec("SET SESSION AUTHORIZATION ux_actor", 0)
			defer c.conn.ExecContext(c.p.Ctx, "RESET SESSION AUTHORIZATION")
			s := fixture.ErrorDiagnostic(q, usingIndexDiagnostic("42501", "must be owner of table ux_t"))
			s.ErrorStage = "execute"
			fixture.RunStep(c.t, c.p.Ctx, fixture.HeldStatement{Executor: c.conn, Statement: held, SQL: q}, c.recorder, s)
			c.exec("RESET SESSION AUTHORIZATION", 0)
			if c.oid("ux_idx") != original {
				c.t.Fatal("authorization failure changed index")
			}
			c.rows()
			fixture.RunStep(c.t, c.p.Ctx, fixture.HeldStatement{Executor: c.conn, Statement: held, SQL: q}, c.recorder, fixture.Exec(q, 0))
			// Public-schema reset does not remove roles between prepared routes.
			if c.b.PG {
				c.exec("REVOKE USAGE ON SCHEMA public FROM ux_actor", 0)
			}
			c.exec("DROP ROLE ux_actor", 0)
		})
	})
}

func TestReferenceUsingIndexNames(t *testing.T) {
	for _, x := range []struct{ name, table, setup string }{
		{"relation", "CREATE TABLE ux_t(a INT,b TEXT,payload TEXT)", "CREATE TABLE ux_owned(k INT)"},
		{"check", "CREATE TABLE ux_t(a INT,b TEXT,payload TEXT,CONSTRAINT ux_owned CHECK(a>0))", ""},
		{"not_null", "CREATE TABLE ux_t(a INT,b TEXT,payload TEXT)", "ALTER TABLE ux_t ADD CONSTRAINT ux_owned NOT NULL a"},
	} {
		t.Run(x.name, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.exec(x.table, 0)
				c.exec("INSERT INTO ux_t VALUES(1,'x','kept'),(2,'y',NULL)", 2)
				c.exec("CREATE UNIQUE INDEX ux_idx ON ux_t(a)", 0)
				if x.setup != "" {
					c.exec(x.setup, 0)
				}
				old := c.oid("ux_idx")
				original := c.snapshot()
				want := usingIndexCatalogDuplicate(c.oid("ux_t"), "ux_owned")
				if x.name == "relation" {
					want = usingIndexDiagnostic("42P07", `relation "ux_owned" already exists`)
				}
				c.failure("ALTER TABLE ux_t ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx", want, false, usingIndexRename("ux_idx", "ux_owned"))
				if c.oid("ux_idx") != old {
					c.t.Fatal("collision changed original index")
				}
				c.rows()
				c.unchanged(original)
			})
		})
	}
	t.Run("foreign_key", func(t *testing.T) {
		for _, primary := range []bool{false, true} {
			name, kind := "unique", "UNIQUE"
			if primary {
				name, kind = "primary", "PRIMARY KEY"
			}
			t.Run(name, func(t *testing.T) {
				usingIndexScenario(t, func(c *usingIndexCase) {
					c.exec("CREATE TABLE ux_parent(k INT PRIMARY KEY)", 0)
					c.exec("INSERT INTO ux_parent VALUES(1),(2),(3)", 3)
					c.exec("CREATE TABLE ux_t(a INT,b TEXT,payload TEXT,CONSTRAINT ux_owned FOREIGN KEY(a) REFERENCES ux_parent(k))", 0)
					c.exec("INSERT INTO ux_t VALUES(1,'x','kept'),(2,'y',NULL)", 2)
					c.exec("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)", 0)
					parent, index := c.oid("ux_t"), c.oid("ux_idx")
					var foreignKey int64
					if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_constraint WHERE conname='ux_owned' AND contype='f' AND conrelid=$1", parent).Scan(&foreignKey); err != nil || foreignKey <= 0 {
						c.t.Fatal("original FK identity", foreignKey, err)
					}
					checkFK := func() {
						c.run(fixture.Select("SELECT oid::bigint,conname,contype,conrelid::bigint,pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_owned' AND contype='f' AND conrelid=$1", []string{"oid", "conname", "contype", "conrelid", "pg_get_constraintdef"}, []string{"INT8", "NAME", "CHAR", "INT8", "TEXT"}, [][]any{{foreignKey, "ux_owned", "f", parent, "FOREIGN KEY (a) REFERENCES ux_parent(k)"}}, parent))
						c.run(fixture.Select("SELECT k FROM ux_parent ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					}
					checkFK()
					c.rows()
					c.attributes([][]any{{"a", int64(23), false}, {"b", int64(25), false}, {"payload", int64(25), false}})
					c.indexAttributes("ux_idx", [][]any{{"a", int64(23)}, {"b", int64(25)}, {"payload", int64(25)}})
					original := c.snapshot()
					// REL_18_0 pg_constraint's name uniqueness spans admitted kinds;
					// this is an independently expected catalog23505, not native parity.
					c.failure("ALTER TABLE ux_t ADD CONSTRAINT ux_owned "+kind+" USING INDEX ux_idx", usingIndexCatalogDuplicate(parent, "ux_owned"), false, usingIndexRename("ux_idx", "ux_owned"))
					if c.oid("ux_idx") != index {
						c.t.Fatal("FK collision replaced original standalone index")
					}
					c.unchanged(original)
					checkFK()
					c.rows()
					c.attributes([][]any{{"a", int64(23), false}, {"b", int64(25), false}, {"payload", int64(25), false}})
					c.notices = nil
					c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_attached "+kind+" USING INDEX ux_idx", 0)
					c.noticesWant(usingIndexRename("ux_idx", "ux_attached"))
					if ids := c.ids("ux_attached"); ids[1] != parent || ids[2] != index || ids[0] == foreignKey {
						c.t.Fatal("healthy FK-collision retry lost reciprocal original group", ids, parent, index, foreignKey)
					}
					checkFK()
					c.attributes([][]any{{"a", int64(23), primary}, {"b", int64(25), primary}, {"payload", int64(25), false}})
					c.indexAttributes("ux_attached", [][]any{{"a", int64(23)}, {"b", int64(25)}, {"payload", int64(25)}})
					c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_attached'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{kind + " (a, b) INCLUDE (payload)"}}))
					c.rows()
					c.exec("INSERT INTO ux_t VALUES(3,'z',NULL)", 1)
					c.run(fixture.Select("SELECT a,b,payload FROM ux_t ORDER BY a", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}, {int64(3), "z", nil}}))
				})
			})
		}
	})
	t.Run("target_namespace", func(t *testing.T) {
		fixture.UniqueReferenceSchemaCleanup(t, "ux_shadow")
		usingIndexScenario(t, func(c *usingIndexCase) {
			defer func() {
				if _, err := c.conn.ExecContext(c.p.Ctx, "DROP SCHEMA IF EXISTS ux_shadow CASCADE"); err != nil {
					c.t.Error(err)
				}
			}()
			c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a)")
			old := c.oid("ux_idx")
			c.exec("CREATE SCHEMA ux_shadow", 0)
			c.exec("CREATE TABLE ux_shadow.shadow(a INT)", 0)
			c.exec("CREATE UNIQUE INDEX ux_idx ON ux_shadow.shadow(a)", 0)
			c.exec("SET search_path=ux_shadow,public", 0)
			c.exec("ALTER TABLE public.ux_t ADD UNIQUE USING INDEX ux_idx", 0)
			if c.ids("ux_idx")[2] != old {
				c.t.Fatal("search_path selected wrong index")
			}
			c.exec("RESET search_path", 0)
			c.exec("DROP SCHEMA ux_shadow CASCADE", 0)
		})
	})
}
