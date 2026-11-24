package redis

import (
	"context"

	redis "github.com/redis/go-redis/v9"
)

func (c *Client) Publish(ctx context.Context, topic string, payload []byte) error {
	return c.rdb.Publish(ctx, c.Key("%s", topic), payload).Err()
}

func (c *Client) Subscribe(ctx context.Context, topic string) *redis.PubSub {
	return c.rdb.Subscribe(ctx, c.Key("%s", topic))
}
