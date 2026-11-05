package engine

import (
	"context"
	"testing"
	"time"
)

func TestWorkersIsolateAndSupersede(t *testing.T) {
	a, da := fixture(t)
	b, db := fixture(t)
	db.id = "second"
	b.state.ID = db.id
	entered := make(chan struct{})
	release := make(chan struct{})
	da.collect = func(ctx context.Context) (map[string]Traffic, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	m, e := NewManager(a, b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewManager(a, a); e == nil {
		t.Fatal("duplicate ID accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer func() { cancel(); m.Wait() }()
	one := m.Submit("sing-box", []byte("one"), Hash([]byte("one")))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	two := m.Submit("sing-box", []byte("two"), Hash([]byte("two")))
	three := m.Submit("sing-box", []byte("three"), Hash([]byte("three")))
	select {
	case <-two.Done():
		e := two.Err()
		if e == nil {
			t.Fatal("superseded reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("superseded not completed")
	}
	other := m.Submit("second", []byte("other"), Hash([]byte("other")))
	select {
	case <-other.Done():
		e := other.Err()
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("second engine blocked")
	}
	close(release)
	if e := one.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	if e := three.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	cancel()
}

func TestLatestCurrentRequestSupersedesPending(t *testing.T) {
	r, d := fixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	d.collect = func(ctx context.Context) (map[string]Traffic, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	m, e := NewManager(r)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); m.Wait() }()
	m.Start(ctx)
	a := m.Submit(d.ID(), []byte("A"), Hash([]byte("A")))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	b := m.Submit(d.ID(), []byte("B"), Hash([]byte("B")))
	again := m.Submit(d.ID(), []byte("A"), Hash([]byte("A")))
	if again != a {
		t.Fatal("duplicate did not share future")
	}
	select {
	case <-b.Done():
		if b.Err() == nil {
			t.Fatal("pending B succeeded")
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		t.Fatal("pending obsolete B was retained")
	}
	close(release)
	if e = a.Wait(ctx); e != nil {
		t.Fatal(e)
	}
}
