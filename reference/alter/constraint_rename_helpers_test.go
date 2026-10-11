package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func constraintRenameScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			body(t, p, b, c)
		})
	}
}

type constraintRenameKind struct {
	kind, declaration, definition string
	indexed                       bool
}

var constraintRenameKinds = []constraintRenameKind{
	{"n", "k INT CONSTRAINT old NOT NULL,payload TEXT", "NOT NULL k", false},
	{"c", "k INT,payload TEXT,CONSTRAINT old CHECK(k>0) NO INHERIT", "CHECK ((k > 0)) NO INHERIT", false},
	{"u", "k INT CONSTRAINT old UNIQUE,payload TEXT", "UNIQUE (k)", true},
	{"p", "k INT CONSTRAINT old PRIMARY KEY,payload TEXT", "PRIMARY KEY (k)", true},
	{"x", "k INT,payload TEXT,CONSTRAINT old EXCLUDE(k WITH =)", "EXCLUDE USING btree (k WITH =)", true},
}

func constraintRenameViolation(k constraintRenameKind, name string) (string, fixture.Diagnostic) {
	switch k.kind {
	case "n":
		return "INSERT INTO t VALUES(NULL,'bad')", fixture.PrimaryKeyReferenceNull("t", "k", "null, bad")
	case "c":
		d := fixture.CheckViolation("t", name, "0, bad")
		d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
		return "INSERT INTO t VALUES(0,'bad')", d
	case "x":
		return "INSERT INTO t VALUES(1,'bad')", fixture.ExclusionReferenceConflict("t", name, "k", "1", "1")
	default:
		return "INSERT INTO t VALUES(1,'bad')", fixture.UniqueReferenceDuplicate("t", name, "Key (k)=(1) already exists.")
	}
}

type constraintRenameIdentity struct {
	constraint, parent, index int64
	definition                string
}

func constraintRenameCapture(t *testing.T, p *fixture.Pair, c *sql.Conn, name string, indexed bool) constraintRenameIdentity {
	t.Helper()
	var v constraintRenameIdentity
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,conrelid,pg_get_constraintdef(oid) FROM pg_constraint WHERE conname=$1 AND conrelid='t'::regclass::oid", name).Scan(&v.constraint, &v.parent, &v.definition); err != nil {
		t.Fatal(err)
	}
	if v.constraint <= 0 || v.parent <= 0 || v.constraint == v.parent {
		t.Fatal("invalid constraint identity", v)
	}
	if indexed {
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&v.index); err != nil {
			t.Fatal(err)
		}
		if v.index <= 0 || v.index == v.constraint || v.index == v.parent {
			t.Fatal("invalid backing identity", v)
		}
	}
	return v
}
func constraintRenameNames(run func(fixture.Step), name string, k constraintRenameKind) {
	run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conname=$1", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{name, k.kind}}, name))
	if k.indexed {
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{name}}, name))
	}
}
func constraintRenameError(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}
func constraintRenameMissing(name string) fixture.Diagnostic {
	return constraintRenameError("42704", fmt.Sprintf(`constraint "%s" for table "t" does not exist`, name))
}
