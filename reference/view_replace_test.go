//go:build reference

package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceViewReplaceDefinition(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,txt TEXT)", 0), referenceExec("CREATE TABLE u(id INT,txt TEXT)", 0), referenceExec("INSERT INTO t VALUES(1,'a')", 1), referenceExec("INSERT INTO u VALUES(2,'b')", 1), referenceExec("CREATE VIEW v AS SELECT id,txt FROM t", 0), referenceExec("CREATE VIEW w AS SELECT id FROM v", 0),
	} {
		p.step(t, s)
	}
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		var oid, rule int64
		if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
			t.Fatal(err)
		}
		r, err := db.ExecContext(p.ctx, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,txt,TRUE AS extra FROM u")
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		var gotOID, gotRule int64
		if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&gotOID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&gotRule); err != nil || gotOID != oid || gotRule != rule {
			t.Fatal(gotOID, gotRule, oid, rule, err)
		}
	}
	for _, s := range []referenceStep{
		referenceSelect("SELECT * FROM v", []string{"id", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}}), referenceSelect("SELECT * FROM v WHERE FALSE", []string{"id", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{}), referenceSelect("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}}), referenceExec("DROP TABLE t", 0), referenceExec("INSERT INTO u VALUES(3,'c')", 1), referenceSelect("SELECT id FROM w ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}}),
		referenceExec("CREATE OR REPLACE VIEW absent AS SELECT id FROM u", 0), referenceExec("CREATE SEQUENCE seq", 0), referenceExec("CREATE VIEW sv AS SELECT nextval('seq') AS n FROM u", 0), referenceExec("CREATE OR REPLACE VIEW sv AS SELECT nextval('seq') AS n FROM u WHERE FALSE", 0), referenceSelect("SELECT last_value,is_called FROM seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
	} {
		p.step(t, s)
	}
}

func TestReferenceViewReplaceDiagnostics(t *testing.T) {
	cases := []struct {
		setup, query string
		want         referenceDiagnostic
	}{
		{"", "CREATE OR REPLACE VIEW v AS SELECT id FROM t", referenceDiagnostic{Code: "42P16", Message: "cannot drop columns from view"}},
		{"", "CREATE OR REPLACE VIEW v AS SELECT id AS renamed,txt FROM t", referenceDiagnostic{Code: "42P16", Message: "cannot change name of view column \"id\" to \"renamed\"", Hint: "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."}},
		{"", "CREATE OR REPLACE VIEW v AS SELECT id::BIGINT AS id,txt FROM t", referenceDiagnostic{Code: "42P16", Message: "cannot change data type of view column \"id\" from integer to bigint"}},
		{"CREATE VIEW cv AS SELECT txt::VARCHAR(3) AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT txt::VARCHAR(4) AS x FROM t", referenceDiagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from character varying(3) to character varying(4)"}},
		{"CREATE VIEW cv AS SELECT id::NUMERIC(10,1) AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT id::NUMERIC(10,2) AS x FROM t", referenceDiagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from numeric(10,1) to numeric(10,2)"}},
		{"CREATE VIEW cv AS SELECT txt COLLATE \"C\" AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT txt COLLATE \"POSIX\" AS x FROM t", referenceDiagnostic{Code: "42P16", Message: "cannot change collation of view column \"x\" from \"C\" to \"POSIX\""}},
		{"", "CREATE OR REPLACE VIEW t AS SELECT id FROM t", referenceDiagnostic{Code: "42809", Message: "\"t\" is not a view"}},
		{"", "ALTER VIEW t RENAME TO other", referenceDiagnostic{Code: "42809", Message: "\"t\" is not a view"}},
		{"", "DROP VIEW IF EXISTS t", referenceDiagnostic{Code: "42809", Message: "\"t\" is not a view", Hint: "Use DROP TABLE to remove a table."}},
		{"", "DROP TABLE v", referenceDiagnostic{Code: "42809", Message: "\"v\" is not a table", Hint: "Use DROP VIEW to remove a view."}},
		{"", "ALTER VIEW v RENAME id TO txt", referenceDiagnostic{Code: "42701", Message: "column \"txt\" of relation \"v\" already exists"}},
		{"", "ALTER VIEW v RENAME COLUMN missing TO z", referenceDiagnostic{Code: "42703", Message: "column \"missing\" does not exist"}},
		{"", "ALTER VIEW v RENAME TO t", referenceDiagnostic{Code: "42P07", Message: "relation \"t\" already exists"}},
		{"CREATE SCHEMA s; CREATE VIEW s.v AS SELECT id FROM t", "ALTER VIEW v SET SCHEMA s", referenceDiagnostic{Code: "42P07", Message: "relation \"v\" already exists in schema \"s\""}},
		{"", "ALTER VIEW absent RENAME TO z", referenceDiagnostic{Code: "42P01", Message: "relation \"absent\" does not exist"}},
		{"", "DROP VIEW absent", referenceDiagnostic{Code: "42P01", Message: "view \"absent\" does not exist"}},
		{"", "DROP VIEW absent_schema.v", referenceDiagnostic{Code: "3F000", Message: "schema \"absent_schema\" does not exist"}},
		{"CREATE VIEW w AS SELECT id FROM v", "DROP VIEW v", referenceDiagnostic{Code: "2BP01", Message: "cannot drop view v because other objects depend on it", Detail: "view w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}},
		{"", "ALTER VIEW v OWNER TO absent_role", referenceDiagnostic{Code: "42704", Message: "role \"absent_role\" does not exist"}},
		{"", "ALTER VIEW v SET(security_invoker=wat)", referenceDiagnostic{Code: "22023", Message: "invalid value for boolean option \"security_invoker\": wat"}},
		{"", "ALTER VIEW v SET(unknown_option=true)", referenceDiagnostic{Code: "22023", Message: "unrecognized parameter \"unknown_option\""}},
		{"", "ALTER VIEW v SET(security_invoker=true,security_invoker=false)", referenceDiagnostic{Code: "22023", Message: "parameter \"security_invoker\" specified more than once"}},
		{"", "ALTER VIEW v RESET(security_invoker=true)", referenceDiagnostic{Code: "42601", Message: "RESET must not include values for parameters"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "s")
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT,txt TEXT)", 0), referenceExec("INSERT INTO t VALUES(1,'a')", 1), referenceExec("CREATE VIEW v AS SELECT id,txt FROM t", 0)} {
				p.step(t, s)
			}
			for _, query := range strings.Split(tc.setup, ";") {
				if query = strings.TrimSpace(query); query != "" {
					p.step(t, referenceExec(query, 0))
				}
			}
			step := referenceImplicitError(tc.query, tc.want)
			t.Run("postgres_first", func(t *testing.T) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step) })
			t.Run("paired", func(t *testing.T) { p.step(t, step) })
			p.step(t, referenceSelect("SELECT * FROM v", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}))
		})
	}
}

func TestReferenceViewReplacePrepared(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(1)", 1), referenceExec("CREATE VIEW v AS SELECT id FROM t", 0)} {
		p.step(t, s)
	}
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		id, err := db.PrepareContext(p.ctx, "SELECT id FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer id.Close()
		star, err := db.PrepareContext(p.ctx, "SELECT * FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer star.Close()
		ddl, err := db.PrepareContext(p.ctx, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,TRUE AS extra FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer ddl.Close()
		var before int64
		if err := db.QueryRowContext(p.ctx, "SELECT id FROM v").Scan(&before); err != nil || before != 1 {
			t.Fatal(before, err)
		}
		for range 2 {
			r, err := ddl.ExecContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 0 {
				t.Fatal(n, err)
			}
		}
		var after int64
		if err := id.QueryRowContext(p.ctx).Scan(&after); err != nil || after != 11 {
			t.Fatal(after, err)
		}
		rows, err := star.QueryContext(p.ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("changed prepared shape returned rows")
		}
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
		referenceAssertDiagnostic(t, err, &want)
	}
	for _, q := range []string{"ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET(security_barrier=true)", "ALTER VIEW renamed RESET(security_barrier)", "DROP VIEW renamed"} {
		p.step(t, referencePrepared(referenceExec(q, 0)))
	}
}

func TestReferenceViewReplaceAlterDrop(t *testing.T) {
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "s")
	for _, s := range []referenceStep{
		referenceExec("CREATE SCHEMA s", 0), referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(7)", 1), referenceExec("CREATE VIEW v AS SELECT id FROM t", 0), referenceExec("CREATE VIEW w AS SELECT id FROM v", 0), referenceExec("ALTER VIEW v RENAME COLUMN id TO key", 0), referenceExec("ALTER VIEW v RENAME TO renamed", 0), referenceExec("ALTER VIEW renamed SET SCHEMA s", 0), referenceExec("ALTER VIEW s.renamed OWNER TO CURRENT_USER", 0), referenceExec("CREATE VIEW v AS SELECT 99 AS id FROM t", 0), referenceSelect("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}), referenceImplicitError("DROP VIEW v,w,missing", referenceDiagnostic{Code: "42P01", Message: "view \"missing\" does not exist"}), referenceSelect("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}), referenceExec("DROP VIEW s.renamed,w RESTRICT", 0), referenceExec("DROP VIEW IF EXISTS absent,v", 0), referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}),
		referenceExec("CREATE VIEW a AS SELECT id FROM t", 0), referenceExec("CREATE VIEW b AS SELECT id FROM a", 0), referenceExec("CREATE VIEW c AS SELECT id FROM b", 0), referenceExec("DROP VIEW a CASCADE", 0), referenceSelect("SELECT count(*) FROM pg_class WHERE relname='a' OR relname='b' OR relname='c'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
	} {
		p.step(t, s)
	}
}

func TestReferenceViewReplaceCycles(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(7)", 1), referenceExec("CREATE VIEW v AS SELECT id FROM t", 0), referenceExec("CREATE OR REPLACE VIEW v AS SELECT id FROM v", 0), referenceImplicitError("SELECT id FROM v", referenceDiagnostic{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""}), referencePrepared(referenceImplicitError("SELECT id FROM v", referenceDiagnostic{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""})), referenceImplicitError("SELECT id FROM v FOR UPDATE", referenceDiagnostic{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""}), referenceExec("CREATE OR REPLACE VIEW v AS SELECT id FROM t", 0), referenceExec("CREATE VIEW w AS SELECT id FROM v", 0), referenceExec("CREATE OR REPLACE VIEW v AS SELECT id FROM w", 0), referenceImplicitError("SELECT id FROM v", referenceDiagnostic{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""}), referenceExec("DROP VIEW v,w RESTRICT", 0), referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})} {
		p.step(t, s)
	}
}
