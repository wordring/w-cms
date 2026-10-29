package keylock

import (
	"sync"
	"testing"
	"time"
)

// TestLockSerializesSameKeyAndForgetsIt は、同じ名前は1つずつ・違う名前は並べて通し、使い終わった名前を捨てることを固定します。
func TestLockSerializesSameKeyAndForgetsIt(t *testing.T) {
	var s Set
	unlock := s.Lock("a")
	got := make(chan string, 2)
	go func() { u := s.Lock("a"); got <- "a2"; u() }()
	go func() { u := s.Lock("b"); got <- "b"; u() }()
	select {
	case v := <-got:
		if v != "b" {
			t.Fatalf("同じ名前の2つ目が先に通りました: %s", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("違う名前が通りません")
	}
	select {
	case v := <-got:
		t.Fatalf("錠を外す前に同じ名前が通りました: %s", v)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("錠を外しても同じ名前が通りません")
	}
	// 外し終えたら名前は残らない。
	deadline := time.Now().Add(2 * time.Second)
	for s.size() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := s.size(); n != 0 {
		t.Errorf("使い終わった名前が %d 残っています", n)
	}
	var wg sync.WaitGroup
	n := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); defer s.Lock("c")(); n++ }()
	}
	wg.Wait()
	if n != 50 || s.size() != 0 {
		t.Errorf("同じ名前の仕事が重なりました: %d・残り %d", n, s.size())
	}
}
