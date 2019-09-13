// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package daemon

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/mirrors"
	. "github.com/etix/mirrorbits/testing"
)

func TestMain(m *testing.M) {
	SetConfiguration(&Configuration{
		RedisDB: 42,
	})
	os.Exit(m.Run())
}

func TestStart(t *testing.T) {
	_, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCluster(conn)
	c.Start()
	defer c.Stop()

	if c.running != true {
		t.Fatalf("Expected true, got false")
	}
}

func TestStop(t *testing.T) {
	_, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCluster(conn)
	c.Start()
	c.Stop()

	if c.running != false {
		t.Fatalf("Expected false, got true")
	}
}

func TestClusterLoop(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCluster(conn)

	cmdPublish := mock.Command("PUBLISH", string(database.CLUSTER), fmt.Sprintf("%s %s", c.announceText, c.nodeID)).Expect("1")

	c.Start()
	defer c.Stop()

	n := time.Now()

	for {
		if time.Since(n) > 1500*time.Millisecond {
			t.Fatalf("Announce not made")
		}
		if mock.Stats(cmdPublish) > 0 {
			// Success
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRefreshNodeList(t *testing.T) {
	_, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCluster(conn)

	n := node{
		ID:           "test-4242",
		LastAnnounce: time.Now().UTC().Unix(),
	}
	c.nodes = append(c.nodes, n)
	sort.Sort(byNodeID(c.nodes))

	n = node{
		ID:           "meh-4242",
		LastAnnounce: time.Now().UTC().Add(time.Second * -6).Unix(),
	}
	c.nodes = append(c.nodes, n)
	sort.Sort(byNodeID(c.nodes))

	c.Start()
	defer c.Stop()

	c.refreshNodeList("test-4242", "test-4242")

	if len(c.nodes) != 1 {
		t.Fatalf("Node meh-4242 should have left")
	}

	c.refreshNodeList("meh-4242", "test-4242")

	if len(c.nodes) != 2 {
		t.Fatalf("Node meh-4242 should have joined")
	}
}

func TestAddMirror(t *testing.T) {
	_, conn := PrepareRedisTest()

	c := NewCluster(conn)

	r := []int{2}
	c.AddMirror(&mirrors.Mirror{
		ID:   2,
		Name: "bbb",
	})
	if !reflect.DeepEqual(r, c.mirrorsIndex) {
		t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
	}

	r = []int{1, 2}
	c.AddMirror(&mirrors.Mirror{
		ID:   1,
		Name: "aaa",
	})
	if !reflect.DeepEqual(r, c.mirrorsIndex) {
		t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
	}

	r = []int{1, 2, 3}
	c.AddMirror(&mirrors.Mirror{
		ID:   3,
		Name: "ccc",
	})
	if !reflect.DeepEqual(r, c.mirrorsIndex) {
		t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
	}
}

func TestRemoveMirror(t *testing.T) {
	_, conn := PrepareRedisTest()

	c := NewCluster(conn)

	c.AddMirror(&mirrors.Mirror{
		ID:   1,
		Name: "aaa",
	})
	c.AddMirror(&mirrors.Mirror{
		ID:   2,
		Name: "bbb",
	})
	c.AddMirror(&mirrors.Mirror{
		ID:   3,
		Name: "ccc",
	})

	c.RemoveMirror(&mirrors.Mirror{ID: 4})
	r := []int{1, 2, 3}
	if !reflect.DeepEqual(r, c.mirrorsIndex) {
		t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
	}

	c.RemoveMirror(&mirrors.Mirror{ID: 1})
	r = []int{2, 3}
	if !reflect.DeepEqual(r, c.mirrorsIndex) {
		t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
	}

	c.RemoveMirror(&mirrors.Mirror{ID: 3})
	r = []int{2}
	if !reflect.DeepEqual(r, c.mirrorsIndex) {
		t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
	}
}

func TestIsHandled(t *testing.T) {
	_, conn := PrepareRedisTest()

	conn.ConnectPubsub()

	c := NewCluster(conn)
	c.Start()
	defer c.Stop()

	c.AddMirror(&mirrors.Mirror{
		ID:   1,
		Name: "aaa",
	})
	c.AddMirror(&mirrors.Mirror{
		ID:   2,
		Name: "bbb",
	})
	c.AddMirror(&mirrors.Mirror{
		ID:   3,
		Name: "ccc",
	})
	c.AddMirror(&mirrors.Mirror{
		ID:   4,
		Name: "ddd",
	})

	c.nodeTotal = 1

	if !c.IsHandled(1) || !c.IsHandled(2) || !c.IsHandled(3) || !c.IsHandled(4) {
		t.Fatalf("All mirrors should be handled")
	}

	c.nodeTotal = 2

	handled := 0

	if c.IsHandled(1) {
		handled++
	}
	if c.IsHandled(2) {
		handled++
	}
	if c.IsHandled(3) {
		handled++
	}
	if c.IsHandled(4) {
		handled++
	}

	if handled != 2 {
		t.Fatalf("Expected 2, got %d", handled)
	}
}

func TestRemoveMirrorIDFromSlice(t *testing.T) {
	s1 := []int{1, 2, 3, 4, 5}
	r1 := []int{1, 2, 4, 5}
	r := removeMirrorIDFromSlice(s1, 3)
	if !reflect.DeepEqual(r1, r) {
		t.Fatalf("Expected %+v, got %+v", r1, r)
	}

	s2 := []int{1, 2, 3, 4, 5}
	r2 := []int{2, 3, 4, 5}
	r = removeMirrorIDFromSlice(s2, 1)
	if !reflect.DeepEqual(r2, r) {
		t.Fatalf("Expected %+v, got %+v", r2, r)
	}

	s3 := []int{1, 2, 3, 4, 5}
	r3 := []int{1, 2, 3, 4}
	r = removeMirrorIDFromSlice(s3, 5)
	if !reflect.DeepEqual(r3, r) {
		t.Fatalf("Expected %+v, got %+v", r3, r)
	}

	s4 := []int{1, 2, 3, 4, 5}
	r4 := []int{1, 2, 3, 4, 5}
	r = removeMirrorIDFromSlice(s4, 6)
	if !reflect.DeepEqual(r4, r) {
		t.Fatalf("Expected %+v, got %+v", r4, r)
	}
}

func TestAddMirrorIDToSlice(t *testing.T) {
	s1 := []int{1, 3}
	r1 := []int{1, 2, 3}
	r := addMirrorIDToSlice(s1, 2)
	if !reflect.DeepEqual(r1, r) {
		t.Fatalf("Expected %+v, got %+v", r1, r)
	}

	s2 := []int{2, 3, 4}
	r2 := []int{1, 2, 3, 4}
	r = addMirrorIDToSlice(s2, 1)
	if !reflect.DeepEqual(r2, r) {
		t.Fatalf("Expected %+v, got %+v", r2, r)
	}

	s3 := []int{1, 2, 3}
	r3 := []int{1, 2, 3, 4}
	r = addMirrorIDToSlice(s3, 4)
	if !reflect.DeepEqual(r3, r) {
		t.Fatalf("Expected %+v, got %+v", r3, r)
	}
}
