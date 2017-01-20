#!/bin/sh

if [ -z "$REDIS_PORT_6379_TCP_ADDR" ] || [ -z "$REDIS_PORT_6379_TCP_PORT" ]
then
    echo "Missing link to Redis: use --link redis:redis"
    exit 1
fi

echo "RedisAddress: $REDIS_PORT_6379_TCP_ADDR:$REDIS_PORT_6379_TCP_PORT" >> /go/src/github.com/etix/mirrorbits/contrib/docker/mirrorbits.conf

