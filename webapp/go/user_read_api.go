package isureal

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type searchUsersRequest struct {
	Q              string `query:"q"`
	BeforeUsername string `query:"beforeUsername"`
	BeforeID       string `query:"beforeId"`
}

type searchUsersResponse struct {
	Items   []relationshipUserResponse `json:"items"`
	HasMore bool                       `json:"hasMore"`
}

type userSearchCandidate struct {
	ID          string `db:"id"`
	Username    string `db:"username"`
	DisplayName string `db:"display_name"`
}

func searchUsersHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input searchUsersRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		if !validUserText(input.Q, 1, 100) {
			return NewAPIError(http.StatusBadRequest, "invalid_relationship")
		}
		if (input.BeforeUsername == "") != (input.BeforeID == "") {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		ctx := c.Request().Context()
		viewer := GetSession(c).UserID
		tx, err := h.DB.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		fold := cases.Fold()
		normalize := func(value string) string {
			return norm.NFC.String(fold.String(norm.NFC.String(value)))
		}
		query := normalize(input.Q)
		matchingIDs := make([]string, 0, 51)
		// Stream only fields needed for matching; the repeatable-read snapshot keeps the later detail fetch consistent with these candidates.
		rows, err := tx.QueryxContext(ctx, `
  SELECT u.id, u.username, u.display_name
  FROM users u
  WHERE u.status='active' AND u.id<>$1
    AND ($2='' OR (u.username,u.id)>($2,$3))
    AND NOT EXISTS (
      SELECT 1 FROM block_relations b
      WHERE (b.blocker_id=$1 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$1)
    )
		ORDER BY u.username, u.id`, viewer, input.BeforeUsername, input.BeforeID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var candidate userSearchCandidate
			if err := rows.StructScan(&candidate); err != nil {
				rows.Close()
				return err
			}
			if !strings.Contains(normalize(candidate.Username), query) && !strings.Contains(normalize(candidate.DisplayName), query) {
				continue
			}
			matchingIDs = append(matchingIDs, candidate.ID)
			if len(matchingIDs) == 51 {
				break
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		hasMore := len(matchingIDs) > 50
		if hasMore {
			matchingIDs = matchingIDs[:50]
		}
		items := make([]relationshipUserResponse, 0, len(matchingIDs))
		if len(matchingIDs) != 0 {
			query, args, err := sqlx.In(`
  SELECT `+strings.Join(userColumns, ", ")+`
  FROM users WHERE id IN (?)`, matchingIDs)
			if err != nil {
				return err
			}
			var users []User
			if err := tx.SelectContext(ctx, &users, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
				return err
			}
			usersByID := make(map[string]User, len(users))
			for _, user := range users {
				usersByID[user.ID] = user
			}
			for _, id := range matchingIDs {
				user, ok := usersByID[id]
				if !ok {
					return errors.New("user search candidate missing from repeatable-read snapshot")
				}
				if err := user.setImageKey(); err != nil {
					return err
				}
				if user.AccountType != "certified_public" {
					user.Bio = ""
				}
				items = append(items, newRelationshipUserResponse(user))
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, searchUsersResponse{Items: items, HasMore: hasMore})
	}
}

type getUserRequest struct {
	UserID string `param:"userId"`
}

type getUserResponse relationshipUserResponse

func getUserHandler(h *HandlerContext) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var input getUserRequest
		if err := c.Bind(&input); err != nil {
			return err
		}
		ctx := c.Request().Context()
		viewer := GetSession(c).UserID
		user, err := getUser(ctx, h.DB, input.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		}
		if err != nil {
			return err
		}
		blocked, err := isBlocked(ctx, h.DB, viewer, user.ID)
		if err != nil {
			return err
		}
		if blocked {
			return NewAPIError(http.StatusNotFound, "relationship_not_found")
		}
		var friends bool
		if err := h.DB.GetContext(ctx, &friends, `
   SELECT EXISTS(
    SELECT 1 FROM friendships
    WHERE (user_low_id=$1 AND user_high_id=$2)
       OR (user_low_id=$2 AND user_high_id=$1)
   )
  `, viewer, user.ID); err != nil {
			return err
		}
		if user.AccountType != "certified_public" && !friends {
			user.Bio = ""
		}
		return c.JSON(http.StatusOK, getUserResponse(newRelationshipUserResponse(user)))
	}
}

func newRelationshipUserResponse(user User) relationshipUserResponse {
	return newRelationshipUserResponseWithProfileImageURL(user, newGetMeResponse(user).ProfileImageURL)
}

func newRelationshipUserResponseWithProfileImageURL(user User, profileImageURL *string) relationshipUserResponse {
	return relationshipUserResponse{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, ProfileImageURL: profileImageURL, Bio: user.Bio, AccountType: user.AccountType}
}
