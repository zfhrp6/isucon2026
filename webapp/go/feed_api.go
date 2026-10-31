package isureal

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getFeedRequest struct {
	Feed           string     `param:"feed"`
	BeforeScore    *int64     `query:"beforeScore"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type feedItemResponse struct {
	createPostResponse
	Score *int64 `json:"score,omitempty"`
}

type getFeedResponse struct {
	Items   []feedItemResponse `json:"items"`
	HasMore bool               `json:"hasMore"`
}

func getFeedHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getFeedRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		switch input.Feed {
		case "friends", "friends-of-friends", "public", "following":
		default:
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		if (input.BeforePostedAt == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ranked := input.Feed == "friends-of-friends"
		if ranked && (input.BeforeScore == nil) != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		viewer, hasPostedToday, err := loadFeedViewer(ctx, h.DB, GetSession(c).UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []feedItemResponse{}})
		}
		if err != nil {
			return err
		}
		if !hasPostedToday {
			return c.JSON(http.StatusOK, getFeedResponse{Items: []feedItemResponse{}})
		}
		posts, err := selectFeedPosts(ctx, h.DB, viewer.ID, input)
		if err != nil {
			return err
		}
		hasMore := len(posts) > 50
		if hasMore {
			posts = posts[:50]
		}
		items := make([]feedItemResponse, 0, len(posts))
		profileImageURLs := newProfileImageURLSigner()
		for _, result := range posts {
			item := feedItemResponse{createPostResponse: newPostResponseWithProfileImageSigner(result.Post, result.Author, profileImageURLs)}
			if ranked {
				item.Score = &result.Score
			}
			items = append(items, item)
		}
		return c.JSON(http.StatusOK, getFeedResponse{Items: items, HasMore: hasMore})
	}
}

type feedPostResult struct {
	PostWithAuthor
	Score int64 `db:"score"`
}

// selectFeedPosts applies eligibility and pagination before loading the page's
// full post and author records. Ranked feeds must score every eligible
// candidate before limiting, so their first CTE contains only lightweight IDs.
func selectFeedPosts(ctx context.Context, db sqlx.QueryerContext, viewerID string, input getFeedRequest) ([]feedPostResult, error) {
	feedPredicate := ""
	switch input.Feed {
	case "public":
		feedPredicate = `AND p.visibility = 'public'`
	case "friends":
		feedPredicate = `AND EXISTS (SELECT 1 FROM friendships f WHERE (f.user_low_id = $1 AND f.user_high_id = p.author_id) OR (f.user_high_id = $1 AND f.user_low_id = p.author_id))`
	case "following":
		feedPredicate = `AND EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = $1 AND f.followee_id = p.author_id)`
	}
	visibilityPredicate := `AND (p.visibility = 'public'
      OR (p.visibility = 'friends' AND EXISTS (SELECT 1 FROM friendships f WHERE (f.user_low_id = $1 AND f.user_high_id = p.author_id) OR (f.user_high_id = $1 AND f.user_low_id = p.author_id)))
      OR (p.visibility = 'friends_of_friends' AND (
        EXISTS (SELECT 1 FROM friendships f WHERE (f.user_low_id = $1 AND f.user_high_id = p.author_id) OR (f.user_high_id = $1 AND f.user_low_id = p.author_id))
        OR EXISTS (
          SELECT 1 FROM friendships vf
          JOIN friendships af ON af.user_low_id = CASE WHEN vf.user_low_id = $1 THEN vf.user_high_id ELSE vf.user_low_id END
                            OR af.user_high_id = CASE WHEN vf.user_low_id = $1 THEN vf.user_high_id ELSE vf.user_low_id END
          WHERE (vf.user_low_id = $1 OR vf.user_high_id = $1)
            AND CASE WHEN af.user_low_id = CASE WHEN vf.user_low_id = $1 THEN vf.user_high_id ELSE vf.user_low_id END THEN af.user_high_id ELSE af.user_low_id END = p.author_id
        )
      )))`
	if input.Feed == "friends" {
		// The feed predicate already requires a direct friendship, so every supported visibility is viewable; the general friends-of-friends search would repeat a large graph traversal for no additional authorization.
		visibilityPredicate = `AND p.visibility IN ('public', 'friends', 'friends_of_friends')`
	}
	base := `
  FROM posts p
  JOIN posting_windows w ON w.id = p.posting_window_id
  JOIN users u ON u.id = p.author_id AND u.status = 'active'
  WHERE p.deleted_at IS NULL AND p.author_id <> $1 AND w.ended_at IS NULL
    AND NOT EXISTS (SELECT 1 FROM block_relations b WHERE (b.blocker_id = $1 AND b.blocked_id = p.author_id) OR (b.blocked_id = $1 AND b.blocker_id = p.author_id))
    ` + visibilityPredicate + `
    ` + feedPredicate

	if input.Feed != "friends-of-friends" {
		query := `SELECT ` + postColumns + `, ` + userColumnsAs("u", "author") + `, 0::bigint AS score` + base + `
  AND ($2::timestamptz IS NULL OR (p.posted_at, p.id COLLATE "C") < ($2, $3::varchar COLLATE "C"))
  ORDER BY p.posted_at DESC, p.id COLLATE "C" DESC LIMIT 51`
		var posts []feedPostResult
		if err := sqlx.SelectContext(ctx, db, &posts, query, viewerID, input.BeforePostedAt, input.BeforeID); err != nil {
			return nil, err
		}
		for i := range posts {
			if err := posts[i].Author.setImageKey(); err != nil {
				return nil, err
			}
		}
		return posts, nil
	}
	query := `WITH viewer_friends AS (
  SELECT CASE WHEN user_low_id = $1 THEN user_high_id ELSE user_low_id END AS id
  FROM friendships WHERE user_low_id = $1 OR user_high_id = $1
), viewer_mutuals AS (
  SELECT CASE WHEN f.user_low_id = vf.id THEN f.user_high_id ELSE f.user_low_id END AS id, count(*)::bigint AS count
  FROM viewer_friends vf JOIN friendships f ON f.user_low_id = vf.id OR f.user_high_id = vf.id
  WHERE f.user_low_id <> $1 AND f.user_high_id <> $1
  GROUP BY 1
), candidates AS (
  SELECT p.id, p.author_id, p.posted_at,
    CASE WHEN u.region IN ('america', 'europe') THEN 0 ELSE 1 END AS region_priority
  FROM posts p
  JOIN posting_windows w ON w.id = p.posting_window_id
  JOIN users u ON u.id = p.author_id AND u.status = 'active'
  LEFT JOIN viewer_mutuals candidate_mutual ON candidate_mutual.id = p.author_id
  WHERE p.deleted_at IS NULL AND p.author_id <> $1 AND w.ended_at IS NULL
    AND NOT EXISTS (SELECT 1 FROM block_relations b WHERE (b.blocker_id = $1 AND b.blocked_id = p.author_id) OR (b.blocked_id = $1 AND b.blocker_id = p.author_id))
    AND NOT EXISTS (SELECT 1 FROM viewer_friends vf WHERE vf.id = p.author_id)
    AND candidate_mutual.count > 0
    AND p.visibility IN ('public', 'friends_of_friends')
), cursor_region AS (
  -- Resolve the cursor from eligible candidates in this statement's snapshot; the public cursor has no region-rank field.
  SELECT c.region_priority AS priority
  FROM candidates c
  WHERE c.id = $4
), author_connections AS (
  SELECT a.author_id, sum(coalesce(vm.count, 0))::bigint AS score
  FROM (SELECT DISTINCT author_id FROM candidates) a
  JOIN friendships f ON f.user_low_id = a.author_id OR f.user_high_id = a.author_id
  LEFT JOIN viewer_mutuals vm ON vm.id = CASE WHEN f.user_low_id = a.author_id THEN f.user_high_id ELSE f.user_low_id END
  GROUP BY a.author_id
), engagements AS (
  SELECT post_id, user_id FROM (
    SELECT r.post_id, r.reactor_id AS user_id FROM realmojis r
    JOIN candidates c ON c.id = r.post_id JOIN users eu ON eu.id = r.reactor_id AND eu.status = 'active'
    WHERE r.replaced_at IS NULL AND r.deleted_at IS NULL
    UNION
    SELECT cm.post_id, cm.author_id AS user_id FROM comments cm
    JOIN candidates c ON c.id = cm.post_id JOIN users eu ON eu.id = cm.author_id AND eu.status = 'active'
    WHERE cm.deleted_at IS NULL
  ) e JOIN viewer_friends vf ON vf.id = e.user_id
), engagement_counts AS (
  SELECT post_id, count(*)::bigint AS count FROM engagements GROUP BY post_id
), scored AS (
  SELECT c.id, c.author_id, c.posted_at,
    10 * coalesce(ec.count, 0) + 3 * coalesce(vm.count, 0) + least(coalesce(ac.score, 0), 30) AS score,
    c.region_priority
  FROM candidates c
  LEFT JOIN engagement_counts ec ON ec.post_id = c.id
  LEFT JOIN viewer_mutuals vm ON vm.id = c.author_id
  LEFT JOIN author_connections ac ON ac.author_id = c.author_id
), page AS (
  SELECT * FROM scored
  WHERE ($2::bigint IS NULL
    OR region_priority > (SELECT priority FROM cursor_region)
    OR (region_priority = (SELECT priority FROM cursor_region)
      AND (score, posted_at, id COLLATE "C") < ($2, $3, $4::varchar COLLATE "C")))
  ORDER BY region_priority ASC, score DESC, posted_at DESC, id COLLATE "C" DESC LIMIT 51
)
SELECT ` + postColumns + `, ` + userColumnsAs("u", "author") + `, page.score
FROM page JOIN posts p ON p.id = page.id
JOIN posting_windows w ON w.id = p.posting_window_id
JOIN users u ON u.id = p.author_id AND u.status = 'active'
ORDER BY page.region_priority ASC, page.score DESC, page.posted_at DESC, page.id COLLATE "C" DESC`
	var posts []feedPostResult
	if err := sqlx.SelectContext(ctx, db, &posts, query, viewerID, input.BeforeScore, input.BeforePostedAt, input.BeforeID); err != nil {
		return nil, err
	}
	if len(posts) == 0 && input.BeforeID != "" {
		var cursorExists bool
		if err := sqlx.GetContext(ctx, db, &cursorExists, `
  WITH viewer_friends AS (
    SELECT CASE WHEN user_low_id = $1 THEN user_high_id ELSE user_low_id END AS id
    FROM friendships WHERE user_low_id = $1 OR user_high_id = $1
  )
  SELECT EXISTS (
    SELECT 1 FROM posts p
    JOIN posting_windows w ON w.id = p.posting_window_id AND w.ended_at IS NULL
    JOIN users u ON u.id = p.author_id AND u.status = 'active'
    WHERE p.id = $2 AND p.deleted_at IS NULL AND p.author_id <> $1
      AND p.visibility IN ('public', 'friends_of_friends')
      AND NOT EXISTS (SELECT 1 FROM block_relations b WHERE (b.blocker_id=$1 AND b.blocked_id=p.author_id) OR (b.blocked_id=$1 AND b.blocker_id=p.author_id))
      AND NOT EXISTS (SELECT 1 FROM viewer_friends vf WHERE vf.id = p.author_id)
      AND EXISTS (
        SELECT 1 FROM viewer_friends vf JOIN friendships f
          ON f.user_low_id = vf.id OR f.user_high_id = vf.id
        WHERE CASE WHEN f.user_low_id = vf.id THEN f.user_high_id ELSE f.user_low_id END = p.author_id
      )
  )
 `, viewerID, input.BeforeID); err != nil {
			return nil, err
		}
		if !cursorExists {
			return nil, NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
	}
	for i := range posts {
		if err := posts[i].Author.setImageKey(); err != nil {
			return nil, err
		}
	}
	return posts, nil
}
