package engine

func serialPublicationQuery(alter bool) string {
	if alter {
		return `ALTER TABLE consumer ALTER id SET DEFAULT nextval('q')`
	}
	return `CREATE TABLE consumer(id INT DEFAULT nextval('q'))`
}
