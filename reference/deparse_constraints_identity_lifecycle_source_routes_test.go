package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsDefinitionIdentitiesSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			db := b.db
			db.SetMaxOpenConns(1)
			postgres := b.pg
			run := func(s referenceStep) referenceObserved {
				s.nonContext = true
				if s.selectRows && s.diagnostic == nil {
					previous := s.checkMetadata
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						deparseReviewMetadata(t, b.pg, rows)
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				return referenceRunStep(t, p.ctx, db, b.recorder, s)
			}
			run(referenceExec("CREATE TABLE t(a INT,b BIGINT DEFAULT 7 CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a::bigint>0))", 0))
			cid := deparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'")
			rid := deparseSourceDefinitionID(t, db, "SELECT conrelid FROM pg_catalog.pg_constraint WHERE conname='ck'")
			did := deparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef")
			if cid == rid || cid == did || rid == did {
				t.Fatal("identities collide", cid, rid, did)
			}
			_, relation := deparseReviewTypedExpected(t, postgres, "OID", strconv.FormatInt(rid, 10))
			run(referenceSelect("SELECT adrelid,adnum FROM pg_catalog.pg_attrdef", []string{"adrelid", "adnum"}, []string{"OID", "INT2"}, [][]any{{relation, int64(2)}}))
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					query string
					want  any
					args  []any
				}{
					{"SELECT pg_get_constraintdef($1)", "CHECK (((a)::bigint > 0))", []any{cid}},
					{"SELECT pg_get_constraintdef($1,false)", "CHECK (((a)::bigint > 0))", []any{cid}},
					{"SELECT pg_get_constraintdef($1,NULL)", nil, []any{cid}},
				} {
					step := referenceSelect(tc.query, []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.args...)
					step.prepared = prepared
					run(step)
				}
				step := referenceSelect("SELECT pg_get_constraintdef($1,$2)", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK (a::bigint > 0)"}}, cid, true)
				step.prepared = prepared
				run(step)
				step = referenceSelect("SELECT pg_get_constraintdef(oid) FROM pg_catalog.pg_constraint WHERE conname='nn'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"NOT NULL b"}})
				step.prepared = prepared
				run(step)
			}
			rows, err := db.Query("SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='ck'")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			metadata, err := rows.ColumnTypes()
			wantScan := reflect.TypeFor[string]()
			if postgres {
				wantScan = reflect.TypeFor[any]()
			}
			if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "PG_NODE_TREE" || metadata[0].ScanType() != wantScan {
				t.Fatal(metadata, err)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var tree string
			if err := rows.Scan(&tree); err != nil || tree == "" || tree == "((a)::bigint > 0)" || tree == "a::bigint>0" {
				t.Fatal(tree, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceDeparseConstraintsKindsAndDefaultIdentitySourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			db := b.db
			db.SetMaxOpenConns(1)
			run := func(s referenceStep) referenceObserved {
				s.nonContext = true
				if s.selectRows && s.diagnostic == nil {
					previous := s.checkMetadata
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						deparseReviewMetadata(t, b.pg, rows)
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				return referenceRunStep(t, p.ctx, db, b.recorder, s)
			}
			run(referenceExec("CREATE TABLE modifiers(a INTEGER CONSTRAINT nn NOT NULL,b INTEGER,CONSTRAINT ck_no CHECK(b>0) NO INHERIT)", 0))
			modifierOID := deparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_class WHERE relname='modifiers'")
			kind := "CHAR"
			run(referenceSelect("SELECT conname,contype FROM pg_catalog.pg_constraint WHERE conrelid=$1 ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", kind}, [][]any{{"ck_no", "c"}, {"nn", "n"}}, modifierOID))
			run(referenceExec("CREATE TABLE defaults(a INT DEFAULT NULL,b INT DEFAULT NULL::integer,c INT DEFAULT COALESCE(NULL::integer,NULL::integer),d INT DEFAULT 7)", 0))
			run(referenceSelect("SELECT adnum FROM pg_catalog.pg_attrdef ORDER BY adnum", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}, {int64(4)}}))
			old := deparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4")
			run(referenceExec("ALTER TABLE defaults ALTER d SET DEFAULT 3", 0))
			fresh := deparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4")
			if fresh == old {
				t.Fatal("replacement retained definition identity")
			}
			run(referenceSelect("SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef WHERE adnum=4", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"3"}}))
			run(referenceExec("ALTER TABLE defaults ALTER d SET DEFAULT NULL", 0))
			run(referenceSelect("SELECT adnum FROM pg_catalog.pg_attrdef", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}}))
			run(referenceExec("ALTER TABLE defaults ALTER d SET DEFAULT 7", 0))
			if deparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4") == fresh {
				t.Fatal("re-add reused identity")
			}
		})
	}
}
