package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func Open(ctx context.Context, url string) (*redis.Client, error) {
	_ = ctx
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis config: %w", err)
	}
	c := redis.NewClient(opts)
	return c, nil
}
