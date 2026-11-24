package redis

import (
	"errors"
	goredis "github.com/redis/go-redis/v9"
)

// Nil is the upstream sentinel returned when a requested key does not exist.
var Nil = goredis.Nil

var (
	ErrNotInitialized          = errors.New("redis: not initialized")
	ErrAlreadyExists           = errors.New("redis: instance already exists")
	ErrInstanceNotFound        = errors.New("redis: instance not found")
	ErrInvalidConfig           = errors.New("redis: invalid config")
	ErrManagerClosedDuringInit = errors.New("redis: manager closed while initialization was in progress")
)
