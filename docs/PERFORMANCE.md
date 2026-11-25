# Performance

No benchmarks have been run yet — nothing in this file is a measured
number, and none should be trusted until PROGRESS.md says they were
actually captured.

## What's already in place

- Schema registry is an in-memory cache (`internal/schema.Registry`),
  warmed once at startup and updated in place on writes — content
  requests never parse or query the schema from Postgres on the hot path.
- pgxpool connection pooling, parameterized/prepared-statement-eligible
  queries throughout.
- Delivery reads don't write to the database (no implicit "touch" on
  read).

## What's missing before any benchmark number means anything

- No Redis caching layer yet (planned to be optional; core CMS must stay
  functional without it).
- No k6 suite, no seed datasets at the 10k/100k/1M scale called for in
  the original spec.
- No batch relation loading (relations aren't implemented at all yet, so
  this isn't tested).
- No Strapi comparison environment.

Do not state or imply a performance comparison to anything until this
file has been updated with an actual benchmark run and raw results.
