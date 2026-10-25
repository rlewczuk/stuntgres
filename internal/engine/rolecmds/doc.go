// Package rolecmds holds the role, privilege and ownership statement
// executors (CREATE, ALTER and DROP ROLE, role membership GRANT and REVOKE,
// SET ROLE and SESSION AUTHORIZATION, object and database GRANT and REVOKE,
// ALTER DEFAULT PRIVILEGES, ALTER ... OWNER TO, REASSIGN and DROP OWNED, and
// CREATE DATABASE) and their family host RoleHost; the root composition
// dispatches to them.
package rolecmds
