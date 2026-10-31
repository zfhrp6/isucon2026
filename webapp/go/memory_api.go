package isureal

import (
	"context"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

type getMemoriesRequest struct {
	Month          string     `query:"month"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type memoryPostResponse struct {
	createPostResponse
	RealMojis []replaceRealMojiResponse `json:"realmojis"`
	Comments  []createCommentResponse   `json:"comments"`
}

type getMemoriesResponse struct {
	Items   []memoryPostResponse `json:"items"`
	HasMore bool                 `json:"hasMore"`
}

type getMemoriesByDateRequest struct {
	Date           string     `param:"date"`
	BeforePostedAt *time.Time `query:"beforePostedAt"`
	BeforeID       string     `query:"beforeId"`
}

type getMemoriesByDateResponse getMemoriesResponse

func getMemoriesHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getMemoriesRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		start, err := time.Parse("2006-01", input.Month)
		if err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_memory_period")
		}
		response, err := listMemories(c.Request().Context(), h.DB, GetSession(c).UserID, start, start.AddDate(0, 1, 0), input.BeforePostedAt, input.BeforeID)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, response)
	}
}

func getMemoriesByDateHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getMemoriesByDateRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		start, err := time.Parse(time.DateOnly, input.Date)
		if err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_memory_period")
		}
		response, err := listMemories(c.Request().Context(), h.DB, GetSession(c).UserID, start, start.AddDate(0, 0, 1), input.BeforePostedAt, input.BeforeID)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, getMemoriesByDateResponse(response))
	}
}

func listMemories(ctx context.Context, db sqlx.QueryerContext, userID string, start, end time.Time, beforeAt *time.Time, beforeID string) (getMemoriesResponse, error) {
	var response getMemoriesResponse
	if (beforeAt == nil) != (beforeID == "") {
		return response, NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	posts, err := selectPostsWithAuthor(ctx, db, `
  AND p.author_id = $1 AND w.local_date >= $2 AND w.local_date < $3
  AND ($4::timestamptz IS NULL OR (p.posted_at, p.id) < ($4, $5))
  ORDER BY p.posted_at DESC, p.id DESC LIMIT 51
 `, userID, start, end, beforeAt, beforeID)
	if err != nil {
		return response, err
	}
	response.HasMore = len(posts) > 50
	if response.HasMore {
		posts = posts[:50]
	}
	items := make([]memoryPostResponse, 0, len(posts))
	profileImageURLs := newProfileImageURLSigner()
	for _, post := range posts {
		items = append(items, memoryPostResponse{
			createPostResponse: newPostResponseWithProfileImageSigner(post.Post, post.Author, profileImageURLs),
			RealMojis:          make([]replaceRealMojiResponse, 0),
			Comments:           make([]createCommentResponse, 0),
		})
	}
	if err := loadMemoryInteractions(ctx, db, items, profileImageURLs); err != nil {
		return response, err
	}
	response.Items = items
	return response, nil
}

func loadMemoryInteractions(ctx context.Context, db sqlx.QueryerContext, items []memoryPostResponse, profileImageURLs *profileImageURLSigner) error {
	if len(items) == 0 {
		return nil
	}
	postIDs := make([]string, len(items))
	itemByPostID := make(map[string]*memoryPostResponse, len(items))
	for i := range items {
		postIDs[i] = items[i].ID
		itemByPostID[items[i].ID] = &items[i]
	}

	query, args, err := sqlx.In(`AND r.id IN (
    SELECT ranked.id FROM (
      SELECT r2.id, row_number() OVER (PARTITION BY r2.post_id ORDER BY r2.created_at DESC, r2.id DESC) AS position
      FROM realmojis r2 JOIN users u2 ON u2.id=r2.reactor_id AND u2.status='active'
      WHERE r2.replaced_at IS NULL AND r2.deleted_at IS NULL AND r2.post_id IN (?)
    ) ranked WHERE ranked.position <= 50
  ) ORDER BY r.created_at DESC, r.id DESC`, postIDs)
	if err != nil {
		return err
	}
	reactions, err := selectCurrentRealMojis(ctx, db, sqlx.Rebind(sqlx.DOLLAR, query), args...)
	if err != nil {
		return err
	}
	for _, reaction := range reactions {
		item := itemByPostID[reaction.PostID]
		if len(item.RealMojis) < 50 {
			item.RealMojis = append(item.RealMojis, newRealMojiResponseWithProfileImageSigner(reaction.RealMoji, reaction.Reactor, profileImageURLs))
		}
	}

	query, args, err = sqlx.In(`AND c.id IN (
    SELECT ranked.id FROM (
      SELECT c2.id, row_number() OVER (PARTITION BY c2.post_id ORDER BY c2.created_at, c2.id) AS position
      FROM comments c2 JOIN users u2 ON u2.id=c2.author_id AND u2.status='active'
      WHERE c2.deleted_at IS NULL AND c2.post_id IN (?)
    ) ranked WHERE ranked.position <= 50
  ) ORDER BY c.created_at, c.id`, postIDs)
	if err != nil {
		return err
	}
	comments, err := selectCommentsWithAuthor(ctx, db, sqlx.Rebind(sqlx.DOLLAR, query), args...)
	if err != nil {
		return err
	}
	for _, comment := range comments {
		item := itemByPostID[comment.PostID]
		if len(item.Comments) < 50 {
			item.Comments = append(item.Comments, newCommentResponseWithProfileImageSigner(comment.Comment, comment.Author, profileImageURLs))
		}
	}
	return nil
}
