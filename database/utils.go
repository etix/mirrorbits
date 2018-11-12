package database

import (
	"errors"
	"strconv"

	"github.com/gomodule/redigo/redis"
)

func (r *Redis) GetListOfMirrors() (map[int]string, error) {
	conn, err := r.Connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	values, err := redis.Values(conn.Do("HGETALL", "MIRRORS"))
	if err != nil {
		return nil, err
	}

	mirrors := make(map[int]string, len(values)/2)

	// Convert the mirror id to int
	for i := 0; i < len(values); i += 2 {
		key, okKey := values[i].([]byte)
		value, okValue := values[i+1].([]byte)
		if !okKey || !okValue {
			return nil, errors.New("invalid type for mirrors key")
		}
		id, err := strconv.Atoi(string(key))
		if err != nil {
			return nil, errors.New("invalid type for mirrors ID")
		}
		mirrors[id] = string(value)
	}

	return mirrors, nil
}
