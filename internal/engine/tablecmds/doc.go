// Package tablecmds holds the table, sequence and VACUUM statement executors
// (ALTER TABLE, CREATE TABLE, DROP TABLE, RENAME and SET SCHEMA of relations;
// CREATE, ALTER, DROP and COMMENT ON SEQUENCE; VACUUM), the TableHost and
// AlterHost family hosts and the table and identity Restore checks; the root
// composition dispatches to them.
package tablecmds
