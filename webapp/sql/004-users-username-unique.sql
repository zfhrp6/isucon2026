-- Run each statement separately in autocommit mode; CREATE INDEX CONCURRENTLY
-- cannot run in a transaction. Duplicate usernames make this step fail. Resolve
-- them explicitly before retrying; do not delete or merge rows automatically.
-- Plain CREATE intentionally fails if this name exists, so an invalid or
-- differently-defined index cannot be mistaken for the required rule.
CREATE UNIQUE INDEX CONCURRENTLY users_username_key ON users (username);

ALTER TABLE users
  ADD CONSTRAINT users_username_key UNIQUE USING INDEX users_username_key;
