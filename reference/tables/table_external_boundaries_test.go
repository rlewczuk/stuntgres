package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableCoreExternalDependencyBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			// PK is shared functionality. FK dependency propagation remains a
			// real PostgreSQL phase; native views retain their independent path.
			run(fixture.Exec("CREATE TABLE native_pk(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("INSERT INTO native_pk VALUES(1)", 1))
			run(fixture.ErrorDiagnostic("INSERT INTO native_pk VALUES(1)", fixture.UniqueReferenceDuplicate("native_pk", "native_pk_pkey", "Key (id)=(1) already exists.")))
			run(fixture.ErrorDiagnostic("INSERT INTO native_pk VALUES(NULL)", fixture.PrimaryKeyReferenceNull("native_pk", "id", "null")))
			if b.PG {
				for _, q := range []string{"CREATE TABLE products(id integer primary key)", "CREATE TABLE orders(product_no integer references products(id))", "CREATE VIEW product_view AS SELECT id FROM products"} {
					if _, err := fixture.Admin.ExecContext(p.Ctx, q); err != nil {
						t.Fatal(err)
					}
				}
				_, err := fixture.Admin.ExecContext(p.Ctx, "DROP TABLE products RESTRICT")
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop table products because other objects depend on it", Detail: "constraint orders_product_no_fkey on table orders depends on table products\nview product_view depends on table products", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				fixture.AssertDiagnostic(t, err, &want)
				if _, err = fixture.Admin.ExecContext(p.Ctx, "DROP TABLE products CASCADE"); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT * FROM orders", []string{"product_no"}, []string{"INT4"}, nil))
				if _, err = fixture.Admin.ExecContext(p.Ctx, "INSERT INTO orders VALUES(99)"); err != nil {
					t.Fatal("FK survived cascade", err)
				}
				rows, err := fixture.Admin.QueryContext(p.Ctx, "SELECT * FROM product_view")
				if rows != nil {
					rows.Close()
				}
				fixture.State(t, err, "42P01")
			} else {
				run(fixture.ImplicitError("CREATE TABLE native_fk(id INT REFERENCES products)", fixture.Diagnostic{Code: "0A000", Message: "column constraints are unsupported", Position: "31"}))
				run(fixture.Exec("CREATE TABLE orders(product_no INT)", 0))
				run(fixture.Exec("INSERT INTO orders VALUES(99)", 1))
			}
			run(fixture.Exec("CREATE VIEW native_view AS SELECT product_no FROM orders", 0))
			run(fixture.Select("SELECT product_no FROM native_view", []string{"product_no"}, []string{"INT4"}, [][]any{{int64(99)}}))
		})
	}
}

func TestReferenceTableCoreQuotedLikeSourceDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct{ q, message string }{{`CREATE TABLE clone (LIKE "a""b")`, `relation "a"b" does not exist`}, {`CREATE TABLE clone (LIKE "a\b")`, `relation "a\b" does not exist`}} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42P01", Message: tc.message, Position: "26"})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			p.Step(t, s)
		}
	}
}
