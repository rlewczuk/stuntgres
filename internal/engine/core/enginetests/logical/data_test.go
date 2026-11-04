package logical

import "testing"

// Values keep NULL, empty and binary distinctions; old values follow each
// relation's replica identity at the time of the change.
func TestLogicalStreamDataIdentity(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE vals (id int PRIMARY KEY, a text, b bytea)")
	f.must(s, "CREATE TABLE full_id (id int, v text)")
	f.must(s, "ALTER TABLE full_id REPLICA IDENTITY FULL")
	f.must(s, "CREATE TABLE no_id (id int PRIMARY KEY, v text)")
	f.must(s, "ALTER TABLE no_id REPLICA IDENTITY NOTHING")
	f.must(s, "CREATE TABLE idx_id (id int, code text NOT NULL, v text)")
	f.must(s, "CREATE UNIQUE INDEX idx_id_code ON idx_id (code)")
	f.must(s, "ALTER TABLE idx_id REPLICA IDENTITY USING INDEX idx_id_code")
	f.slot(s, "data")

	f.must(s, "INSERT INTO vals VALUES (1, NULL, NULL), (2, '', '\\x'), (3, 'q''t', '\\x00ff')")
	f.must(s, "INSERT INTO full_id VALUES (1, 'a')")
	f.must(s, "UPDATE full_id SET v = 'b'")
	f.must(s, "DELETE FROM full_id")
	f.must(s, "INSERT INTO no_id VALUES (1, 'a')")
	f.must(s, "UPDATE no_id SET id = 2")
	f.must(s, "DELETE FROM no_id")
	f.must(s, "INSERT INTO idx_id VALUES (1, 'k', 'a')")
	f.must(s, "UPDATE idx_id SET v = 'b'")
	f.must(s, "UPDATE idx_id SET code = 'm'")
	f.must(s, "DELETE FROM idx_id")
	f.expect(f.changes(s, "data"), concat(
		tx("INSERT public.vals (id[integer]:'1' a[text]:null b[bytea]:null)",
			"INSERT public.vals (id[integer]:'2' a[text]:'' b[bytea]:'\\x')",
			"INSERT public.vals (id[integer]:'3' a[text]:'q't' b[bytea]:'\\x00ff')"),
		tx("INSERT public.full_id (id[integer]:'1' v[text]:'a')"),
		tx("UPDATE public.full_id old:(id[integer]:'1' v[text]:'a') (id[integer]:'1' v[text]:'b')"),
		tx("DELETE public.full_id old:(id[integer]:'1' v[text]:'b')"),
		tx("INSERT public.no_id (id[integer]:'1' v[text]:'a')"),
		tx("UPDATE public.no_id (id[integer]:'2' v[text]:'a')"),
		tx("DELETE public.no_id"),
		tx("INSERT public.idx_id (id[integer]:'1' code[text]:'k' v[text]:'a')"),
		tx("UPDATE public.idx_id (id[integer]:'1' code[text]:'k' v[text]:'b')"),
		tx("UPDATE public.idx_id old-key:(id[integer]:null code[text]:'k' v[text]:null) (id[integer]:'1' code[text]:'m' v[text]:'b')"),
		tx("DELETE public.idx_id old-key:(id[integer]:null code[text]:'m' v[text]:null)"),
	)...)
}

// A pending change keeps the relation and column identity of its own time,
// whatever later DDL renames, retypes or drops.
func TestLogicalStreamHistoricIdentity(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE old_name (id int, v int)")
	f.slot(s, "names")
	f.must(s, "INSERT INTO old_name VALUES (1, 2)")
	f.must(s, "BEGIN")
	f.must(s, "INSERT INTO old_name VALUES (3, 4)")
	f.must(s, "ALTER TABLE old_name RENAME TO new_name")
	f.must(s, "ALTER TABLE new_name RENAME COLUMN v TO w")
	f.must(s, "INSERT INTO new_name VALUES (5, 6)")
	f.must(s, "COMMIT")
	f.must(s, "ALTER TABLE new_name ALTER COLUMN w TYPE text")
	f.must(s, "DROP TABLE new_name")
	f.expect(f.changes(s, "names"), concat(
		tx("INSERT public.old_name (id[integer]:'1' v[integer]:'2')"),
		tx("INSERT public.old_name (id[integer]:'3' v[integer]:'4')", "INSERT public.new_name (id[integer]:'5' w[integer]:'6')"),
		tx(), tx(), // The ALTER TYPE rewrite and the DROP are catalog-only.
	)...)
}

// TRUNCATE decodes once with its logged relation set and options; plugins
// without a truncate callback skip it.
func TestLogicalStreamTruncate(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE a (id int PRIMARY KEY GENERATED ALWAYS AS IDENTITY)")
	f.must(s, "CREATE TABLE b (id int REFERENCES a)")
	f.must(s, "CREATE UNLOGGED TABLE u (id int)")
	f.slot(s, "trunc")
	f.must(s, "SELECT pg_create_logical_replication_slot('trunc_plain', 'plain')")
	f.must(s, "INSERT INTO a DEFAULT VALUES")
	f.must(s, "TRUNCATE a, u RESTART IDENTITY CASCADE")
	f.must(s, "BEGIN")
	f.must(s, "INSERT INTO b VALUES (NULL)")
	f.must(s, "TRUNCATE b")
	f.must(s, "INSERT INTO b VALUES (NULL)")
	f.must(s, "COMMIT")
	f.must(s, "TRUNCATE u")
	f.expect(f.changes(s, "trunc"), concat(
		tx("INSERT public.a (id[integer]:'1')"),
		tx("TRUNCATE public.a,public.b cascade restart"),
		tx("INSERT public.b (id[integer]:null)", "TRUNCATE public.b", "INSERT public.b (id[integer]:null)"),
		tx(), // Truncating only an unlogged table changes just its catalog row.
	)...)
	f.expect(f.changes(s, "trunc_plain"), concat(tx("INSERT public.a (id[integer]:'1')"), tx(), tx("INSERT public.b (id[integer]:null)", "INSERT public.b (id[integer]:null)"), tx())...)
}

// Temporary, unlogged and catalog relations are not logically logged.
func TestLogicalStreamExclusions(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE UNLOGGED TABLE u (id int)")
	f.must(s, "CREATE TEMP TABLE tmp (id int)")
	f.must(s, "CREATE TABLE kept (id int)")
	f.must(s, "CREATE MATERIALIZED VIEW mv AS SELECT 1 AS n")
	f.slot(s, "excl")
	f.must(s, "INSERT INTO u VALUES (1)")
	f.must(s, "INSERT INTO tmp VALUES (1)")
	f.must(s, "REFRESH MATERIALIZED VIEW mv")
	f.must(s, "INSERT INTO kept VALUES (1)")
	f.must(s, "COMMENT ON TABLE kept IS 'catalog row only'")
	f.expect(f.changes(s, "excl"), concat(tx(), tx("INSERT public.kept (id[integer]:'1')"), tx())...)
}
