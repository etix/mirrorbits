// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package database

import (
	"github.com/garyburd/redigo/redis"
	"github.com/op/go-logging"
	"sync"
	"time"
)

var (
	log = logging.MustGetLogger("main")
)

type PubsubEvent string

const (
	CLUSTER            PubsubEvent = "_mirrorbits_cluster"
	FILE_UPDATE        PubsubEvent = "_mirrorbits_file_update"
	MIRROR_UPDATE      PubsubEvent = "_mirrorbits_mirror_update"
	MIRROR_FILE_UPDATE PubsubEvent = "_mirrorbits_mirror_file_update"

	PUBSUB_RECONNECTED PubsubEvent = "_mirrorbits_pubsub_reconnected"
)

type Pubsub struct {
	r                  *Redisobj
	extSubscribers     map[string][]chan string
	extSubscribersLock sync.RWMutex
}

func NewPubsub(r *Redisobj) *Pubsub {
	pubsub := new(Pubsub)
	pubsub.r = r
	pubsub.extSubscribers = make(map[string][]chan string)
	go pubsub.updateEvents()
	return pubsub
}

// SubscribeEvent allows subscription to a particular kind of events and receive a
// notification when an event is dispatched on the given channel.
func (p *Pubsub) SubscribeEvent(event PubsubEvent, channel chan string) {
	p.extSubscribersLock.Lock()
	defer p.extSubscribersLock.Unlock()

	listeners := p.extSubscribers[string(event)]
	listeners = append(listeners, channel)
	p.extSubscribers[string(event)] = listeners
}

func (p *Pubsub) updateEvents() {
	var disconnected bool = false
connect:
	for {
		rconn := p.r.Get()
		if _, err := rconn.Do("PING"); err != nil {
			disconnected = true
			rconn.Close()
			if RedisIsLoading(err) {
				// Doing a PING after (re-connection) prevents cases where redis
				// is currently loading the dataset and is still not ready.
				log.Warning("Redis is still loading the dataset in memory")
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		log.Info("Subscribing pubsub")
		psc := redis.PubSubConn{Conn: rconn}

		psc.Subscribe(CLUSTER)
		psc.Subscribe(FILE_UPDATE)
		psc.Subscribe(MIRROR_UPDATE)
		psc.Subscribe(MIRROR_FILE_UPDATE)

		if disconnected == true {
			// This is a way to keep the cache active while disconnected
			// from redis but still clear the cache (possibly outdated)
			// after a successful reconnection.
			disconnected = false
			p.handleMessage(string(PUBSUB_RECONNECTED), nil)
		}
		for {
			switch v := psc.Receive().(type) {
			case redis.Message:
				//log.Debug("Redis message on channel %s: message: %s", v.Channel, v.Data)
				p.handleMessage(v.Channel, v.Data)
			case redis.Subscription:
				log.Debug("Redis subscription on channel %s: %s (%d)", v.Channel, v.Kind, v.Count)
			case error:
				log.Error("Pubsub disconnected: %s", v)
				psc.Close()
				rconn.Close()
				time.Sleep(50 * time.Millisecond)
				disconnected = true
				goto connect
			}
		}
	}
}

// Notify subscribers of the new message
func (p *Pubsub) handleMessage(channel string, data []byte) {
	p.extSubscribersLock.RLock()
	defer p.extSubscribersLock.RUnlock()

	listeners := p.extSubscribers[channel]
	for _, listener := range listeners {
		select {
		case listener <- string(data):
		default:
			// Don't block if the listener is not available
			// and drop the message.
		}
	}
}

func Publish(r redis.Conn, event PubsubEvent, message string) error {
	_, err := r.Do("PUBLISH", string(event), message)
	return err
}

func SendPublish(r redis.Conn, event PubsubEvent, message string) error {
	err := r.Send("PUBLISH", string(event), message)
	return err
}
