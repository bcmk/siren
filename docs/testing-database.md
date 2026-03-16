# Testing Database Environment

## Access

- Access testing DB environment via `psql` without parameters

## Safety Rules

- NEVER drop testing indexes (`testing_ix_*`)
  without explicit permission.
  This includes "cleaning up" after interrupted runs.
  Testing indexes can take hours to rebuild.
- NEVER stop a running benchmark (`run-all.py`) midway.
  Fix the code first, then let the current run finish
  before starting a new one. Rerunning wastes up to an hour.
- Check table ownership first, then `set role` to the table
  owner before DDL commands in psql

## Query Performance Testing

- When testing query performance, use at least 10 queries
  with varied input data, and up to 100 if they run fast enough.
  Never draw conclusions from fewer queries.
- When testing query performance, always check
  execution plans to verify which index is actually used.
- Include boundary cases in test queries
  (e.g., single char, repeated chars, special chars).
- Present SQL query performance test results as markdown tables.
- When comparing index strategies, create all indexes first,
  then test each by dropping the others inside a transaction
  and rolling back after.
