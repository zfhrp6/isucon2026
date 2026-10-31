-- Apply to an existing database with psql -X -v ON_ERROR_STOP=1 -f this_file.
-- Run in autocommit mode, without --single-transaction: concurrent index builds
-- avoid blocking application writes but cannot run inside a transaction.
-- Plain CREATE fails on an existing name rather than hiding an invalid index.
-- If a build fails, inspect both indexes and remove any invalid index before
-- retrying. The first index can remain applied if the second build fails.
CREATE INDEX CONCURRENTLY friendships_user_high_id_user_low_id_idx
  ON friendships(user_high_id, user_low_id);

CREATE INDEX CONCURRENTLY login_sessions_active_token_hash_idx
  ON login_sessions(token_hash)
  WHERE active = true;

-- Verify definitions and indisvalid in pg_index after applying. Benchmark
-- initialization uses reset.sql and preserves these indexes; measure its cost.
-- Roll back with friendship-session-indexes-rollback.sql. Redeploying the
-- application alone does not remove indexes from an existing database.
