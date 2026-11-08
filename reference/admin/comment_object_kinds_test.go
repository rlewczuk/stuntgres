package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceAdminCommentObjectKinds covers the round-2 repro and the whole
// accepted object-kind matrix for COMMENT: the exact pg_description address and
// text of every kind, the inquiry functions (including the legacy one-argument
// form), removal and replacement, transactional visibility, DROP semantics and
// the information_schema visibility that PostgreSQL 18 actually exposes.
func TestReferenceAdminCommentObjectKinds(t *testing.T) {
	cokScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, s *cokSession) {
		// Exact round-2 repro, in order.
		for _, q := range []string{
			"CREATE TYPE grin_kind AS ENUM ('a')",
			"COMMENT ON TYPE grin_kind IS 'kind'",
			"CREATE TABLE grin_kind_t (id int)",
			"CREATE INDEX grin_kind_idx ON grin_kind_t (id)",
			"COMMENT ON INDEX grin_kind_idx IS 'idx'",
			"CREATE DOMAIN grin_dom AS jsonb",
			"COMMENT ON DOMAIN grin_dom IS 'dom'",
			"CREATE FUNCTION grin_kind_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER grin_kind_tr AFTER INSERT ON grin_kind_t FOR EACH ROW EXECUTE PROCEDURE grin_kind_fn()",
		} {
			s.exec(q)
		}
		// A utility statement reached through QueryContext publishes no columns
		// and no rows on either route.
		s.zeroRows("COMMENT ON TRIGGER grin_kind_tr ON grin_kind_t IS 'tr'")
		for _, q := range []string{
			"COMMENT ON FUNCTION grin_kind_fn() IS 'fn'",
			"COMMENT ON COLUMN grin_kind_t.id IS 'col'",
		} {
			s.exec(q)
		}

		kindOID := s.oid("SELECT 'grin_kind'::regtype::oid")
		indexOID := s.oid("SELECT 'grin_kind_idx'::regclass::oid")
		domOID := s.oid("SELECT 'grin_dom'::regtype::oid")
		trOID := s.oid("SELECT oid FROM pg_catalog.pg_trigger WHERE tgrelid='grin_kind_t'::regclass AND tgname='grin_kind_tr'")
		fnOID := s.oid("SELECT 'grin_kind_fn()'::regprocedure::oid")

		s.description(cokDescription(kindOID, cokTypeClass, "kind"))
		s.description(cokDescription(indexOID, cokRelationClass, "idx"))
		s.description(cokDescription(domOID, cokTypeClass, "dom"))
		s.description(cokDescription(trOID, cokTriggerClass, "tr"))
		s.description(cokDescription(fnOID, cokProcedureClass, "fn"))
		columnOwner := s.oid("SELECT 'grin_kind_t'::regclass::oid")
		s.description(fixture.Select(
			"SELECT objoid,classoid,objsubid,description FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259 AND objsubid=1",
			cokDescriptionColumns, cokDescriptionTypes,
			[][]any{{columnOwner, cokRelationClass, int64(1), "col"}}, columnOwner))
		s.run(cokText("SELECT obj_description('grin_kind'::regtype,'pg_type') AS text", "kind"))
		s.run(cokText("SELECT obj_description('grin_kind_idx'::regclass,'pg_class') AS text", "idx"))
		// An index comment is a pg_class comment, never a pg_index comment.
		s.run(cokText("SELECT obj_description('grin_kind_idx'::regclass,'pg_index') AS text", nil))
		s.run(cokText("SELECT obj_description('grin_dom'::regtype,'pg_type') AS text", "dom"))
		s.run(cokText("SELECT obj_description('grin_kind_fn()'::regprocedure,'pg_proc') AS text", "fn"))
		s.run(cokText("SELECT col_description('grin_kind_t'::regclass,1) AS text", "col"))
		// Legacy one-argument obj_description for a type, index, trigger and
		// procedure selects the same row.
		s.run(cokText("SELECT obj_description('grin_kind'::regtype::oid) AS text", "kind"))
		s.run(cokText("SELECT obj_description('grin_kind_idx'::regclass::oid) AS text", "idx"))
		s.run(cokText(fmt.Sprintf("SELECT obj_description(%d::oid) AS text", trOID), "tr"))
		s.run(cokText("SELECT obj_description('grin_kind_fn()'::regprocedure::oid) AS text", "fn"))

		// CREATE OR REPLACE TRIGGER retains the live trigger oid and its comment.
		s.exec("CREATE OR REPLACE TRIGGER grin_kind_tr AFTER INSERT ON grin_kind_t FOR EACH ROW EXECUTE FUNCTION grin_kind_fn()")
		s.description(cokDescription(trOID, cokTriggerClass, "tr"))

		// TYPE flavors: standalone composite, range, a plain table's row type and
		// the relation comment of the same table are independent addresses.
		for _, q := range []string{
			"CREATE TYPE cok_comp AS (a int)",
			"COMMENT ON TYPE cok_comp IS 'composite'",
			"CREATE TYPE cok_range AS RANGE (subtype = integer)",
			"COMMENT ON TYPE cok_range IS 'range'",
			"CREATE TABLE cok_tbl(id int)",
			"COMMENT ON TYPE cok_tbl IS 'rowtype'",
			"COMMENT ON TABLE cok_tbl IS 'table comment'",
		} {
			s.exec(q)
		}
		s.description(cokDescription(s.oid("SELECT 'cok_comp'::regtype::oid"), cokTypeClass, "composite"))
		s.description(cokDescription(s.oid("SELECT 'cok_range'::regtype::oid"), cokTypeClass, "range"))
		s.description(cokDescription(s.oid("SELECT 'cok_tbl'::regtype::oid"), cokTypeClass, "rowtype"))
		s.description(cokDescription(s.oid("SELECT 'cok_tbl'::regclass::oid"), cokRelationClass, "table comment"))
		// A domain comment survives unrelated domain use.
		s.exec("CREATE TABLE cok_dom_use(v grin_dom)")
		s.description(cokDescription(domOID, cokTypeClass, "dom"))
		for _, q := range []string{
			"COMMENT ON TYPE grin_kind_t IS 'repro rowtype'",
			"COMMENT ON TABLE grin_kind_t IS 'repro table'",
		} {
			s.exec(q)
		}
		s.description(cokDescription(s.oid("SELECT 'grin_kind_t'::regtype::oid"), cokTypeClass, "repro rowtype"))
		s.description(cokDescription(s.oid("SELECT 'grin_kind_t'::regclass::oid"), cokRelationClass, "repro table"))

		// Built-in array and a typmod spelling target the base type oid; the
		// shared oracle keeps its builtin descriptions, so this runs in a
		// rolled-back transaction.
		s.execDirect("BEGIN")
		s.exec("COMMENT ON TYPE int[] IS 'arr'")
		s.exec("COMMENT ON TYPE numeric(10,2) IS 'nummod'")
		s.description(cokDescription(s.oid("SELECT 'int[]'::regtype::oid"), cokTypeClass, "arr"))
		s.description(cokDescription(s.oid("SELECT 'numeric(10,2)'::regtype::oid"), cokTypeClass, "nummod"))
		s.description(cokDescription(s.oid("SELECT 'numeric'::regtype::oid"), cokTypeClass, "nummod"))
		s.run(cokText("SELECT obj_description('int[]'::regtype,'pg_type') AS text", "arr"))
		s.execDirect("ROLLBACK")

		// Constraint-created indexes are ordinary pg_class comment targets.
		s.exec("CREATE TABLE cok_pk(a INT, CONSTRAINT cok_pk_pkey PRIMARY KEY(a))")
		s.exec("COMMENT ON INDEX cok_pk_pkey IS 'pkey'")
		s.description(cokDescription(s.oid("SELECT 'cok_pk_pkey'::regclass::oid"), cokRelationClass, "pkey"))
		s.run(cokText("SELECT obj_description('cok_pk_pkey'::regclass,'pg_class') AS text", "pkey"))

		// Internal foreign-key triggers are commentable through their own oid.
		s.exec("CREATE TABLE cok_parent(id INT PRIMARY KEY)")
		s.exec("CREATE TABLE cok_child(id INT REFERENCES cok_parent(id))")
		fkOID := s.oid("SELECT oid FROM pg_catalog.pg_trigger WHERE tgrelid='cok_parent'::regclass AND tgisinternal AND tgname LIKE 'RI_ConstraintTrigger_a_%' ORDER BY oid LIMIT 1")
		s.exec(fmt.Sprintf("COMMENT ON TRIGGER \"RI_ConstraintTrigger_a_%d\" ON cok_parent IS 'fk'", fkOID))
		s.description(cokDescription(fkOID, cokTriggerClass, "fk"))
		s.run(cokText(fmt.Sprintf("SELECT obj_description(%d::oid,'pg_trigger') AS text", fkOID), "fk"))

		// A partition clone and the parent's own trigger hold independent rows.
		for _, q := range []string{
			"CREATE TABLE cok_part(v INT) PARTITION BY RANGE(v)",
			"CREATE TABLE cok_part_child PARTITION OF cok_part FOR VALUES FROM (0) TO (10)",
			"CREATE FUNCTION cok_part_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER cok_part_tr BEFORE INSERT ON cok_part FOR EACH ROW EXECUTE FUNCTION cok_part_fn()",
			"COMMENT ON TRIGGER cok_part_tr ON cok_part IS 'parent trigger'",
			"COMMENT ON TRIGGER cok_part_tr ON cok_part_child IS 'clone trigger'",
		} {
			s.exec(q)
		}
		parentTrOID := s.oid("SELECT oid FROM pg_catalog.pg_trigger WHERE tgrelid='cok_part'::regclass AND tgname='cok_part_tr'")
		cloneTrOID := s.oid("SELECT oid FROM pg_catalog.pg_trigger WHERE tgrelid='cok_part_child'::regclass AND tgname='cok_part_tr'")
		if parentTrOID == cloneTrOID {
			t.Fatal("partition clone shared the parent trigger identity")
		}
		s.description(cokDescription(parentTrOID, cokTriggerClass, "parent trigger"))
		s.description(cokDescription(cloneTrOID, cokTriggerClass, "clone trigger"))

		// Signatures with arguments, bare-name lookup, procedures and ROUTINE
		// over both routine kinds.
		for _, q := range []string{
			"CREATE FUNCTION cok_fn(a INT) RETURNS INT LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION cok_bare() RETURNS INT LANGUAGE SQL AS 'SELECT 1'",
			"CREATE PROCEDURE cok_proc(IN x INT) LANGUAGE SQL AS 'SELECT 1'",
			"COMMENT ON FUNCTION cok_fn(int) IS 'fnargs'",
			"COMMENT ON FUNCTION cok_bare IS 'bare'",
			"COMMENT ON PROCEDURE cok_proc(int) IS 'proc'",
			"COMMENT ON ROUTINE cok_fn(int) IS 'routinefn'",
			"COMMENT ON ROUTINE cok_proc(int) IS 'routineproc'",
		} {
			s.exec(q)
		}
		fnArgsOID := s.oid("SELECT 'cok_fn(integer)'::regprocedure::oid")
		bareOID := s.oid("SELECT 'cok_bare()'::regprocedure::oid")
		procOID := s.oid("SELECT 'cok_proc(integer)'::regprocedure::oid")
		s.description(cokDescription(fnArgsOID, cokProcedureClass, "routinefn"))
		s.description(cokDescription(bareOID, cokProcedureClass, "bare"))
		s.description(cokDescription(procOID, cokProcedureClass, "routineproc"))
		s.run(cokText("SELECT obj_description('cok_fn(integer)'::regprocedure,'pg_proc') AS text", "routinefn"))
		s.run(cokText("SELECT obj_description('cok_bare()'::regprocedure,'pg_proc') AS text", "bare"))
		s.run(cokText("SELECT obj_description('cok_proc(integer)'::regprocedure,'pg_proc') AS text", "routineproc"))

		// Replacement and removal: NULL and '' delete, ' ' persists, and removing
		// an absent text succeeds without changing anything.
		s.exec("COMMENT ON TYPE grin_kind IS 'replacement'")
		s.description(cokDescription(kindOID, cokTypeClass, "replacement"))
		s.exec("COMMENT ON TYPE grin_kind IS NULL")
		s.description(cokNoDescription(kindOID, cokTypeClass))
		s.run(cokText("SELECT obj_description('grin_kind'::regtype,'pg_type') AS text", nil))
		s.exec("COMMENT ON TYPE grin_kind IS ''")
		s.description(cokNoDescription(kindOID, cokTypeClass))
		s.exec("COMMENT ON TYPE grin_kind IS ' '")
		s.description(cokDescription(kindOID, cokTypeClass, " "))
		s.exec("COMMENT ON TYPE grin_kind IS NULL")
		s.description(cokNoDescription(kindOID, cokTypeClass))
		s.exec("COMMENT ON TYPE grin_kind IS NULL")
		s.description(cokNoDescription(kindOID, cokTypeClass))
		s.exec("COMMENT ON INDEX grin_kind_idx IS 'idx replacement'")
		s.description(cokDescription(indexOID, cokRelationClass, "idx replacement"))
		s.exec("COMMENT ON INDEX grin_kind_idx IS ''")
		s.description(cokNoDescription(indexOID, cokRelationClass))
		s.exec("COMMENT ON INDEX grin_kind_idx IS NULL")
		s.description(cokNoDescription(indexOID, cokRelationClass))
		s.exec("COMMENT ON TRIGGER grin_kind_tr ON grin_kind_t IS 'tr replacement'")
		s.description(cokDescription(trOID, cokTriggerClass, "tr replacement"))
		s.exec("COMMENT ON TRIGGER grin_kind_tr ON grin_kind_t IS NULL")
		s.description(cokNoDescription(trOID, cokTriggerClass))
		s.exec("COMMENT ON FUNCTION cok_fn(int) IS 'fn replacement'")
		s.description(cokDescription(fnArgsOID, cokProcedureClass, "fn replacement"))
		s.exec("COMMENT ON FUNCTION cok_fn(int) IS ''")
		s.description(cokNoDescription(fnArgsOID, cokProcedureClass))

		// Explicit transactions: COMMENT is transactional on both kinds.
		s.exec("COMMENT ON TYPE grin_kind IS 'committed text'")
		s.execDirect("BEGIN")
		s.exec("COMMENT ON TYPE grin_kind IS 'rolled back'")
		s.description(cokDescription(kindOID, cokTypeClass, "rolled back"))
		s.execDirect("ROLLBACK")
		s.description(cokDescription(kindOID, cokTypeClass, "committed text"))
		s.execDirect("BEGIN")
		s.exec("COMMENT ON TRIGGER grin_kind_tr ON grin_kind_t IS 'committed trigger'")
		s.execDirect("COMMIT")
		s.description(cokDescription(trOID, cokTriggerClass, "committed trigger"))

		// DROP semantics: a dropped object's row disappears, and a re-created
		// trigger gets a new oid without inheriting the old text.
		s.exec("CREATE TABLE cok_drop_t(id int)")
		s.exec("CREATE INDEX cok_drop_idx ON cok_drop_t(id)")
		dropIdxOID := s.oid("SELECT 'cok_drop_idx'::regclass::oid")
		s.exec("COMMENT ON INDEX cok_drop_idx IS 'dropped index'")
		s.description(cokDescription(dropIdxOID, cokRelationClass, "dropped index"))
		s.exec("DROP INDEX cok_drop_idx")
		s.description(cokNoDescription(dropIdxOID, cokRelationClass))
		s.exec("CREATE FUNCTION cok_drop_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$")
		s.exec("CREATE TRIGGER cok_drop_tr BEFORE INSERT ON cok_drop_t FOR EACH ROW EXECUTE FUNCTION cok_drop_fn()")
		dropTrOID := s.oid("SELECT oid FROM pg_catalog.pg_trigger WHERE tgrelid='cok_drop_t'::regclass AND tgname='cok_drop_tr'")
		s.exec("COMMENT ON TRIGGER cok_drop_tr ON cok_drop_t IS 'dropped trigger'")
		s.description(cokDescription(dropTrOID, cokTriggerClass, "dropped trigger"))
		s.exec("DROP TRIGGER cok_drop_tr ON cok_drop_t")
		s.description(cokNoDescription(dropTrOID, cokTriggerClass))
		s.exec("CREATE TRIGGER cok_drop_tr BEFORE INSERT ON cok_drop_t FOR EACH ROW EXECUTE FUNCTION cok_drop_fn()")
		recreatedTrOID := s.oid("SELECT oid FROM pg_catalog.pg_trigger WHERE tgrelid='cok_drop_t'::regclass AND tgname='cok_drop_tr'")
		if recreatedTrOID == dropTrOID {
			t.Fatal("re-created trigger reused its oid")
		}
		s.description(cokNoDescription(recreatedTrOID, cokTriggerClass))
		s.run(cokText(fmt.Sprintf("SELECT obj_description(%d::oid,'pg_trigger') AS text", recreatedTrOID), nil))
		s.exec("CREATE TABLE cok_drop_table(id int)")
		dropTableOID := s.oid("SELECT 'cok_drop_table'::regclass::oid")
		dropRowTypeOID := s.oid("SELECT 'cok_drop_table'::regtype::oid")
		s.exec("COMMENT ON TABLE cok_drop_table IS 'dropped table'")
		s.exec("COMMENT ON TYPE cok_drop_table IS 'dropped row type'")
		s.description(cokDescription(dropTableOID, cokRelationClass, "dropped table"))
		s.description(cokDescription(dropRowTypeOID, cokTypeClass, "dropped row type"))
		s.exec("DROP TABLE cok_drop_table")
		s.description(cokNoDescription(dropTableOID, cokRelationClass))
		s.description(cokNoDescription(dropRowTypeOID, cokTypeClass))
		s.exec("CREATE TYPE cok_drop_type AS ENUM ('a')")
		dropTypeOID := s.oid("SELECT 'cok_drop_type'::regtype::oid")
		s.exec("COMMENT ON TYPE cok_drop_type IS 'dropped type'")
		s.description(cokDescription(dropTypeOID, cokTypeClass, "dropped type"))
		s.exec("DROP TYPE cok_drop_type")
		s.description(cokNoDescription(dropTypeOID, cokTypeClass))

		// PostgreSQL 18 exposes no object-comment column in information_schema;
		// the record's wording is satisfied by the commented objects' visibility.
		s.run(fixture.Select("SELECT domain_name::text FROM information_schema.domains WHERE domain_schema='public' AND domain_name='grin_dom'", []string{"domain_name"}, []string{"TEXT"}, [][]any{{"grin_dom"}}))
		s.run(fixture.Select("SELECT routine_name::text,routine_type::text FROM information_schema.routines WHERE routine_schema='public' AND routine_name IN ('grin_kind_fn','cok_proc') ORDER BY routine_name", []string{"routine_name", "routine_type"}, []string{"TEXT", "TEXT"}, [][]any{{"cok_proc", "PROCEDURE"}, {"grin_kind_fn", "FUNCTION"}}))
		s.run(fixture.Select("SELECT trigger_name::text,event_object_table::text FROM information_schema.triggers WHERE trigger_schema='public' AND trigger_name='grin_kind_tr'", []string{"trigger_name", "event_object_table"}, []string{"TEXT", "TEXT"}, [][]any{{"grin_kind_tr", "grin_kind_t"}}))
		s.run(fixture.Select("SELECT column_name::text FROM information_schema.columns WHERE table_schema='public' AND table_name='grin_kind_t' ORDER BY ordinal_position", []string{"column_name"}, []string{"TEXT"}, [][]any{{"id"}}))
		s.run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_shdescription WHERE classoid IN (1247,1255,1259,2620)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}