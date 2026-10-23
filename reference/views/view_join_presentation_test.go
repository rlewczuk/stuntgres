package reference_test

import "stuntgres/reference/fixture"

// Independent canonical definitions from the pinned PostgreSQL fixture.
var referenceJoinedDefinitions = map[string]string{
	"leading.joined":             " SELECT l.aid AS id,\n    l.x,\n    r.y\n   FROM (renamed_a l\n     JOIN renamed_b r ON (((l.aid = r.id) AND (l.d = '2000-02-01'::date))));",
	"leading.merged":             " SELECT renamed_b.id,\n    renamed_a.x,\n    renamed_b.y\n   FROM (renamed_a renamed_a(unused, id, x, d)\n     JOIN renamed_b USING (id));",
	"leading.natural_view":       " SELECT renamed_b.id,\n    renamed_a.x,\n    renamed_b.y\n   FROM (renamed_a renamed_a(unused, id, x, d)\n     JOIN renamed_b USING (id));",
	"leading.repeated":           " SELECT l.aid AS left_id,\n    r.aid AS right_id\n   FROM (renamed_a l\n     JOIN renamed_a r ON ((l.aid < r.aid)));",
	"leading.chain":              " SELECT l.aid AS id,\n    l.x,\n    r.y\n   FROM ((renamed_a l\n     JOIN renamed_b r ON (((l.aid = r.id) AND (l.d = '2000-02-01'::date))))\n     JOIN renamed_b z ON (((l.aid = z.id) AND (z.y = 'u'::text))));",
	"leading.alias_columns":      " SELECT q.key AS id,\n    q.label AS x,\n    q.value AS y\n   FROM (renamed_a l(discard, key, label, day)\n     JOIN renamed_b r(other, value) ON ((l.key = r.other))) q;",
	"leading.converted":          " SELECT id\n   FROM (renamed_a renamed_a(unused, id, x, d)\n     JOIN c USING (id));",
	"leading.mixed":              " SELECT l.unused AS discard,\n    l.aid AS left_id,\n    l.x AS label,\n    l.d AS day,\n    r.aid AS right_id\n   FROM (renamed_a l\n     JOIN renamed_a r ON ((l.aid < r.aid)));",
	"leading.nested":             " SELECT id,\n    x,\n    y\n   FROM joined;",
	"compact.joined":             " SELECT l.aid AS id,\n    l.x,\n    r.y\n   FROM (renamed_a l\n     JOIN renamed_b r ON (((l.aid = r.id) AND (l.d = '2000-02-01'::date))));",
	"compact.merged":             " SELECT renamed_b.id,\n    renamed_a.x,\n    renamed_b.y\n   FROM (renamed_a renamed_a(id, x, d)\n     JOIN renamed_b USING (id));",
	"compact.natural_view":       " SELECT renamed_b.id,\n    renamed_a.x,\n    renamed_b.y\n   FROM (renamed_a renamed_a(id, x, d)\n     JOIN renamed_b USING (id));",
	"compact.repeated":           " SELECT l.aid AS left_id,\n    l.x AS label,\n    l.d AS day,\n    r.aid AS right_id\n   FROM (renamed_a l\n     JOIN renamed_a r ON ((l.aid < r.aid)));",
	"compact.chain":              " SELECT l.aid AS id,\n    l.x,\n    r.y\n   FROM ((renamed_a l\n     JOIN renamed_b r ON (((l.aid = r.id) AND (l.d = '2000-02-01'::date))))\n     JOIN renamed_b z ON (((l.aid = z.id) AND (z.y = 'u'::text))));",
	"compact.alias_columns":      " SELECT q.key AS id,\n    q.label AS x,\n    q.value AS y\n   FROM (renamed_a l(key, label, day)\n     JOIN renamed_b r(other, value) ON ((l.key = r.other))) q;",
	"compact.converted":          " SELECT id\n   FROM (renamed_a renamed_a(id, x, d)\n     JOIN c USING (id));",
	"namespace.self_sorted":      " SELECT l.id\n   FROM (a l(id)\n     JOIN a r(id) USING (id))\n  ORDER BY l.id;",
	"namespace.converted_sorted": " SELECT id\n   FROM (a a(id)\n     JOIN c USING (id))\n  ORDER BY id;",
	"namespace.alias_sorted":     " SELECT q.key AS id\n   FROM (a l(key)\n     JOIN a r(key) USING (key)) q\n  ORDER BY q.key;",
}

func referenceJoinedDefinitionSteps(mode, name string) []fixture.Step {
	want := referenceJoinedDefinitions[mode+"."+name]
	steps := []fixture.Step{
		fixture.Select("SELECT pg_get_viewdef($1::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{want}}, name),
		fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='public' AND viewname=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{want}}, name),
	}
	return append(steps, fixture.Prepared(steps[0]), fixture.Prepared(steps[1]))
}
