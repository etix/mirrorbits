// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package mirrors

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/network"
	. "github.com/etix/mirrorbits/testing"
	"github.com/garyburd/redigo/redis"
	"github.com/rafaeljusto/redigomock"
)

func generateSimpleMirrorList(number int) Mirrors {
	ret := Mirrors{}
	for i := 0; i < number; i++ {
		m := Mirror{
			ID: fmt.Sprintf("M%d", i),
		}
		ret = append(ret, m)
	}
	return ret
}

func formatMirrorOrder(mirrors Mirrors) string {
	buf := ""
	for _, m := range mirrors {
		buf += fmt.Sprintf("%s, ", m.ID)
	}
	return strings.TrimSuffix(buf, ", ")
}

func matchingMirrorOrder(m Mirrors, order []string) bool {
	if len(m) != len(order) {
		return false
	}

	for i, v := range order {
		if v != m[i].ID {
			return false
		}
	}

	return true
}

func TestMirrors_Len(t *testing.T) {
	m := Mirrors{}
	if m.Len() != 0 {
		t.Fatalf("Expected 0, got %d", m.Len())
	}

	m = generateSimpleMirrorList(2)
	if m.Len() != len(m) {
		t.Fatalf("Expected %d, got %d", len(m), m.Len())
	}
}

func TestMirrors_Swap(t *testing.T) {
	m := generateSimpleMirrorList(5)

	if !matchingMirrorOrder(m, []string{"M0", "M1", "M2", "M3", "M4"}) {
		t.Fatalf("Expected M0 before M1, got %s", formatMirrorOrder(m))
	}

	m.Swap(0, 1)

	if !matchingMirrorOrder(m, []string{"M1", "M0", "M2", "M3", "M4"}) {
		t.Fatalf("Expected M1 before M0, got %s", formatMirrorOrder(m))
	}

	m.Swap(2, 4)

	if !matchingMirrorOrder(m, []string{"M1", "M0", "M4", "M3", "M2"}) {
		t.Fatal("Expected M4 at position 2 and M2 at position 4", m)
	}
}

func TestByRank_Less(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	/* */

	c := network.GeoIPRecord{}
	if c.IsValid() {
		t.Fatalf("GeoIPRecord is supposed to be invalid")
	}

	/* */

	// Generate two identical slices
	m1 := generateSimpleMirrorList(50)
	m2 := generateSimpleMirrorList(50)

	// Mirrors are identical (besides name) so ByRank is expected
	// to randomize their order.
	sort.Sort(ByRank{m1, c})

	differences := 0
	for i, m := range m1 {
		if m.ID != m2[i].ID {
			differences++
		}
	}

	if differences == 0 {
		t.Fatalf("Result is supposed to be randomized")
	} else if differences < 10 {
		t.Fatalf("Too many similarities, something's wrong?")
	}

	// Sort again, just to be sure the result is different
	m3 := generateSimpleMirrorList(50)
	sort.Sort(ByRank{m3, c})

	differences = 0
	for i, m := range m3 {
		if m.ID != m1[i].ID {
			differences++
		}
	}

	if differences == 0 {
		t.Fatalf("Result is supposed to be different from previous run")
	} else if differences < 10 {
		t.Fatalf("Too many similarities, something's wrong?")
	}

	/* */

	c = network.GeoIPRecord{
		CountryCode:   "FR",
		ContinentCode: "EU",
		ASNum:         4444,
	}
	if !c.IsValid() {
		t.Fatalf("GeoIPRecord is supposed to be valid")
	}

	/* asnum */

	m := Mirrors{
		Mirror{
			ID:    "M0",
			Asnum: 6666,
		},
		Mirror{
			ID:    "M1",
			Asnum: 5555,
		},
		Mirror{
			ID:    "M2",
			Asnum: 4444,
		},
		Mirror{
			ID:    "M3",
			Asnum: 6666,
		},
	}

	sort.Sort(ByRank{m, c})

	if !matchingMirrorOrder(m, []string{"M2", "M0", "M1", "M3"}) {
		t.Fatalf("Order doesn't seem right: %s, expected M2, M0, M1, M3", formatMirrorOrder(m))
	}

	/* distance */

	m = Mirrors{
		Mirror{
			ID:       "M0",
			Distance: 1000.0,
		},
		Mirror{
			ID:       "M1",
			Distance: 999.0,
		},
		Mirror{
			ID:       "M2",
			Distance: 1000.0,
		},
		Mirror{
			ID:       "M3",
			Distance: 888.0,
		},
	}

	sort.Sort(ByRank{m, c})

	if !matchingMirrorOrder(m, []string{"M3", "M1", "M0", "M2"}) {
		t.Fatalf("Order doesn't seem right: %s, expected M3, M1, M0, M2", formatMirrorOrder(m))
	}

	/* countrycode */

	m = Mirrors{
		Mirror{
			ID:            "M0",
			CountryFields: []string{"IT", "UK"},
		},
		Mirror{
			ID:            "M1",
			CountryFields: []string{"IT", "UK"},
		},
		Mirror{
			ID:            "M2",
			CountryFields: []string{"IT", "FR"},
		},
		Mirror{
			ID:            "M3",
			CountryFields: []string{"FR", "UK"},
		},
	}

	sort.Sort(ByRank{m, c})

	if !matchingMirrorOrder(m, []string{"M2", "M3", "M0", "M1"}) {
		t.Fatalf("Order doesn't seem right: %s, expected M2, M3, M0, M1", formatMirrorOrder(m))
	}

	/* continentcode */

	c = network.GeoIPRecord{
		ContinentCode: "EU",
		ASNum:         4444,
		CountryCode:   "XX",
	}

	m = Mirrors{
		Mirror{
			ID:            "M0",
			ContinentCode: "NA",
		},
		Mirror{
			ID:            "M1",
			ContinentCode: "NA",
		},
		Mirror{
			ID:            "M2",
			ContinentCode: "EU",
		},
		Mirror{
			ID:            "M3",
			ContinentCode: "NA",
		},
	}

	sort.Sort(ByRank{m, c})

	if !matchingMirrorOrder(m, []string{"M2", "M0", "M1", "M3"}) {
		t.Fatalf("Order doesn't seem right: %s, expected M2, M0, M1, M3", formatMirrorOrder(m))
	}

	/* */

	c = network.GeoIPRecord{
		CountryCode:   "FR",
		ContinentCode: "EU",
		ASNum:         4444,
	}

	m = Mirrors{
		Mirror{
			ID:            "M0",
			Distance:      100.0,
			CountryFields: []string{"IT", "FR"},
			ContinentCode: "EU",
		},
		Mirror{
			ID:            "M1",
			Distance:      200.0,
			CountryFields: []string{"FR", "CH"},
			ContinentCode: "EU",
		},
		Mirror{
			ID:            "M2",
			Distance:      1000.0,
			CountryFields: []string{"UK", "DE"},
			Asnum:         4444,
		},
	}

	sort.Sort(ByRank{m, c})

	if !matchingMirrorOrder(m, []string{"M2", "M0", "M1"}) {
		t.Fatalf("Order doesn't seem right: %s, expected M2, M0, M1", formatMirrorOrder(m))
	}
}

func TestByComputedScore_Less(t *testing.T) {
	m := Mirrors{
		Mirror{
			ID:            "M0",
			ComputedScore: 50,
		},
		Mirror{
			ID:            "M1",
			ComputedScore: 0,
		},
		Mirror{
			ID:            "M2",
			ComputedScore: 2500,
		},
		Mirror{
			ID:            "M3",
			ComputedScore: 21,
		},
	}

	sort.Sort(ByComputedScore{m})

	if !matchingMirrorOrder(m, []string{"M2", "M0", "M3", "M1"}) {
		t.Fatalf("Order doesn't seem right: %s, expected M2, M0, M3, M1", formatMirrorOrder(m))
	}
}

func TestByExcludeReason_Less(t *testing.T) {
	m := Mirrors{
		Mirror{
			ID:            "M0",
			ExcludeReason: "x42",
		},
		Mirror{
			ID:            "M1",
			ExcludeReason: "x43",
		},
		Mirror{
			ID:            "M2",
			ExcludeReason: "Test one",
		},
		Mirror{
			ID:            "M3",
			ExcludeReason: "Test two",
		},
		Mirror{
			ID:            "M4",
			ExcludeReason: "test three",
		},
	}

	sort.Sort(ByExcludeReason{m})

	if !matchingMirrorOrder(m, []string{"M2", "M3", "M4", "M0", "M1"}) {
		t.Fatalf("Order doesn't seem right: %s, expected M2, M3, M4, M0, M1", formatMirrorOrder(m))
	}
}

func TestEnableMirror(t *testing.T) {
	mock, conn := PrepareRedisTest()

	cmdEnable := mock.Command("HMSET", "MIRROR_m1", "enabled", true).Expect("ok")
	EnableMirror(conn, "m1")

	if mock.Stats(cmdEnable) != 1 {
		t.Fatalf("Mirror not enabled")
	}

	mock.Command("HMSET", "MIRROR_m1", "enabled", true).ExpectError(redis.Error("blah"))
	if EnableMirror(conn, "m1") == nil {
		t.Fatalf("Error expected")
	}
}

func TestDisableMirror(t *testing.T) {
	mock, conn := PrepareRedisTest()

	cmdDisable := mock.Command("HMSET", "MIRROR_m1", "enabled", false).Expect("ok")
	DisableMirror(conn, "m1")

	if mock.Stats(cmdDisable) != 1 {
		t.Fatalf("Mirror not enabled")
	}

	mock.Command("HMSET", "MIRROR_m1", "enabled", false).ExpectError(redis.Error("blah"))
	if DisableMirror(conn, "m1") == nil {
		t.Fatalf("Error expected")
	}
}

func TestSetMirrorEnabled(t *testing.T) {
	mock, conn := PrepareRedisTest()

	cmdPublish := mock.Command("PUBLISH", string(database.MIRROR_UPDATE), redigomock.NewAnyData()).Expect("ok")

	cmdEnable := mock.Command("HMSET", "MIRROR_m1", "enabled", true).Expect("ok")
	SetMirrorEnabled(conn, "m1", true)

	if mock.Stats(cmdEnable) < 1 {
		t.Fatalf("Mirror not enabled")
	} else if mock.Stats(cmdEnable) > 1 {
		t.Fatalf("Mirror enabled more than once")
	}

	if mock.Stats(cmdPublish) < 1 {
		t.Fatalf("Event MIRROR_UPDATE not published")
	}

	mock.Command("HMSET", "MIRROR_m1", "enabled", true).ExpectError(redis.Error("blah"))
	if SetMirrorEnabled(conn, "m1", true) == nil {
		t.Fatalf("Error expected")
	}

	cmdDisable := mock.Command("HMSET", "MIRROR_m1", "enabled", false).Expect("ok")
	SetMirrorEnabled(conn, "m1", false)

	if mock.Stats(cmdDisable) != 1 {
		t.Fatalf("Mirror not disabled")
	} else if mock.Stats(cmdDisable) > 1 {
		t.Fatalf("Mirror disabled more than once")
	}

	if mock.Stats(cmdPublish) < 2 {
		t.Fatalf("Event MIRROR_UPDATE not published")
	}

	mock.Command("HMSET", "MIRROR_m1", "enabled", false).ExpectError(redis.Error("blah"))
	if SetMirrorEnabled(conn, "m1", false) == nil {
		t.Fatalf("Error expected")
	}
}

func TestMarkMirrorUp(t *testing.T) {
	_, conn := PrepareRedisTest()

	if err := MarkMirrorUp(conn, "m1"); err == nil {
		t.Fatalf("Error expected but nil returned")
	}
}

func TestMarkMirrorDown(t *testing.T) {
	_, conn := PrepareRedisTest()

	if err := MarkMirrorDown(conn, "m1", "test1"); err == nil {
		t.Fatalf("Error expected but nil returned")
	}
}

func TestSetMirrorState(t *testing.T) {
	mock, conn := PrepareRedisTest()

	if err := SetMirrorState(conn, "m1", true, "test1"); err == nil {
		t.Fatalf("Error expected but nil returned")
	}

	cmdPublish := mock.Command("PUBLISH", string(database.MIRROR_UPDATE), redigomock.NewAnyData()).Expect("ok")

	/* */

	cmdPreviousState := mock.Command("HGET", "MIRROR_m1", "up").Expect(int64(0)).Expect(int64(1))
	cmdStateSince := mock.Command("HMSET", "MIRROR_m1", "up", true, "excludeReason", "test1", "stateSince", redigomock.NewAnyInt()).Expect("ok")
	cmdState := mock.Command("HMSET", "MIRROR_m1", "up", true, "excludeReason", "test2").Expect("ok")

	if err := SetMirrorState(conn, "m1", true, "test1"); err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}

	if mock.Stats(cmdPreviousState) < 1 {
		t.Fatalf("Previous state not tested")
	}

	if mock.Stats(cmdStateSince) < 1 {
		t.Fatalf("New state not set")
	} else if mock.Stats(cmdStateSince) > 1 {
		t.Fatalf("State set more than once")
	}

	if mock.Stats(cmdPublish) < 1 {
		t.Fatalf("Event MIRROR_UPDATE not published")
	}

	/* */

	if err := SetMirrorState(conn, "m1", true, "test2"); err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}

	if mock.Stats(cmdStateSince) > 1 || mock.Stats(cmdState) < 1 {
		t.Fatalf("The value stateSince isn't supposed to be set")
	}

	if mock.Stats(cmdPublish) != 1 {
		t.Fatalf("Event MIRROR_UPDATE should not be sent")
	}

	/* */

	cmdPreviousState = mock.Command("HGET", "MIRROR_m1", "up").Expect(int64(1))
	cmdStateSince = mock.Command("HMSET", "MIRROR_m1", "up", false, "excludeReason", "test3", "stateSince", redigomock.NewAnyInt()).Expect("ok")

	if err := SetMirrorState(conn, "m1", false, "test3"); err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}

	if mock.Stats(cmdPreviousState) < 1 {
		t.Fatalf("Previous state not tested")
	}

	if mock.Stats(cmdStateSince) < 1 {
		t.Fatalf("New state not set")
	} else if mock.Stats(cmdStateSince) > 1 {
		t.Fatalf("State set more than once")
	}

	if mock.Stats(cmdPublish) < 2 {
		t.Fatalf("Event MIRROR_UPDATE not published")
	}
}
