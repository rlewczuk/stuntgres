package logical

import "testing"

// Identical final contents must not hide the history that produced them.
func TestLogicalStreamHistory(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int PRIMARY KEY, v text)")
	f.must(s, "CREATE TABLE u (id int PRIMARY KEY)")
	f.slot(s, "hist")
	f.must(s, "BEGIN")
	f.must(s, "INSERT INTO t VALUES (1, 'a')")
	f.must(s, "DELETE FROM t WHERE id = 1")
	f.must(s, "COMMIT")
	f.must(s, "INSERT INTO t VALUES (2, 'x')")
	f.must(s, "BEGIN")
	f.must(s, "UPDATE t SET v = 'y' WHERE id = 2")
	f.must(s, "UPDATE t SET v = 'x' WHERE id = 2")
	f.must(s, "INSERT INTO u VALUES (7)")
	f.must(s, "UPDATE t SET id = 3 WHERE id = 2")
	f.must(s, "COMMIT")
	f.expect(f.changes(s, "hist"), concat(
		tx("INSERT public.t (id[integer]:'1' v[text]:'a')", "DELETE public.t old-key:(id[integer]:'1' v[text]:null)"),
		tx("INSERT public.t (id[integer]:'2' v[text]:'x')"),
		tx("UPDATE public.t (id[integer]:'2' v[text]:'y')",
			"UPDATE public.t (id[integer]:'2' v[text]:'x')",
			"INSERT public.u (id[integer]:'7')",
			"UPDATE public.t old-key:(id[integer]:'2' v[text]:null) (id[integer]:'3' v[text]:'x')"),
	)...)
}

// Every supported write route reaches the stream with its actual effects.
func TestLogicalStreamRoutes(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE r (id int PRIMARY KEY, v text)")
	f.must(s, "CREATE TABLE audit (note text)")
	f.must(s, "CREATE TABLE child (id int REFERENCES r ON DELETE CASCADE)")
	f.must(s, "CREATE FUNCTION audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES (TG_OP || ' ' || NEW.id); RETURN NEW; END$$")
	f.must(s, "CREATE TRIGGER r_audit AFTER INSERT ON r FOR EACH ROW EXECUTE FUNCTION audit()")
	f.must(s, "CREATE TABLE p (id int, k int) PARTITION BY RANGE (k)")
	f.must(s, "CREATE TABLE p1 PARTITION OF p FOR VALUES FROM (0) TO (10)")
	f.must(s, "CREATE TABLE p2 PARTITION OF p FOR VALUES FROM (10) TO (20)")
	f.must(s, "CREATE TABLE ruled (id int)")
	f.must(s, "CREATE RULE ruled_copy AS ON INSERT TO ruled DO ALSO INSERT INTO audit VALUES ('rule ' || NEW.id)")
	f.slot(s, "routes")

	f.must(s, "PREPARE ins(int, text) AS INSERT INTO r VALUES ($1, $2)")
	f.must(s, "EXECUTE ins(1, 'p')")
	if err := copyIn(s, "COPY r FROM STDIN", "2\tc\n"); err != nil {
		t.Fatal(err)
	}
	f.must(s, "INSERT INTO r VALUES (1, 'conflict') ON CONFLICT (id) DO UPDATE SET v = excluded.v")
	f.must(s, "INSERT INTO r VALUES (2, 'skip') ON CONFLICT DO NOTHING")
	f.must(s, "MERGE INTO r USING (VALUES (2, 'm'), (5, 'n')) AS src(id, v) ON r.id = src.id WHEN MATCHED THEN UPDATE SET v = src.v WHEN NOT MATCHED THEN INSERT VALUES (src.id, src.v)")
	f.must(s, "INSERT INTO child VALUES (5)")
	f.must(s, "DELETE FROM r WHERE id = 5")
	f.must(s, "INSERT INTO p VALUES (1, 5)")
	f.must(s, "UPDATE p SET k = 15")
	f.must(s, "INSERT INTO ruled VALUES (9)")
	f.must(s, "CREATE TABLE ctas AS SELECT 4 AS n")
	f.must(s, "BEGIN")
	f.must(s, "CREATE TABLE ctas2 AS SELECT 5 AS n")
	f.must(s, "COMMIT")
	f.expect(f.changes(s, "routes"), concat(
		// Prepared execution; the AFTER trigger's insert follows the row.
		tx("INSERT public.r (id[integer]:'1' v[text]:'p')", "INSERT public.audit (note[text]:'INSERT 1')"),
		tx("INSERT public.r (id[integer]:'2' v[text]:'c')", "INSERT public.audit (note[text]:'INSERT 2')"),
		// ON CONFLICT DO UPDATE decodes as the update; DO NOTHING writes nothing.
		tx("UPDATE public.r (id[integer]:'1' v[text]:'conflict')"),
		tx("UPDATE public.r (id[integer]:'2' v[text]:'m')", "INSERT public.r (id[integer]:'5' v[text]:'n')", "INSERT public.audit (note[text]:'INSERT 5')"),
		tx("INSERT public.child (id[integer]:'5')"),
		// The cascade follows its parent; child has no replica identity key.
		tx("DELETE public.r old-key:(id[integer]:'5' v[text]:null)", "DELETE public.child"),
		// Partitioned writes decode on leaves; a moving update is DELETE+INSERT.
		tx("INSERT public.p1 (id[integer]:'1' k[integer]:'5')"),
		tx("DELETE public.p1", "INSERT public.p2 (id[integer]:'1' k[integer]:'15')"),
		// A DO ALSO rule's action runs after the original INSERT.
		tx("INSERT public.ruled (id[integer]:'9')", "INSERT public.audit (note[text]:'rule 9')"),
		tx("INSERT public.ctas (n[integer]:'4')"),
		tx("INSERT public.ctas2 (n[integer]:'5')"),
	)...)
}
