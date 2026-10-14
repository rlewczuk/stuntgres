package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGConstraintExclusionMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, typ, declaration string
		declared, canonical    sqltype.ID
		operator               OID
		first, second          string
	}{
		{"cidr", "CIDR", "", sqltype.Cidr, sqltype.Inet, 1201, "192.0.2.0/24", "198.51.100.0/24"},
		{"inet", "INET", "", sqltype.Inet, sqltype.Inet, 1201, "192.0.2.1", "198.51.100.1"},
		{"oid", "OID", "", sqltype.Oid, sqltype.Oid, 607, "1", "2"},
		{"regclass", "REGCLASS", "", sqltype.Regclass, sqltype.Oid, 607, "", ""},
		{"regtype", "REGTYPE", "", sqltype.Regtype, sqltype.Oid, 607, "", ""},
		{"regproc", "REGPROC", "", sqltype.Regproc, sqltype.Oid, 607, "", ""},
		{"regprocedure", "REGPROCEDURE", "", sqltype.Regprocedure, sqltype.Oid, 607, "", ""},
		{"regrole", "REGROLE", "", sqltype.Regrole, sqltype.Oid, 607, "", ""},
		{"regoper", "REGOPER", "", sqltype.Regoper, sqltype.Oid, 607, "", ""},
		{"regoperator", "REGOPERATOR", "", sqltype.Regoperator, sqltype.Oid, 607, "", ""},
		{"regcollation", "REGCOLLATION", "", sqltype.Regcollation, sqltype.Oid, 607, "", ""},
		{"regnamespace", "REGNAMESPACE", "", sqltype.Regnamespace, sqltype.Oid, 607, "", ""},
		{"domain_cidr", "pc_exunit_domain_cidr", "CREATE DOMAIN pc_exunit_domain_cidr AS CIDR", 0, sqltype.Inet, 1201, "192.0.2.0/24", "198.51.100.0/24"},
		{"domain_oid", "pc_exunit_domain_oid", "CREATE DOMAIN pc_exunit_domain_oid AS REGOPERATOR", 0, sqltype.Oid, 607, "", ""},
		{"varchar", "VARCHAR(20)", "", sqltype.VarChar, sqltype.Text, 98, "first", "second"},
		{"enum", "pc_exunit_enum", "CREATE TYPE pc_exunit_enum AS ENUM ('first','second')", 0, 3500, 3516, "first", "second"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			if tc.declaration != "" {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, tc.declaration))
			}
			view := e.CatalogRead()
			var typ *sqltype.Descriptor
			if tc.declared != 0 {
				var ok bool
				typ, ok = view.LookupType(tc.declared)
				if !ok {
					t.Fatal("missing declared type", tc.declared)
				}
			} else {
				typ = domainUnitType(t, view, tc.typ).typ
			}
			class, ok := sqltype.DefaultIndexClass(typ)
			if !ok {
				t.Fatal("missing admitted class", tc.typ)
			}
			bound, err := sqltype.BindExclusionOperator(typ.Usage(), class, "=")
			if err != nil {
				t.Fatal("existing exclusion admission", err)
			}
			inputs, err := bound.EqualityInputs()
			if err != nil || inputs != ([2]sqltype.ID{tc.canonical, tc.canonical}) {
				t.Fatal("canonical operator inputs", inputs, tc.canonical, err)
			}
			identity, err := constraintOperator(view, bound.Name, inputs[0], inputs[1])
			if err != nil || identity != tc.operator {
				t.Fatal("released equality identity", identity, tc.operator, err)
			}
			if !bound.MatchesUsage(typ.Usage()) {
				t.Fatal("metadata changed original comparison generation")
			}
			if tc.first != "" {
				first, err := typ.Input(context.Background(), tc.first, -1)
				if err != nil {
					t.Fatal(err)
				}
				second, err := typ.Input(context.Background(), tc.second, -1)
				if err != nil {
					t.Fatal(err)
				}
				if equal, err := bound.Equal(context.Background(), first, first); err != nil || !equal {
					t.Fatal("original equality", equal, err)
				}
				if equal, err := bound.Equal(context.Background(), first, second); err != nil || equal {
					t.Fatal("distinct original inputs", equal, err)
				}
			}

			// Exercise the real owner binder and capture, including a separate row
			// whose lookup must remain usable when the normalized exclusion exists.
			for _, query := range []string{
				"CREATE TABLE pc_exunit_ordinary(a INT,CONSTRAINT pc_exunit_ck CHECK(a>0))",
				"CREATE TABLE pc_exunit_excluded(a " + tc.typ + ",CONSTRAINT pc_exunit_x EXCLUDE USING btree(a WITH =))",
			} {
				domainUnitPublish(t, e, pgConstraintUnitTableStage(t, e, op, query))
			}
			rows := pgConstraintUnitCapture(t, e, e.CatalogRead())
			if len(rows) != 2 || rows["pc_exunit_ck"] == nil || rows["pc_exunit_x"] == nil {
				t.Fatal("catalog-wide capture", rows)
			}
			operators := rows["pc_exunit_x"][26]
			if operators.ArrayRank() != 1 || operators.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 1}) || operators.ArrayLen() != 1 {
				t.Fatal("operator vector shape", operators)
			}
			id, err := operators.ArrayElement(0).OID()
			if err != nil || OID(id) != tc.operator {
				t.Fatal("captured canonical exclusion operator", id, tc.operator, err)
			}
			if got := pgConstraintUnitText(t, rows["pc_exunit_ck"][20]); got != "{1}" {
				t.Fatal("unrelated CHECK key", got)
			}
		})
	}
	if _, err := (sqltype.ExclusionOperator{}).EqualityInputs(); err == nil {
		t.Fatal("unbound exclusion signature accepted")
	}
}
