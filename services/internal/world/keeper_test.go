package world

import (
	"testing"
	"time"
)

func TestClockSleepsWhenNobodyPlays(t *testing.T) {
	k := &Keeper{DayLength: time.Minute, IdleAfter: 50 * time.Millisecond}
	if k.Awake() {
		t.Fatal("a new village is asleep")
	}
	k.Touch()
	if !k.Awake() || k.NextDayIn() > time.Minute || k.NextDayIn() < 50*time.Second {
		t.Fatalf("touch should wake the village and start the day timer, next day in %s", k.NextDayIn())
	}
	time.Sleep(80 * time.Millisecond)
	if k.Awake() {
		t.Fatal("the village should fall asleep when idle")
	}
}

func TestFeedIsNewestFirstAndBounded(t *testing.T) {
	k := &Keeper{}
	for i := 0; i < 250; i++ {
		k.Record(FeedItem{Who: "x", Text: string(rune('a' + i%26))})
	}
	f := k.Feed(3)
	if len(f) != 3 || f[0].Text != string(rune('a'+249%26)) {
		t.Fatalf("feed: %+v", f)
	}
	if len(k.feed) != 200 {
		t.Fatalf("feed should keep 200 items, has %d", len(k.feed))
	}
}

func TestSeasonLabels(t *testing.T) {
	if SeasonLabel("barsaat") != "Barsaat, the monsoon" {
		t.Fatal(SeasonLabel("barsaat"))
	}
}
