SET session_replication_role = replica;

BEGIN;

-- Go の normalizeSearchText（cases.Fold + NFC）の値。PostgreSQL の lower() とは一致しないため、
-- 初期データの値は初期化処理が Go で計算して initial_users に埋める。
ALTER TABLE users ADD COLUMN IF NOT EXISTS username_norm text;
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name_norm text;
ALTER TABLE initial_users ADD COLUMN IF NOT EXISTS username_norm text;
ALTER TABLE initial_users ADD COLUMN IF NOT EXISTS display_name_norm text;
-- 実行中に投稿した画像はバイト列を DB に書かず（data は空）、書き込んだサーバのローカルにだけ置く。そのサーバのリージョン。
ALTER TABLE image_objects ADD COLUMN IF NOT EXISTS origin_region text;
-- GET /api/images の画像メタの引き当て用。分割のない表なので id だけの検索で 36 パーティションをロックしない。
-- 行は作成後に変わらず、画像の削除でも消さない（可否は別のクエリで判定する）。
CREATE TABLE IF NOT EXISTS image_meta (
  id varchar(36) PRIMARY KEY,
  owner_user_id varchar(36) NOT NULL,
  media_type text NOT NULL,
  origin_region text,
  created_at timestamptz NOT NULL DEFAULT now()
);

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
DELETE FROM image_meta WHERE created_at > (SELECT created_at FROM initial_data_boundary);
INSERT INTO image_meta (id, owner_user_id, media_type, origin_region, created_at)
SELECT id, owner_user_id, media_type, origin_region, created_at FROM image_objects
WHERE created_at <= (SELECT created_at FROM initial_data_boundary) AND NOT EXISTS (SELECT 1 FROM image_meta)
ON CONFLICT DO NOTHING;

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
-- 申請だけのときは standby の反映を待たずに commit する。
-- set_config の第3引数 true は呼び出し元のトランザクションの終わりまで効くので、明示トランザクションの中では呼ばない。
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
  PERFORM set_config('synchronous_commit', 'off', true);
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
    -- 友達になると friends-of-friends から消える投稿があるので、承認は standby の反映を待つ。
    PERFORM set_config('synchronous_commit', 'remote_apply', true);
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
-- standby の反映を待たずに commit する。置き換えた古いセッションが standby でまだ有効に見えるので、
-- Go 側はキャッシュにないセッションを primary から引く。明示トランザクションの中では呼ばない。
CREATE OR REPLACE FUNCTION login_user(p_username text, p_secret_key_hash text, p_token_hash text)
RETURNS SETOF users LANGUAGE plpgsql AS $$
DECLARE
  v_user users;
BEGIN
  PERFORM set_config('synchronous_commit', 'off', true);
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
  PERFORM set_config('synchronous_commit', 'off', true);
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
-- 画像の行も同じトランザクションで書く。バイト列は DB に書かず、p_image_origin のサーバのローカルにある。
DROP FUNCTION IF EXISTS create_post(text, text, text, text, text, numeric, numeric, text);
CREATE OR REPLACE FUNCTION create_post(
  p_author_id text,
  p_image_id text,
  p_image_origin text,
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

  INSERT INTO image_objects (id, owner_user_id, data, media_type, origin_region)
  VALUES (p_image_id, p_author_id, '', 'image/jpeg', p_image_origin);
  INSERT INTO image_meta (id, owner_user_id, media_type, origin_region)
  VALUES (p_image_id, p_author_id, 'image/jpeg', p_image_origin);

  INSERT INTO posts (id, author_id, posting_window_id, image_id, caption, visibility,
                     location_kind, latitude, longitude, locality, posted_at)
  VALUES (gen_random_uuid(), p_author_id, post_window.id, p_image_id, p_caption, p_visibility,
          p_location_kind, p_latitude, p_longitude, p_locality, v_posted_at)
  RETURNING * INTO new_post;

  RETURN NEXT;
END;
$$;

-- canComment と同じ判定をペアのロックの下で行い、通れば書く。不可なら行を返さない。
-- 作者本人以外は、期間中・本日投稿済み・ブロックなしのうえで public か友達。友達の友達は対象外なので集計しない。
CREATE OR REPLACE FUNCTION create_comment(
  p_actor text,
  p_post_id text,
  p_body text,
  OUT new_comment comments,
  OUT author users
) RETURNS SETOF record LANGUAGE plpgsql AS $$
DECLARE
  v_post_author text;
  v_visibility text;
BEGIN
  SELECT author_id INTO v_post_author FROM posts WHERE id = p_post_id AND deleted_at IS NULL;
  IF NOT FOUND THEN
    RETURN;
  END IF;

  PERFORM pg_advisory_xact_lock(user_pair_advisory_key(p_actor, v_post_author));
  PERFORM 1 FROM users WHERE id IN (p_actor, v_post_author) AND status = 'active' ORDER BY id FOR KEY SHARE;

  SELECT p.visibility INTO v_visibility
  FROM posts p
  JOIN posting_windows w ON w.id = p.posting_window_id AND w.ended_at IS NULL
  JOIN users u ON u.id = p.author_id AND u.status = 'active'
  WHERE p.id = p_post_id AND p.deleted_at IS NULL;
  IF NOT FOUND THEN
    RETURN;
  END IF;

  SELECT * INTO author FROM users WHERE id = p_actor AND status = 'active';
  IF NOT FOUND THEN
    RETURN;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM posts p
    JOIN posting_windows posted ON posted.id = p.posting_window_id
    JOIN posting_windows cur ON cur.region = author.region AND cur.ended_at IS NULL AND cur.local_date = posted.local_date
    WHERE p.author_id = p_actor AND p.deleted_at IS NULL
  ) THEN
    RETURN;
  END IF;

  IF p_actor <> v_post_author THEN
    IF EXISTS (
      SELECT 1 FROM block_relations
      WHERE (blocker_id = p_actor AND blocked_id = v_post_author) OR (blocker_id = v_post_author AND blocked_id = p_actor)
    ) THEN
      RETURN;
    END IF;
    IF v_visibility <> 'public' AND NOT (v_visibility IN ('friends', 'friends_of_friends') AND EXISTS (
      SELECT 1 FROM friendships
      WHERE (user_low_id = p_actor AND user_high_id = v_post_author) OR (user_low_id = v_post_author AND user_high_id = p_actor)
    )) THEN
      RETURN;
    END IF;
  END IF;

  INSERT INTO comments (id, post_id, author_id, body, created_at)
  VALUES (gen_random_uuid(), p_post_id, p_actor, p_body, clock_timestamp())
  RETURNING * INTO new_comment;

  RETURN NEXT;
END;
$$;

-- Go の canViewPost と同じ判定。投稿か作者か閲覧者が無い・active でなければ false。
CREATE OR REPLACE FUNCTION can_view_post(p_viewer text, p_post_id text) RETURNS boolean LANGUAGE sql STABLE AS $$
  SELECT EXISTS (
    SELECT 1
    FROM posts p
    JOIN posting_windows w ON w.id = p.posting_window_id
    JOIN users a ON a.id = p.author_id AND a.status = 'active'
    JOIN users v ON v.id = p_viewer AND v.status = 'active'
    WHERE p.id = p_post_id AND p.deleted_at IS NULL
      AND (p.author_id = p_viewer OR (
        w.ended_at IS NULL
        AND EXISTS (
          SELECT 1 FROM posts vp
          JOIN posting_windows posted ON posted.id = vp.posting_window_id
          JOIN posting_windows cur ON cur.region = v.region AND cur.ended_at IS NULL AND cur.local_date = posted.local_date
          WHERE vp.author_id = p_viewer AND vp.deleted_at IS NULL)
        AND NOT EXISTS (
          SELECT 1 FROM block_relations b
          WHERE (b.blocker_id = p_viewer AND b.blocked_id = p.author_id) OR (b.blocker_id = p.author_id AND b.blocked_id = p_viewer))
        AND (
          p.visibility = 'public'
          OR (p.visibility IN ('friends', 'friends_of_friends') AND EXISTS (
            SELECT 1 FROM friendships f
            WHERE (f.user_low_id = p_viewer AND f.user_high_id = p.author_id) OR (f.user_low_id = p.author_id AND f.user_high_id = p_viewer)))
          OR (p.visibility = 'friends_of_friends' AND EXISTS (
            SELECT 1 FROM (
              SELECT user_high_id AS id FROM friendships WHERE user_low_id = p_viewer
              UNION ALL
              SELECT user_low_id FROM friendships WHERE user_high_id = p_viewer
            ) vf
            JOIN friendships f2 ON (f2.user_low_id = vf.id AND f2.user_high_id = p.author_id) OR (f2.user_low_id = p.author_id AND f2.user_high_id = vf.id)))
        )
      ))
  );
$$;

-- RealMoji の送信を 1 文で行う。失敗は RAISE の message（interaction_forbidden / relationship_not_found）で返す。
-- lockUserPair と同じく二人の advisory lock の下で、ロックを待ったあとの文で関係と閲覧可否を確かめる。
-- 画像のバイト列は DB に書かず、p_image_origin のサーバのローカルにある。
CREATE OR REPLACE FUNCTION replace_realmoji(
  p_actor text,
  p_post_id text,
  p_image_id text,
  p_image_origin text,
  p_reaction_type text,
  OUT reaction realmojis,
  OUT reactor users
) RETURNS SETOF record LANGUAGE plpgsql AS $$
DECLARE
  v_author_id text;
  v_active_users integer;
BEGIN
  SELECT author_id INTO v_author_id FROM posts WHERE id = p_post_id AND deleted_at IS NULL;
  IF NOT FOUND OR v_author_id = p_actor THEN
    RAISE EXCEPTION 'interaction_forbidden';
  END IF;

  PERFORM pg_advisory_xact_lock(user_pair_advisory_key(p_actor, v_author_id));

  SELECT count(*) INTO v_active_users FROM (
    SELECT 1 FROM users
    WHERE id IN (p_actor, v_author_id) AND status = 'active'
    ORDER BY id
    FOR KEY SHARE
  ) AS locked;
  IF v_active_users <> 2 THEN
    RAISE EXCEPTION 'relationship_not_found';
  END IF;

  IF NOT can_view_post(p_actor, p_post_id) THEN
    RAISE EXCEPTION 'interaction_forbidden';
  END IF;

  UPDATE realmojis SET replaced_at = clock_timestamp()
  WHERE post_id = p_post_id AND reactor_id = p_actor AND replaced_at IS NULL AND deleted_at IS NULL;
  INSERT INTO image_objects (id, owner_user_id, data, media_type, origin_region)
  VALUES (p_image_id, p_actor, '', 'image/jpeg', p_image_origin);
  INSERT INTO image_meta (id, owner_user_id, media_type, origin_region)
  VALUES (p_image_id, p_actor, 'image/jpeg', p_image_origin);
  INSERT INTO realmojis (id, post_id, reactor_id, image_id, reaction_type, created_at)
  VALUES (gen_random_uuid(), p_post_id, p_actor, p_image_id, p_reaction_type, clock_timestamp())
  RETURNING * INTO reaction;
  SELECT * INTO STRICT reactor FROM users WHERE id = p_actor;

  RETURN NEXT;
END;
$$;

COMMIT;
