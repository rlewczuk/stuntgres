//go:build reference

package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceViewReplaceBinding(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(7)", 1), referenceExec("CREATE VIEW v AS SELECT id FROM t", 0)} {
		p.step(t, s)
	}
	for _, tc := range []struct{ query, token, code, message string }{
		{"CREATE OR REPLACE VIEW v AS SELECT /* café */ missing FROM t", "missing", "42703", "column \"missing\" does not exist"},
		{"CREATE OR REPLACE VIEW v AS SELECT /* café */ $1 AS id FROM t", "$1", "42P02", "there is no parameter $1"},
		{"CREATE OR REPLACE VIEW v(x,y) AS SELECT id FROM t", "", "42601", "CREATE VIEW specifies more column names than columns"},
		{"CREATE OR REPLACE VIEW v AS SELECT id,id FROM t", "", "42701", "column \"id\" of relation \"v\" already exists"},
	} {
		position := ""
		if tc.token != "" {
			position = strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1)
		}
		s := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position})
		t.Run(tc.query, func(t *testing.T) {
			t.Run("postgres_first", func(t *testing.T) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) })
			t.Run("paired", func(t *testing.T) { p.step(t, s) })
		})
	}
	p.step(t, referenceSelect("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferenceViewReplaceCollateKeyword(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
	// PostgreSQL accepts COLLATE as a bare output label when no collation name
	// follows. Both implicit and explicit labels must work.
	s := referenceSelect("SELECT id collate FROM t", []string{"collate"}, []string{"INT4"}, [][]any{{int64(7)}})
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
	p.step(t, s)
	p.step(t, referenceSelect("SELECT id AS collate FROM t", []string{"collate"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferenceViewReplaceOptions(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("CREATE VIEW v WITH(security_barrier=true,security_invoker=true) AS SELECT id FROM t", 0), referenceExec("ALTER VIEW v SET(security_invoker=false)", 0), referenceExec("ALTER VIEW v RESET(security_barrier)", 0), referenceExec("ALTER VIEW v RESET(unknown_option,unknown_option,check_option)", 0), referenceExec("CREATE OR REPLACE VIEW v AS SELECT id FROM t", 0)} {
		p.step(t, s)
	}
	// PostgreSQL permits check_option on this automatically updatable view;
	// TestViewReplaceCapabilityBoundaries independently asserts native exclusion.
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("ALTER VIEW v SET(check_option=local)", 0))
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("ALTER VIEW v RESET(check_option)", 0))
}

func TestReferenceViewReplaceReadOnly(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct{ query, tag string }{{"CREATE OR REPLACE VIEW absent AS SELECT id FROM missing", "CREATE VIEW"}, {"ALTER VIEW absent RENAME TO renamed", "ALTER VIEW"}, {"DROP VIEW absent", "DROP VIEW"}} {
		for _, backend := range []struct {
			db     *sql.DB
			record *referenceRecorder
		}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, c, backend.record, referenceExec("BEGIN READ ONLY", 0))
			referenceRunStep(t, p.ctx, c, backend.record, referenceImplicitError(tc.query, referenceDiagnostic{Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"}))
			referenceRunStep(t, p.ctx, c, backend.record, referenceExec("ROLLBACK", 0))
			c.Close()
		}
	}
}
