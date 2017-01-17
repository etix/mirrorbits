// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package daemon

import (
    "fmt"
    "github.com/etix/mirrorbits/database"
    "github.com/etix/mirrorbits/mirrors"
    . "github.com/etix/mirrorbits/testing"
    "reflect"
    "sort"
    "testing"
    "time"
)

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

    cmd_publish := mock.Command("PUBLISH", string(database.CLUSTER), fmt.Sprintf("%s %s", clusterAnnounce, c.nodeID)).Expect("1")

    c.Start()
    defer c.Stop()

    n := time.Now()

    for {
        if time.Since(n) > 1500*time.Millisecond {
            t.Fatalf("Announce not made")
        }
        if mock.Stats(cmd_publish) > 0 {
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

    n := Node{
        ID:           "test-4242",
        LastAnnounce: time.Now().UTC().Unix(),
    }
    c.nodes = append(c.nodes, n)
    sort.Sort(ByNodeID(c.nodes))

    n = Node{
        ID:           "meh-4242",
        LastAnnounce: time.Now().UTC().Add(time.Second * -6).Unix(),
    }
    c.nodes = append(c.nodes, n)
    sort.Sort(ByNodeID(c.nodes))

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

    r := []string{"bbb"}
    c.AddMirror(&mirrors.Mirror{
        ID: "bbb",
    })
    if !reflect.DeepEqual(r, c.mirrorsIndex) {
        t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
    }

    r = []string{"aaa", "bbb"}
    c.AddMirror(&mirrors.Mirror{
        ID: "aaa",
    })
    if !reflect.DeepEqual(r, c.mirrorsIndex) {
        t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
    }

    r = []string{"aaa", "bbb", "ccc"}
    c.AddMirror(&mirrors.Mirror{
        ID: "ccc",
    })
    if !reflect.DeepEqual(r, c.mirrorsIndex) {
        t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
    }
}

func TestRemoveMirror(t *testing.T) {
    _, conn := PrepareRedisTest()

    c := NewCluster(conn)

    c.AddMirror(&mirrors.Mirror{
        ID: "aaa",
    })
    c.AddMirror(&mirrors.Mirror{
        ID: "bbb",
    })
    c.AddMirror(&mirrors.Mirror{
        ID: "ccc",
    })

    c.RemoveMirror(&mirrors.Mirror{ID: "xxx"})
    r := []string{"aaa", "bbb", "ccc"}
    if !reflect.DeepEqual(r, c.mirrorsIndex) {
        t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
    }

    c.RemoveMirror(&mirrors.Mirror{ID: "aaa"})
    r = []string{"bbb", "ccc"}
    if !reflect.DeepEqual(r, c.mirrorsIndex) {
        t.Fatalf("Expected %+v, got %+v", r, c.mirrorsIndex)
    }

    c.RemoveMirror(&mirrors.Mirror{ID: "ccc"})
    r = []string{"bbb"}
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
        ID: "aaa",
    })
    c.AddMirror(&mirrors.Mirror{
        ID: "bbb",
    })
    c.AddMirror(&mirrors.Mirror{
        ID: "ccc",
    })
    c.AddMirror(&mirrors.Mirror{
        ID: "ddd",
    })

    c.nodeTotal = 1

    if !c.IsHandled("aaa") || !c.IsHandled("bbb") || !c.IsHandled("ccc") || !c.IsHandled("ddd") {
        t.Fatalf("All mirrors should be handled")
    }

    c.nodeTotal = 2

    handled := 0

    if c.IsHandled("aaa") {
        handled += 1
    }
    if c.IsHandled("bbb") {
        handled += 1
    }
    if c.IsHandled("ccc") {
        handled += 1
    }
    if c.IsHandled("ddd") {
        handled += 1
    }

    if handled != 2 {
        t.Fatalf("Expected 2, got %d", handled)
    }
}

func TestRemoveMirrorIDFromSlice(t *testing.T) {
    s1 := []string{"aaa", "bbb", "ccc", "ddd", "eee"}
    r1 := []string{"aaa", "bbb", "ddd", "eee"}
    r := removeMirrorIDFromSlice(s1, "ccc")
    if !reflect.DeepEqual(r1, r) {
        t.Fatalf("Expected %+v, got %+v", r1, r)
    }

    s2 := []string{"aaa", "bbb", "ccc", "ddd", "eee"}
    r2 := []string{"bbb", "ccc", "ddd", "eee"}
    r = removeMirrorIDFromSlice(s2, "aaa")
    if !reflect.DeepEqual(r2, r) {
        t.Fatalf("Expected %+v, got %+v", r2, r)
    }

    s3 := []string{"aaa", "bbb", "ccc", "ddd", "eee"}
    r3 := []string{"aaa", "bbb", "ccc", "ddd"}
    r = removeMirrorIDFromSlice(s3, "eee")
    if !reflect.DeepEqual(r3, r) {
        t.Fatalf("Expected %+v, got %+v", r3, r)
    }

    s4 := []string{"aaa", "bbb", "ccc", "ddd", "eee"}
    r4 := []string{"aaa", "bbb", "ccc", "ddd", "eee"}
    r = removeMirrorIDFromSlice(s4, "xxx")
    if !reflect.DeepEqual(r4, r) {
        t.Fatalf("Expected %+v, got %+v", r4, r)
    }
}

func TestAddMirrorIDToSlice(t *testing.T) {
    s1 := []string{"aaa", "ccc"}
    r1 := []string{"aaa", "bbb", "ccc"}
    r := addMirrorIDToSlice(s1, "bbb")
    if !reflect.DeepEqual(r1, r) {
        t.Fatalf("Expected %+v, got %+v", r1, r)
    }

    s2 := []string{"aaa", "bbb", "ccc"}
    r2 := []string{"111", "aaa", "bbb", "ccc"}
    r = addMirrorIDToSlice(s2, "111")
    if !reflect.DeepEqual(r2, r) {
        t.Fatalf("Expected %+v, got %+v", r2, r)
    }

    s3 := []string{"aaa", "bbb", "ccc"}
    r3 := []string{"aaa", "bbb", "ccc", "ddd"}
    r = addMirrorIDToSlice(s3, "ddd")
    if !reflect.DeepEqual(r3, r) {
        t.Fatalf("Expected %+v, got %+v", r3, r)
    }
}
