-- Apply to an existing database with psql -v ON_ERROR_STOP=1.
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction block.
CREATE INDEX CONCURRENTLY posts_active_image_id_author_id
  ON posts(image_id, author_id)
  WHERE deleted_at IS NULL;

CREATE INDEX CONCURRENTLY realmojis_active_image_id_reactor_id
  ON realmojis(image_id, reactor_id) INCLUDE (post_id)
  WHERE replaced_at IS NULL AND deleted_at IS NULL;
