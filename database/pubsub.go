// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package database

import (
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/op/go-logging"
)

var (
	log = logging.MustGetLogger("main")
)

type pubsubEvent string

const (
	CLUSTER            pubsubEvent = "_mirrorbits_cluster"
	FILE_UPDATE        pubsubEvent = "_mirrorbits_file_update"
	MIRROR_UPDATE      pubsubEvent = "_mirrorbits_mirror_update"
	MIRROR_FILE_UPDATE pubsubEvent = "_mirrorbits_mirror_file_update"

	PUBSUB_RECONNECTED pubsubEvent = "_mirrorbits_pubsub_reconnected"
)

// Pubsub is the internal structure of the publish/subscribe handler
type Pubsub struct {
	r                  *Redis
	rconn              redis.Conn
	connlock           sync.Mutex
	extSubscribers     map[string][]chan string
	extSubscribersLock sync.RWMutex
	stop               chan bool
	wg                 sync.WaitGroup
}

// NewPubsub returns a new instance of the publish/subscribe handler
func NewPubsub(r *Redis) *Pubsub {
	pubsub := new(Pubsub)
	pubsub.r = r
	pubsub.stop = make(chan bool)
	pubsub.extSubscribers = make(map[string][]chan string)
	go pubsub.updateEvents()
	return pubsub
}

// Close all the connections to the pubsub server
func (p *Pubsub) Close() {
	close(p.stop)
	p.connlock.Lock()
	if p.rconn != nil {
		// FIXME Calling p.rconn.Close() here will block indefinitely in redigo
		p.rconn.Send("UNSUBSCRIBE")
		p.rconn.Send("QUIT")
		p.rconn.Flush()
	}
	p.connlock.Unlock()
	p.wg.Wait()
}

// SubscribeEvent allows subscription to a particular kind of events and receive a
// notification when an event is dispatched on the given channel.
func (p *Pubsub) SubscribeEvent(event pubsubEvent, channel chan string) {
	p.extSubscribersLock.Lock()
	defer p.extSubscribersLock.Unlock()

	listeners := p.extSubscribers[string(event)]
	listeners = append(listeners, channel)
	p.extSubscribers[string(event)] = listeners
}

func (p *Pubsub) updateEvents() {
	p.wg.Add(1)
	defer p.wg.Done()
	disconnected := false
connect:
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		p.connlock.Lock()
		p.rconn = p.r.Get()
		if _, err := p.rconn.Do("PING"); err != nil {
			disconnected = true
			p.rconn.Close()
			p.rconn = nil
			p.connlock.Unlock()
			if RedisIsLoading(err) {
				// Doing a PING after (re-connection) prevents cases where redis
				// is currently loading the dataset and is still not ready.
				log.Warning("Redis is still loading the dataset in memory")
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		p.connlock.Unlock()
		log.Debug("Subscribing pubsub")
		psc := redis.PubSubConn{Conn: p.rconn}

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
				//log.Debugf("Redis message on channel %s: message: %s", v.Channel, v.Data)
				p.handleMessage(v.Channel, v.Data)
			case redis.Subscription:
				log.Debugf("Redis subscription on channel %s: %s (%d)", v.Channel, v.Kind, v.Count)
			case error:
				select {
				case <-p.stop:
					return
				default:
				}
				log.Errorf("Pubsub disconnected: %s", v)
				psc.Close()
				p.rconn.Close()
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

// Publish a message on the pubsub server
func Publish(r redis.Conn, event pubsubEvent, message string) error {
	_, err := r.Do("PUBLISH", string(event), message)
	return err
}

// SendPublish add the message to a transaction
func SendPublish(r redis.Conn, event pubsubEvent, message string) error {
	err := r.Send("PUBLISH", string(event), message)
	return err
}
