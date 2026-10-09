package session

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type redisStore struct {
	rdb *redis.Client
}

func Redis(rdb *redis.Client) Store { return &redisStore{rdb: rdb} }

// Link records both halves, rolling back the first write on failure.
func (s *redisStore) Link(ctx context.Context, accessJti, refreshJti, userID string, accessTTL, refreshTTL time.Duration) error {
	if accessJti == "" || refreshJti == "" {
		return errEmptySessionID
	}
	if err := s.rdb.Set(ctx, "session:"+accessJti, refreshJti, accessTTL).Err(); err != nil {
		return err
	}
	if err := s.rdb.Set(ctx, "refresh:"+refreshJti, accessJti, refreshTTL).Err(); err != nil {
		_ = s.rdb.Del(ctx, "session:"+accessJti).Err()
		return err
	}
	if userID != "" {
		if err := s.rdb.SAdd(ctx, "user:"+userID, accessJti).Err(); err != nil {
			_ = s.rdb.Del(ctx, "session:"+accessJti, "refresh:"+refreshJti).Err()
			return err
		}
		_ = s.rdb.Expire(ctx, "user:"+userID, refreshTTL).Err()
	}
	return nil
}

func (s *redisStore) AccessValid(ctx context.Context, accessJti string) (string, bool) {
	if accessJti == "" {
		return "", false
	}
	rjti, err := s.rdb.Get(ctx, "session:"+accessJti).Result()
	if err != nil || rjti == "" {
		return "", false
	}
	return rjti, true
}

func (s *redisStore) RefreshValid(ctx context.Context, refreshJti string) (string, bool) {
	if refreshJti == "" {
		return "", false
	}
	ajti, err := s.rdb.Get(ctx, "refresh:"+refreshJti).Result()
	if err != nil || ajti == "" {
		return "", false
	}
	return ajti, true
}

var consumeRefreshScript = redis.NewScript(`
local a = redis.call("GET", "refresh:" .. ARGV[1])
if not a then return nil end
redis.call("DEL", "refresh:" .. ARGV[1])
redis.call("DEL", "session:" .. a)
return a
`)

func ttlSeconds(d time.Duration) int64 {
	if s := int64(d / time.Second); s > 1 {
		return s
	}
	return 1
}

var replaceRefreshScript = redis.NewScript(`
local a = redis.call("GET", "refresh:" .. ARGV[1])
if not a then return nil end
redis.call("DEL", "refresh:" .. ARGV[1])
redis.call("DEL", "session:" .. a)
redis.call("SET", "session:" .. ARGV[2], ARGV[3], "EX", ARGV[4])
redis.call("SET", "refresh:" .. ARGV[3], ARGV[2], "EX", ARGV[5])
return a
`)

func (s *redisStore) ReplaceRefresh(ctx context.Context, oldRefreshJti, newAccessJti, newRefreshJti, userID string, accessTTL, refreshTTL time.Duration) (string, bool, error) {
	if oldRefreshJti == "" || newAccessJti == "" || newRefreshJti == "" {
		return "", false, errEmptySessionID
	}
	v, err := replaceRefreshScript.Run(ctx, s.rdb, nil,
		oldRefreshJti, newAccessJti, newRefreshJti, ttlSeconds(accessTTL), ttlSeconds(refreshTTL)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return "", false, err
	}
	ajti, ok := v.(string)
	if !ok || ajti == "" {
		return "", false, nil
	}
	if userID != "" {
		_ = s.rdb.SAdd(ctx, "user:"+userID, newAccessJti).Err()
		_ = s.rdb.Expire(ctx, "user:"+userID, refreshTTL).Err()
	}
	return ajti, true, nil
}

func (s *redisStore) ConsumeRefresh(ctx context.Context, refreshJti string) (string, bool, error) {
	if refreshJti == "" {
		return "", false, nil
	}
	v, err := consumeRefreshScript.Run(ctx, s.rdb, nil, refreshJti).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return "", false, err
	}
	ajti, ok := v.(string)
	if !ok || ajti == "" {
		return "", false, nil
	}
	return ajti, true, nil
}

func (s *redisStore) Unlink(ctx context.Context, accessJti, refreshJti string) error {
	keys := make([]string, 0, 2)
	if accessJti != "" {
		keys = append(keys, "session:"+accessJti)
	}
	if refreshJti != "" {
		keys = append(keys, "refresh:"+refreshJti)
	}
	if len(keys) == 0 {
		return nil
	}
	return s.rdb.Del(ctx, keys...).Err()
}

func (s *redisStore) RevokeUser(ctx context.Context, userID string) error {
	if userID == "" {
		return nil
	}
	members, err := s.rdb.SMembers(ctx, "user:"+userID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	}
	for _, accessJti := range members {
		rjti, _ := s.rdb.Get(ctx, "session:"+accessJti).Result()
		keys := []string{"session:" + accessJti}
		if rjti != "" {
			keys = append(keys, "refresh:"+rjti)
		}
		if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
			return err
		}
	}
	return s.rdb.Del(ctx, "user:"+userID).Err()
}
