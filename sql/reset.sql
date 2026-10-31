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
-- pg_trgm は trusted extension。contrib が無い環境では検索結果は同じで、3 文字以上の検索だけ索引を使わない。
DO $$
BEGIN
  CREATE EXTENSION IF NOT EXISTS pg_trgm;
EXCEPTION
  WHEN insufficient_privilege OR undefined_file THEN
    RAISE WARNING 'pg_trgm is unavailable: %', SQLERRM;
END $$;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
    CREATE INDEX IF NOT EXISTS users_active_username_norm_trgm
      ON users USING gin (username_norm gin_trgm_ops)
      WHERE status = 'active';
    CREATE INDEX IF NOT EXISTS users_active_display_name_norm_trgm
      ON users USING gin (display_name_norm gin_trgm_ops)
      WHERE status = 'active';
  END IF;
END $$;
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

COMMIT;
