//go:build reference

package reference_test

import (
	"testing"
)

func TestReferenceTableCoreExternalDependencyBoundary(t *testing.T) {
	p := newReferencePair(t)
	// Foreign keys and primary keys remain PostgreSQL-only; views have an
	// independently asserted native dependency path as well.
	for _, q := range []string{"CREATE TABLE products(id integer primary key)", "CREATE TABLE orders(product_no integer references products(id))", "CREATE VIEW product_view AS SELECT id FROM products"} {
		if _, err := referenceAdmin.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_, err := referenceAdmin.ExecContext(p.ctx, "DROP TABLE products RESTRICT")
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop table products because other objects depend on it", Detail: "constraint orders_product_no_fkey on table orders depends on table products\nview product_view depends on table products", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	referenceAssertDiagnostic(t, err, &want)
	if _, err = referenceAdmin.ExecContext(p.ctx, "DROP TABLE products CASCADE"); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.postgres, &referenceRecorder{}, referenceSelect("SELECT * FROM orders", []string{"product_no"}, []string{"INT4"}, nil))
	if _, err = referenceAdmin.ExecContext(p.ctx, "INSERT INTO orders VALUES(99)"); err != nil {
		t.Fatal("FK survived cascade", err)
	}
	rows, err := referenceAdmin.QueryContext(p.ctx, "SELECT * FROM product_view")
	if rows != nil {
		rows.Close()
	}
	referenceState(t, err, "42P01")
	for _, tc := range []struct{ q, message, position string }{{"CREATE TABLE native_pk(id INT PRIMARY KEY)", "column constraints are unsupported", "31"}, {"CREATE TABLE native_fk(id INT REFERENCES products)", "column constraints are unsupported", "31"}} {
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
		referenceRunStep(t, p.ctx, p.memory, &referenceRecorder{}, s)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE orders(product_no INT)", 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO orders VALUES(99)", 1))
	p.step(t, referenceExec("CREATE VIEW native_view AS SELECT product_no FROM orders", 0))
	p.step(t, referenceSelect("SELECT product_no FROM native_view", []string{"product_no"}, []string{"INT4"}, [][]any{{int64(99)}}))
}
func TestReferenceTableCoreQuotedLikeSourceDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct{ q, message string }{{`CREATE TABLE clone (LIKE "a""b")`, `relation "a"b" does not exist`}, {`CREATE TABLE clone (LIKE "a\b")`, `relation "a\b" does not exist`}} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42P01", Message: tc.message, Position: "26"})
			s.prepared = prepared
			s.errorStage = "execute"
			p.step(t, s)
		}
	}
}
