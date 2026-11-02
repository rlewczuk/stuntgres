package session

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestMaterializedStorageClassification(t *testing.T) {
	for _, tc := range []struct{ sql, tag string }{
		{"ALTER TABLE t SET (fillfactor=70)", "ALTER TABLE"},
		{"ALTER MATERIALIZED VIEW mv SET (fillfactor=70)", "ALTER MATERIALIZED VIEW"},
		{"ALTER TABLE ALL IN TABLESPACE pg_default SET TABLESPACE pg_default NOWAIT", "ALTER TABLE"},
		{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_default NOWAIT", "ALTER MATERIALIZED VIEW"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			statements, err := parser.Parse(tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			s := statements[0]
			base, counted, insert, ok := ClassifyCommandTag(s, TxIdle)
			if !ok || base != tc.tag || counted || insert {
				t.Fatal(base, counted, insert, ok)
			}
			if tag := ModifyingTag(s); tag != tc.tag {
				t.Fatalf("modifying tag %q want %q", tag, tc.tag)
			}
			if tag, eligible := EventCommand(s); !eligible || tag != tc.tag {
				t.Fatal("event classification", tag, eligible)
			}
		})
	}
	for _, materialized := range []bool{false, true} {
		want := "ALTER TABLE"
		if materialized {
			want = "ALTER MATERIALIZED VIEW"
		}
		for _, s := range []parser.Statement{&parser.AlterTable{Materialized: materialized}, &parser.AlterTableAllInTablespace{Materialized: materialized}} {
			base, counted, insert, ok := ClassifyCommandTag(s, TxIdle)
			if !ok || base != want || counted || insert || ModifyingTag(s) != want {
				t.Fatal("typed discriminator classification", s, base, counted, insert, ok)
			}
			if tag, eligible := EventCommand(s); !eligible || tag != want {
				t.Fatal("typed event discriminator", tag, eligible)
			}
			if err := ReadOnlyRefusal(s, true); err == nil || sqlText(err) != "25006 cannot execute "+want+" in a read-only transaction" {
				t.Fatal("typed read-only discriminator", err)
			}
		}
	}
}
