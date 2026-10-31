-- Run with psql -X -v ON_ERROR_STOP=1 -f this_file in autocommit mode,
-- without --single-transaction. IF EXISTS also handles a partially applied build.
-- Remove only the indexes introduced by friendship-session-indexes.sql.
DROP INDEX CONCURRENTLY IF EXISTS login_sessions_active_token_hash_idx;
DROP INDEX CONCURRENTLY IF EXISTS friendships_user_high_id_user_low_id_idx;
