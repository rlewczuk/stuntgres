package reference_test

import "testing"

func TestReferenceTableCoreExternalDependencyBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			// PK is shared functionality. FK dependency propagation remains a
			// real PostgreSQL phase; native views retain their independent path.
			run(referenceExec("CREATE TABLE native_pk(id INT PRIMARY KEY)", 0))
			run(referenceExec("INSERT INTO native_pk VALUES(1)", 1))
			run(referenceErrorDiagnostic("INSERT INTO native_pk VALUES(1)", uniqueReferenceDuplicate("native_pk", "native_pk_pkey", "Key (id)=(1) already exists.")))
			run(referenceErrorDiagnostic("INSERT INTO native_pk VALUES(NULL)", primaryKeyReferenceNull("native_pk", "id", "null")))
			if b.pg {
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
				run(referenceSelect("SELECT * FROM orders", []string{"product_no"}, []string{"INT4"}, nil))
				if _, err = referenceAdmin.ExecContext(p.ctx, "INSERT INTO orders VALUES(99)"); err != nil {
					t.Fatal("FK survived cascade", err)
				}
				rows, err := referenceAdmin.QueryContext(p.ctx, "SELECT * FROM product_view")
				if rows != nil {
					rows.Close()
				}
				referenceState(t, err, "42P01")
			} else {
				run(referenceImplicitError("CREATE TABLE native_fk(id INT REFERENCES products)", referenceDiagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: "31"}))
				run(referenceExec("CREATE TABLE orders(product_no INT)", 0))
				run(referenceExec("INSERT INTO orders VALUES(99)", 1))
			}
			run(referenceExec("CREATE VIEW native_view AS SELECT product_no FROM orders", 0))
			run(referenceSelect("SELECT product_no FROM native_view", []string{"product_no"}, []string{"INT4"}, [][]any{{int64(99)}}))
		})
	}
}

func TestReferenceTableCoreQuotedLikeSourceDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range []struct{ q, message string }{{`CREATE TABLE clone (LIKE "a""b")`, `relation "a"b" does not exist`}, {`CREATE TABLE clone (LIKE "a\b")`, `relation "a\b" does not exist`}} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42P01", Message: tc.message, Position: "26"})
			s.prepared = prepared
			s.errorStage = "execute"
			p.step(t, s)
		}
	}
}
