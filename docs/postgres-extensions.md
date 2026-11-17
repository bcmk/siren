# PostgreSQL Extensions

A migration, which runs as the bot's role, needs the hosting's help in two cases:
to drop a PostgreSQL extension owned by another role, as `fuzzystrmatch` may be,
and to create one that is not trusted, such as `pg_stat_statements`.
PostgreSQL lets a role with the `create` privilege on the database create a trusted extension;
that role then owns it and may drop it.

For example, Scaleway's managed PostgreSQL loads `pgextwlist`.
For an extension that `extwlist.extensions` lists,
any role may run `create extension` and `drop extension`:
`pgextwlist` executes the statement as a superuser.
So a superuser owns every extension there, yet the bot's role creates and drops them.
