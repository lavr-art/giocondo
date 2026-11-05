package panelsync

import (
	"testing"

	"github.com/artemlavr/giocondo/internal/service/engine"
)

func TestTrafficBufferRepeatsUnackedBatch(t *testing.T) {
	var a TrafficBuffer

	if a.next() != nil {
		t.Fatal("empty buffer returned stats")
	}
	a.Add(map[string]engine.Traffic{"alice": {Up: 1, Down: 2}})
	a.Add(map[string]engine.Traffic{"alice": {Up: 10}, "bob": {Down: 5}})

	s1 := a.next()
	if s1 == nil || s1.Seq != 1 || len(s1.Users) != 2 || s1.Users[0].Name != "alice" || s1.Users[0].Up != 11 || s1.Users[0].Down != 2 {
		t.Fatalf("first batch %v", s1)
	}

	// Without an ack the same batch is sent again and new traffic waits.
	a.Add(map[string]engine.Traffic{"alice": {Up: 100}})
	if again := a.next(); again != s1 {
		t.Fatalf("unacked batch not repeated: %v", again)
	}
	a.ack(99) // a foreign seq clears nothing
	if a.next() != s1 {
		t.Fatal("wrong ack cleared batch")
	}
	a.ack(1)

	s2 := a.next()
	if s2 == nil || s2.Seq != 2 || len(s2.Users) != 1 || s2.Users[0].Up != 100 {
		t.Fatalf("second batch %v", s2)
	}
	a.ack(2)
	if a.next() != nil {
		t.Fatal("stats after everything was acked")
	}
}
