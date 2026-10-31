SET session_replication_role = replica;

BEGIN;

-- Go の normalizeSearchText（cases.Fold + NFC）の値。PostgreSQL の lower() とは一致しないため、
-- 初期データの値は初期化処理が Go で計算して initial_users に埋める。
ALTER TABLE users ADD COLUMN IF NOT EXISTS username_norm text;
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name_norm text;
ALTER TABLE initial_users ADD COLUMN IF NOT EXISTS username_norm text;
ALTER TABLE initial_users ADD COLUMN IF NOT EXISTS display_name_norm text;

TRUNCATE ip_bans, login_sessions, administrator_sessions, friendships, friend_requests, follows, block_relations;

DELETE FROM comments WHERE created_at > (SELECT created_at FROM initial_data_boundary);
DELETE FROM realmojis WHERE created_at > (SELECT created_at FROM initial_data_boundary);
DELETE FROM posts WHERE created_at > (SELECT created_at FROM initial_data_boundary);
DELETE FROM posting_windows WHERE created_at > (SELECT created_at FROM initial_data_boundary);
DO $$
DECLARE partition_name text;
BEGIN
  FOR partition_name IN
    SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
    WHERE i.inhparent = 'image_objects'::regclass
      AND c.relname > 'image_objects_' || to_char((SELECT created_at FROM initial_data_boundary) AT TIME ZONE 'UTC', 'YYYYMM')
  LOOP
    EXECUTE format('TRUNCATE %I', partition_name);
  END LOOP;
END $$;
DELETE FROM image_objects WHERE created_at > (SELECT created_at FROM initial_data_boundary);
DELETE FROM users WHERE created_at > (SELECT created_at FROM initial_data_boundary);

UPDATE users u
SET display_name = i.display_name, bio = i.bio, profile_image_id = i.profile_image_id, region = i.region,
    account_type = i.account_type, status = i.status, deleted_at = i.deleted_at, updated_at = i.updated_at,
    username_norm = i.username_norm, display_name_norm = i.display_name_norm
FROM initial_users i
WHERE u.id = i.id
  AND (u.display_name, u.bio, u.profile_image_id, u.region, u.account_type, u.status, u.deleted_at, u.updated_at, u.username_norm, u.display_name_norm)
      IS DISTINCT FROM (i.display_name, i.bio, i.profile_image_id, i.region, i.account_type, i.status, i.deleted_at, i.updated_at, i.username_norm, i.display_name_norm);

INSERT INTO friend_requests (id, requester_id, recipient_id, status, created_at, updated_at)
SELECT id, requester_id, recipient_id, status, created_at, updated_at FROM initial_friend_requests;
INSERT INTO friendships (id, user_low_id, user_high_id, source_request_id, created_at, updated_at)
SELECT id, user_low_id, user_high_id, source_request_id, created_at, updated_at FROM initial_friendships;
INSERT INTO follows (id, follower_id, followee_id, created_at, updated_at)
SELECT id, follower_id, followee_id, created_at, updated_at FROM initial_follows;
INSERT INTO block_relations (id, blocker_id, blocked_id, created_at)
SELECT id, blocker_id, blocked_id, created_at FROM initial_block_relations;

CREATE INDEX IF NOT EXISTS posts_image_id_author_id ON posts(image_id, author_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS posts_author_posted_at_id ON posts(author_id, posted_at, id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS posts_window_posted_at_id ON posts(posting_window_id, posted_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS realmojis_image_id_reactor_id ON realmojis(image_id, reactor_id) WHERE replaced_at IS NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS friendships_user_high_id ON friendships(user_high_id);
CREATE INDEX IF NOT EXISTS users_region_status ON users(region, status);
CREATE INDEX IF NOT EXISTS users_status_username_id ON users(status, username, id);
CREATE UNIQUE INDEX IF NOT EXISTS users_username ON users (username);
CREATE INDEX IF NOT EXISTS friend_requests_pending_pair ON friend_requests(requester_id, recipient_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS friend_requests_pending_recipient ON friend_requests(recipient_id, requester_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS login_sessions_token_hash ON login_sessions(token_hash) WHERE active;
CREATE INDEX IF NOT EXISTS login_sessions_user_id ON login_sessions(user_id) WHERE active;
CREATE INDEX IF NOT EXISTS block_relations_blocked_id ON block_relations(blocked_id);
CREATE INDEX IF NOT EXISTS follows_followee_id ON follows(followee_id);

-- VOLATILE なので文ごとにスナップショットを取り直し、advisory lock を待ったあとの文は最新の関係を読む。
-- p_pair_key は Go の userPairAdvisoryKey の値で、lockUserPair と同じペアを直列化する。
-- result は ok / not_found / conflict で、ok のときだけ relation_* が入る。
CREATE OR REPLACE FUNCTION create_friend_request(
  p_actor text,
  p_target text,
  p_pair_key bigint,
  OUT result text,
  OUT relation_id text,
  OUT relation_status text,
  OUT relation_updated_at timestamptz
) LANGUAGE plpgsql AS $$
DECLARE
  v_active_users integer;
  v_pending_id text;
  v_pending_requester_id text;
BEGIN
  PERFORM pg_advisory_xact_lock(p_pair_key);

  SELECT count(*) INTO v_active_users FROM (
    SELECT 1 FROM users
    WHERE id IN (p_actor, p_target) AND status = 'active'
    ORDER BY id
    FOR KEY SHARE
  ) AS locked;
  IF v_active_users <> 2 THEN
    result := 'not_found';
    RETURN;
  END IF;

  IF EXISTS (
    SELECT 1 FROM block_relations
    WHERE (blocker_id = p_actor AND blocked_id = p_target) OR (blocker_id = p_target AND blocked_id = p_actor)
  ) OR EXISTS (
    SELECT 1 FROM friendships
    WHERE user_low_id = LEAST(p_actor, p_target) AND user_high_id = GREATEST(p_actor, p_target)
  ) THEN
    result := 'conflict';
    RETURN;
  END IF;

  SELECT id, requester_id INTO v_pending_id, v_pending_requester_id FROM friend_requests
  WHERE status = 'pending'
    AND ((requester_id = p_actor AND recipient_id = p_target) OR (requester_id = p_target AND recipient_id = p_actor))
  LIMIT 1;

  IF v_pending_id IS NULL THEN
    INSERT INTO friend_requests (id, requester_id, recipient_id, status, created_at, updated_at)
    VALUES (gen_random_uuid(), p_actor, p_target, 'pending', clock_timestamp(), clock_timestamp())
    RETURNING id, status, updated_at INTO relation_id, relation_status, relation_updated_at;
  ELSIF v_pending_requester_id = p_actor THEN
    result := 'conflict';
    RETURN;
  ELSE
    UPDATE friend_requests SET status = 'accepted', updated_at = clock_timestamp()
    WHERE id = v_pending_id
    RETURNING id, status, updated_at INTO relation_id, relation_status, relation_updated_at;
    INSERT INTO friendships (id, user_low_id, user_high_id, source_request_id)
    VALUES (gen_random_uuid(), LEAST(p_actor, p_target), GREATEST(p_actor, p_target), v_pending_id);
  END IF;

  result := 'ok';
END;
$$;

-- Go の userPairAdvisoryKey と同じ値を返す。
CREATE OR REPLACE FUNCTION user_pair_advisory_key(p_a text, p_b text) RETURNS bigint LANGUAGE sql IMMUTABLE AS $$
  SELECT ('x' || encode(substring(
    sha256(convert_to(LEAST(p_a COLLATE "C", p_b COLLATE "C"), 'UTF8') || '\x00'::bytea || convert_to(GREATEST(p_a COLLATE "C", p_b COLLATE "C"), 'UTF8'))
    FROM 1 FOR 8), 'hex'))::bit(64)::bigint;
$$;

-- p_status は accepted / rejected / cancelled。
-- result は ok / not_found / forbidden / conflict / invalid で、ok のときだけ relation_* が入る。
CREATE OR REPLACE FUNCTION transition_friend_request(
  p_actor text,
  p_request_id text,
  p_status text,
  OUT result text,
  OUT relation_id text,
  OUT relation_status text,
  OUT relation_updated_at timestamptz
) LANGUAGE plpgsql AS $$
DECLARE
  v_requester_id text;
  v_recipient_id text;
  v_active_users integer;
  v_current_status text;
BEGIN
  SELECT requester_id, recipient_id INTO v_requester_id, v_recipient_id FROM friend_requests WHERE id = p_request_id;
  IF NOT FOUND THEN
    result := 'not_found';
    RETURN;
  END IF;
  IF (p_status = 'cancelled' AND p_actor <> v_requester_id) OR (p_status <> 'cancelled' AND p_actor <> v_recipient_id) THEN
    result := 'forbidden';
    RETURN;
  END IF;
  IF v_requester_id = v_recipient_id THEN
    result := 'invalid';
    RETURN;
  END IF;

  PERFORM pg_advisory_xact_lock(user_pair_advisory_key(v_requester_id, v_recipient_id));

  SELECT count(*) INTO v_active_users FROM (
    SELECT 1 FROM users
    WHERE id IN (v_requester_id, v_recipient_id) AND status = 'active'
    ORDER BY id
    FOR KEY SHARE
  ) AS locked;
  IF v_active_users <> 2 THEN
    result := 'not_found';
    RETURN;
  END IF;

  SELECT status INTO STRICT v_current_status FROM friend_requests WHERE id = p_request_id;
  IF v_current_status <> 'pending' THEN
    result := 'forbidden';
    RETURN;
  END IF;

  IF p_status = 'accepted' THEN
    IF EXISTS (
      SELECT 1 FROM block_relations
      WHERE (blocker_id = v_requester_id AND blocked_id = v_recipient_id) OR (blocker_id = v_recipient_id AND blocked_id = v_requester_id)
    ) THEN
      result := 'conflict';
      RETURN;
    END IF;
    INSERT INTO friendships (id, user_low_id, user_high_id, source_request_id)
    VALUES (gen_random_uuid(), LEAST(v_requester_id, v_recipient_id), GREATEST(v_requester_id, v_recipient_id), p_request_id);
  END IF;

  UPDATE friend_requests SET status = p_status, updated_at = clock_timestamp()
  WHERE id = p_request_id
  RETURNING id, status, updated_at INTO relation_id, relation_status, relation_updated_at;

  result := 'ok';
END;
$$;

-- 認証に失敗したら行を返さない。有効なセッションが複数残らないよう、users 行のロックの下で置き換える。
CREATE OR REPLACE FUNCTION login_user(p_username text, p_secret_key_hash text, p_token_hash text)
RETURNS SETOF users LANGUAGE plpgsql AS $$
DECLARE
  v_user users;
BEGIN
  SELECT * INTO v_user FROM users WHERE username = p_username FOR NO KEY UPDATE;
  IF NOT FOUND OR v_user.status <> 'active' OR v_user.secret_key_hash <> p_secret_key_hash THEN
    RETURN;
  END IF;

  UPDATE login_sessions
  SET active = false, invalidated_at = clock_timestamp(), reason = 'replaced'
  WHERE user_id = v_user.id AND active = true;
  INSERT INTO login_sessions (id, user_id, token_hash) VALUES (gen_random_uuid(), v_user.id, p_token_hash);

  RETURN NEXT v_user;
END;
$$;

-- users 行のロックの下で読んだ所属リージョンを返す。
CREATE OR REPLACE FUNCTION logout_user(p_user_id text, p_token_hash text) RETURNS text LANGUAGE plpgsql AS $$
DECLARE
  v_region text;
BEGIN
  SELECT region INTO STRICT v_region FROM users WHERE id = p_user_id FOR NO KEY UPDATE;

  UPDATE login_sessions
  SET active = false, invalidated_at = clock_timestamp(), reason = 'logout'
  WHERE user_id = p_user_id AND active = true AND token_hash = p_token_hash;

  RETURN v_region;
END;
$$;

-- 投稿枠は users 行のロックでユーザー単位に直列化する。VOLATILE なので、件数はロックを待ったあとのスナップショットで数える。
-- ウィンドウ行は FOR SHARE にし、別ユーザーの投稿を並ばせない。期間終了の UPDATE とは衝突する。
-- 呼び出し元の文のスナップショットはロックを待つ前に決まるので、応答に使う行はここで読んだものを返す。
-- 投稿枠が空いていなければ行を返さない。
CREATE OR REPLACE FUNCTION create_post(
  p_author_id text,
  p_image_id text,
  p_caption text,
  p_visibility text,
  p_location_kind text,
  p_latitude numeric,
  p_longitude numeric,
  p_locality text,
  OUT new_post posts,
  OUT post_window posting_windows,
  OUT author users
) RETURNS SETOF record LANGUAGE plpgsql AS $$
DECLARE
  v_active_posts bigint;
  v_deleted_posts bigint;
  v_posted_at timestamptz;
BEGIN
  SELECT * INTO STRICT author FROM users WHERE id = p_author_id AND status = 'active' FOR NO KEY UPDATE;

  SELECT * INTO post_window FROM posting_windows WHERE region = author.region AND ended_at IS NULL FOR SHARE;
  IF NOT FOUND THEN
    RETURN;
  END IF;

  SELECT count(*) FILTER (WHERE p.deleted_at IS NULL), count(*) FILTER (WHERE p.deleted_at IS NOT NULL)
  INTO v_active_posts, v_deleted_posts
  FROM posts p JOIN posting_windows w ON w.id = p.posting_window_id
  WHERE p.author_id = p_author_id AND w.local_date = post_window.local_date;

  v_posted_at := clock_timestamp();
  IF v_active_posts <> 0 OR v_deleted_posts >= 2 OR v_posted_at < post_window.starts_at THEN
    RETURN;
  END IF;

  INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility,
                     location_kind, latitude, longitude, locality, posted_at)
  VALUES (gen_random_uuid(), p_author_id, post_window.id, p_image_id, p_caption, p_visibility,
          p_location_kind, p_latitude, p_longitude, p_locality, v_posted_at)
  RETURNING * INTO new_post;

  RETURN NEXT;
END;
$$;

-- p_region_key は Go の regionAdvisoryKey の値で、lockRegionMemberships と同じロックを取る。
-- ユーザー名の重複は users_username の一意性違反としてそのまま呼び出し元に返す。
CREATE OR REPLACE FUNCTION register_user(
  p_region_key integer,
  p_username text,
  p_secret_key_hash text,
  p_display_name text,
  p_region text,
  p_username_norm text,
  p_display_name_norm text
) RETURNS users LANGUAGE plpgsql AS $$
DECLARE
  v_user users;
BEGIN
  PERFORM pg_advisory_xact_lock(1, p_region_key);

  INSERT INTO users (id, username, secret_key_hash, display_name, region, username_norm, display_name_norm)
  VALUES (gen_random_uuid(), p_username, p_secret_key_hash, p_display_name, p_region, p_username_norm, p_display_name_norm)
  RETURNING * INTO v_user;

  RETURN v_user;
END;
$$;

COMMIT;
