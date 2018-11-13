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

type NetReadyError struct {
	error
}

func (n *NetReadyError) Timeout() bool   { return false }
func (n *NetReadyError) Temporary() bool { return true }

func NewNetTemporaryError() NetReadyError {
	return NetReadyError{
		error: errors.New("database not ready"),
	}
}

type NotReadyError struct{}

func (e *NotReadyError) Close() error {
	return NewNetTemporaryError()
}

func (e *NotReadyError) Err() error {
	return NewNetTemporaryError()
}

func (e *NotReadyError) Do(commandName string, args ...interface{}) (reply interface{}, err error) {
	return nil, NewNetTemporaryError()
}

func (e *NotReadyError) Send(commandName string, args ...interface{}) error {
	return NewNetTemporaryError()
}

func (e *NotReadyError) Flush() error {
	return NewNetTemporaryError()
}

func (e *NotReadyError) Receive() (reply interface{}, err error) {
	return nil, NewNetTemporaryError()
}
