package cache

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
)

func NewRedisClient(redisURL string) (*redis.Client, error) {
	if redisURL == "" {
		log.Println("Redis URL is not provided")
		return nil, nil
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opt)

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	log.Println("Successfully connected to Redis")
	return client, nil
}
