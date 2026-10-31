package isureal

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// This test uses a disposable schema in the explicitly configured test DB. It
// exercises the actual PostgreSQL predicates, aggregates, ordering, and joins.
func TestSelectFeedPostsPostgres(t *testing.T) {
	dsn := os.Getenv("ISUREAL_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("ISUREAL_TEST_DATABASE_DSN is not set")
	}
	ctx := context.Background()
	db, err := sqlx.ConnectContext(ctx, "pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := "feed_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE users (id varchar(36) PRIMARY KEY, username varchar(64) NOT NULL, secret_key_hash text NOT NULL, display_name varchar(128) NOT NULL, bio text NOT NULL, profile_image_id varchar(36), region text NOT NULL, account_type text NOT NULL, status text NOT NULL)`,
		`CREATE TABLE posting_windows (id varchar(36) PRIMARY KEY, on_time_deadline timestamptz NOT NULL, ended_at timestamptz)`,
		`CREATE TABLE posts (id varchar(36) PRIMARY KEY, author_id varchar(36) NOT NULL, posting_window_id varchar(36) NOT NULL, image_id varchar(36) NOT NULL, caption text NOT NULL, visibility text NOT NULL, location_kind text NOT NULL, latitude numeric, longitude numeric, locality text, posted_at timestamptz NOT NULL, deleted_at timestamptz)`,
		`CREATE TABLE friendships (id varchar(36) PRIMARY KEY, user_low_id varchar(36) NOT NULL, user_high_id varchar(36) NOT NULL, UNIQUE(user_low_id,user_high_id))`,
		`CREATE TABLE follows (id varchar(36) PRIMARY KEY, follower_id varchar(36) NOT NULL, followee_id varchar(36) NOT NULL)`,
		`CREATE TABLE block_relations (id varchar(36) PRIMARY KEY, blocker_id varchar(36) NOT NULL, blocked_id varchar(36) NOT NULL)`,
		`CREATE TABLE realmojis (post_id varchar(36) NOT NULL, reactor_id varchar(36) NOT NULL, replaced_at timestamptz, deleted_at timestamptz)`,
		`CREATE TABLE comments (post_id varchar(36) NOT NULL, author_id varchar(36) NOT NULL, deleted_at timestamptz)`,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []struct{ id, status string }{{"viewer", "active"}, {"friend", "active"}, {"mutual", "active"}, {"fof", "active"}, {"bridge", "active"}, {"public", "active"}, {"follow", "active"}, {"outsider-fof", "active"}, {"blocked", "active"}, {"blocked-forward", "active"}, {"friend-block-low", "active"}, {"friend-block-high", "active"}, {"inactive", "deleted"}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users VALUES ($1,$1,$2,$1,'',NULL,'asia','regular',$3)`, user.id, strings.Repeat("0", 64), user.status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO posting_windows VALUES ('open','2030-01-02',NULL), ('ended','2030-01-02',now())`); err != nil {
		t.Fatal(err)
	}
	for i, edge := range [][2]string{{"friend", "viewer"}, {"mutual", "viewer"}, {"friend-block-low", "viewer"}, {"viewer", "friend-block-high"}, {"fof", "friend"}, {"fof", "mutual"}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO friendships VALUES ($1,$2,$3)`, fmt.Sprintf("f%d", i), edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 32 {
		id := fmt.Sprintf("viewer-friend-%02d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO users VALUES ($1,$1,$2,$1,'',NULL,'asia','regular','active')`, id, strings.Repeat("0", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO friendships VALUES ($1,'viewer',$2), ($3,'bridge',$2)`, "viewer-edge-"+id, id, "bridge-edge-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users VALUES ('low-bridge','low-bridge',$1,'low-bridge','','','asia','regular','active'), ('low-author','low-author',$1,'low-author','','','asia','regular','active')`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		id := fmt.Sprintf("viewer-friend-%02d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO friendships VALUES ($1,'low-bridge',$2)`, "low-bridge-edge-"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO friendships VALUES ('viewer-low-bridge','viewer','low-bridge')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO friendships VALUES ('low-author-edge','low-author','low-bridge')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO friendships VALUES ('fof-bridge','fof','bridge')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO follows VALUES ('follow-edge','viewer','follow'), ('reverse-follow','follow','public')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO block_relations VALUES ('block','blocked','viewer'), ('block-forward','viewer','blocked-forward'), ('friend-block-forward','viewer','friend-block-low'), ('friend-block-reverse','friend-block-high','viewer')`); err != nil {
		t.Fatal(err)
	}
	baseTime := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	posts := []struct {
		id, author, window, visibility string
		at                             time.Time
		deleted                        bool
	}{
		{"public-post", "public", "open", "public", baseTime.Add(5 * time.Minute), false},
		{"friend-post", "friend", "open", "friends", baseTime.Add(4 * time.Minute), false},
		{"friend-public", "friend", "open", "public", baseTime.Add(350 * time.Second), false},
		{"friend-fof", "friend", "open", "friends_of_friends", baseTime.Add(325 * time.Second), false},
		{"following-post", "follow", "open", "public", baseTime.Add(3 * time.Minute), false},
		{"outsider-friends", "follow", "open", "friends", baseTime.Add(275 * time.Second), false},
		{"outsider-fof", "outsider-fof", "open", "friends_of_friends", baseTime.Add(250 * time.Second), false},
		{"fof-a", "fof", "open", "friends_of_friends", baseTime.Add(2 * time.Minute), false},
		{"fof-b", "fof", "open", "public", baseTime.Add(1 * time.Minute), false},
		{"fof-c", "fof", "open", "public", baseTime.Add(1 * time.Minute), false},
		{"fof-low-a", "low-author", "open", "friends_of_friends", baseTime.Add(30 * time.Second), false},
		{"fof-low-b", "low-author", "open", "friends_of_friends", baseTime.Add(20 * time.Second), false},
		{"blocked-post", "blocked", "open", "public", baseTime, false},
		{"blocked-forward-post", "blocked-forward", "open", "public", baseTime, false},
		{"friend-block-low-post", "friend-block-low", "open", "public", baseTime, false},
		{"friend-block-high-post", "friend-block-high", "open", "friends", baseTime, false},
		{"inactive-post", "inactive", "open", "public", baseTime, false},
		{"ended-post", "public", "ended", "public", baseTime, false},
		{"deleted-post", "public", "open", "public", baseTime, true},
		{"private-post", "mutual", "open", "friends", baseTime, false},
	}
	for _, post := range posts {
		var deleted any
		if post.deleted {
			deleted = baseTime
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO posts VALUES ($1,$2,$3,'image','caption',$4,'none',NULL,NULL,NULL,$5,$6)`, post.id, post.author, post.window, post.visibility, post.at, deleted); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 55 {
		id := fmt.Sprintf("blocked-%02d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO posts VALUES ($1,'blocked','open','image','caption','public','none',NULL,NULL,NULL,$2,NULL)`, id, baseTime.Add(time.Duration(10+i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	// One friend engaging through both mechanisms contributes only ten points.
	if _, err := tx.ExecContext(ctx, `INSERT INTO realmojis VALUES ('fof-a','friend',NULL,NULL), ('fof-a','inactive',NULL,NULL), ('fof-a','friend',now(),NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments VALUES ('fof-a','friend',NULL), ('fof-a','inactive',NULL), ('fof-a','mutual',NULL)`); err != nil {
		t.Fatal(err)
	}
	viewer := viewerContext{
		User: User{ID: "viewer"}, HasPostedToday: true,
		Friends:          map[string]bool{"friend": true, "mutual": true, "friend-block-low": true, "friend-block-high": true},
		FriendsOfFriends: map[string]bool{"fof": true, "low-author": true},
		MutualFriends:    map[string]int{"fof": 2, "bridge": 32, "low-bridge": 7, "low-author": 1},
		Blocked:          map[string]bool{"blocked": true, "blocked-forward": true, "friend-block-low": true, "friend-block-high": true},
	}
	var pageQueryPlan *feedQueryPlan
	var rankedQueryPlan *feedQueryPlan
	allPosts, err := selectPostsWithAuthor(ctx, tx, `AND p.author_id <> $1 AND w.ended_at IS NULL ORDER BY p.posted_at DESC, p.id DESC`, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		feed string
		want []string
	}{
		{"public", []string{"friend-public", "public-post", "following-post", "fof-c", "fof-b"}},
		{"friends", []string{"friend-public", "friend-fof", "friend-post", "private-post"}},
		{"following", []string{"following-post"}},
		{"friends-of-friends", []string{"fof-a", "fof-c", "fof-b", "fof-low-a", "fof-low-b"}},
	} {
		t.Run(test.feed, func(t *testing.T) {
			var queryer sqlx.QueryerContext = tx
			if test.feed == "public" {
				pageQueryPlan = &feedQueryPlan{QueryerContext: tx}
				queryer = pageQueryPlan
			} else if test.feed == "friends-of-friends" {
				rankedQueryPlan = &feedQueryPlan{QueryerContext: tx}
				queryer = rankedQueryPlan
			}
			got, err := selectFeedPosts(ctx, queryer, viewer, getFeedRequest{Feed: test.feed})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("got %d posts, want %d", len(got), len(test.want))
			}
			for i, id := range test.want {
				if got[i].ID != id {
					t.Errorf("post[%d] = %q, want %q", i, got[i].ID, id)
				}
			}
			if test.feed == "friends-of-friends" {
				if got[0].Score != 56 || got[1].Score != 36 || got[2].Score != 36 || got[3].Score != 10 || got[4].Score != 10 {
					t.Errorf("scores = %d, %d, %d, %d, %d; want 56, 36, 36, 10, 10", got[0].Score, got[1].Score, got[2].Score, got[3].Score, got[4].Score)
				}
				cursor := getFeedRequest{Feed: test.feed, BeforeScore: &got[0].Score, BeforePostedAt: &got[0].PostedAt, BeforeID: got[0].ID}
				next, err := selectFeedPosts(ctx, tx, viewer, cursor)
				if err != nil {
					t.Fatal(err)
				}
				if len(next) != 4 || next[0].ID != "fof-c" || next[1].ID != "fof-b" || next[2].ID != "fof-low-a" || next[3].ID != "fof-low-b" {
					t.Errorf("ranked next page = %#v, want fof-c, fof-b, fof-low-a, fof-low-b", next)
				}
			} else if test.feed == "public" {
				cursor := getFeedRequest{Feed: test.feed, BeforePostedAt: &got[0].PostedAt, BeforeID: got[0].ID}
				next, err := selectFeedPosts(ctx, tx, viewer, cursor)
				if err != nil {
					t.Fatal(err)
				}
				if len(next) != 4 || next[0].ID != "public-post" {
					t.Errorf("ordinary next page = %#v, want public-post, following-post, fof-c, fof-b", next)
				}
			}
			following := map[string]bool{"follow": true}
			oracle := make([]PostWithAuthor, 0)
			for _, post := range allPosts {
				if viewer.canView(post.Post) && matchesFeed(viewer, following, post.Post, test.feed) {
					oracle = append(oracle, post)
				}
			}
			scores := map[string]int64{}
			if test.feed == "friends-of-friends" {
				scores, err = recommendationScores(ctx, tx, viewer, oracle)
				if err != nil {
					t.Fatal(err)
				}
				sort.SliceStable(oracle, func(i, j int) bool { return scores[oracle[i].ID] > scores[oracle[j].ID] })
			}
			if len(oracle) > 51 {
				oracle = oracle[:51]
			}
			if len(got) != len(oracle) {
				t.Fatalf("SQL returned %d posts, legacy oracle returned %d", len(got), len(oracle))
			}
			for i := range oracle {
				if got[i].ID != oracle[i].ID || got[i].Score != scores[oracle[i].ID] {
					t.Errorf("SQL result[%d] = (%s,%d), legacy oracle = (%s,%d)", i, got[i].ID, got[i].Score, oracle[i].ID, scores[oracle[i].ID])
				}
			}
			if test.feed == "public" {
				t.Logf("synthetic public candidates=%d, returned=%d", len(oracle), len(got))
			} else if test.feed == "friends-of-friends" {
				t.Logf("synthetic ranked candidates=%d, returned=%d; EXPLAIN (ANALYZE, BUFFERS):\n%s", len(oracle), len(got), strings.Join(rankedQueryPlan.lines, "\n"))
			}
		})
	}
	for i := range 55 {
		id := fmt.Sprintf("friend-page-%02d", i)
		visibility := []string{"public", "friends", "friends_of_friends"}[i%3]
		if _, err := tx.ExecContext(ctx, `INSERT INTO posts VALUES ($1,'friend','open','image','caption',$2,'none',NULL,NULL,NULL,$3,NULL)`, id, visibility, baseTime.Add(time.Duration(30+i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	friendPage, err := selectFeedPosts(ctx, tx, viewer, getFeedRequest{Feed: "friends"})
	if err != nil {
		t.Fatal(err)
	}
	friendOracle, err := selectPostsWithAuthor(ctx, tx, `AND p.author_id <> $1 AND w.ended_at IS NULL ORDER BY p.posted_at DESC, p.id DESC`, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	following := map[string]bool{"follow": true}
	wantFriends := make([]string, 0)
	for _, post := range friendOracle {
		if viewer.canView(post.Post) && matchesFeed(viewer, following, post.Post, "friends") {
			wantFriends = append(wantFriends, post.ID)
		}
	}
	if len(friendPage) != 51 || len(wantFriends) <= 50 {
		t.Fatalf("friends first page rows=%d, legacy eligible=%d; want SQL LIMIT 51 and hasMore", len(friendPage), len(wantFriends))
	}
	for i := range friendPage {
		if friendPage[i].ID != wantFriends[i] {
			t.Fatalf("friends page[%d]=%q, legacy oracle=%q", i, friendPage[i].ID, wantFriends[i])
		}
	}
	friendCursor := getFeedRequest{Feed: "friends", BeforePostedAt: &friendPage[49].PostedAt, BeforeID: friendPage[49].ID}
	nextFriends, err := selectFeedPosts(ctx, tx, viewer, friendCursor)
	if err != nil {
		t.Fatal(err)
	}
	wantNext := wantFriends[50:]
	if len(nextFriends) != len(wantNext) {
		t.Fatalf("friends next page rows=%d, legacy oracle=%d", len(nextFriends), len(wantNext))
	}
	for i := range nextFriends {
		if nextFriends[i].ID != wantNext[i] {
			t.Fatalf("friends next page[%d]=%q, legacy oracle=%q", i, nextFriends[i].ID, wantNext[i])
		}
	}
	for i := range 55 {
		id := fmt.Sprintf("eligible-%02d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO posts VALUES ($1,'public','open','image','caption','public','none',NULL,NULL,NULL,$2,NULL)`, id, baseTime.Add(time.Duration(30+i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	pageQueryPlan = &feedQueryPlan{QueryerContext: tx}
	page, err := selectFeedPosts(ctx, pageQueryPlan, viewer, getFeedRequest{Feed: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 51 {
		t.Errorf("public page returned %d rows, want LIMIT 51", len(page))
	}
	t.Logf("synthetic public candidates=59, returned=%d; EXPLAIN (ANALYZE, BUFFERS):\n%s", len(page), strings.Join(pageQueryPlan.lines, "\n"))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type feedQueryPlan struct {
	sqlx.QueryerContext
	lines []string
}

func (q *feedQueryPlan) QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error) {
	rows, err := q.QueryerContext.QueryxContext(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			return nil, err
		}
		q.lines = append(q.lines, line)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return q.QueryerContext.QueryxContext(ctx, query, args...)
}

// These preserve the pre-SQL feed filtering and scoring as an integration-test
// oracle so the optimized query can be compared against existing behavior.
func matchesFeed(viewer viewerContext, following map[string]bool, post Post, feed string) bool {
	switch feed {
	case "public":
		return post.Visibility == "public"
	case "following":
		return following[post.AuthorID]
	case "friends":
		return viewer.Friends[post.AuthorID]
	case "friends-of-friends":
		return !viewer.Friends[post.AuthorID] && viewer.FriendsOfFriends[post.AuthorID]
	default:
		return false
	}
}

func recommendationScores(ctx context.Context, db sqlx.QueryerContext, viewer viewerContext, posts []PostWithAuthor) (map[string]int64, error) {
	scores := make(map[string]int64, len(posts))
	if len(posts) == 0 {
		return scores, nil
	}
	authorIDs := make([]string, 0, len(posts))
	postIDs := make([]string, 0, len(posts))
	for _, post := range posts {
		authorIDs = append(authorIDs, post.AuthorID)
		postIDs = append(postIDs, post.ID)
	}

	query, args, err := sqlx.In(`SELECT user_low_id, user_high_id FROM friendships WHERE user_low_id IN (?) OR user_high_id IN (?)`, authorIDs, authorIDs)
	if err != nil {
		return nil, err
	}
	var friendships []struct {
		UserLowID  string `db:"user_low_id"`
		UserHighID string `db:"user_high_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &friendships, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return nil, err
	}
	connections := map[string]int64{}
	for _, friendship := range friendships {
		connections[friendship.UserLowID] += int64(viewer.MutualFriends[friendship.UserHighID])
		connections[friendship.UserHighID] += int64(viewer.MutualFriends[friendship.UserLowID])
	}

	query, args, err = sqlx.In(`
  SELECT r.post_id, r.reactor_id AS user_id FROM realmojis r
  JOIN users u ON u.id = r.reactor_id AND u.status = 'active'
  WHERE r.post_id IN (?) AND r.replaced_at IS NULL AND r.deleted_at IS NULL
  UNION
  SELECT c.post_id, c.author_id AS user_id FROM comments c
  JOIN users u ON u.id = c.author_id AND u.status = 'active'
  WHERE c.post_id IN (?) AND c.deleted_at IS NULL
 `, postIDs, postIDs)
	if err != nil {
		return nil, err
	}
	var engagements []struct {
		PostID string `db:"post_id"`
		UserID string `db:"user_id"`
	}
	if err := sqlx.SelectContext(ctx, db, &engagements, sqlx.Rebind(sqlx.DOLLAR, query), args...); err != nil {
		return nil, err
	}
	friendEngagements := map[string]int64{}
	for _, engagement := range engagements {
		if viewer.Friends[engagement.UserID] {
			friendEngagements[engagement.PostID]++
		}
	}

	for _, post := range posts {
		scores[post.ID] = 10*friendEngagements[post.ID] + 3*int64(viewer.MutualFriends[post.AuthorID]) + min(connections[post.AuthorID], 30)
	}
	return scores, nil
}
