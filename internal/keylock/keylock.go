// Package keylock は、名前（鍵）ごとの錠です。
//
// 同じ名前の仕事を1つずつ通し、違う名前の仕事は並べて通します。使っている人がいなくなった名前の錠は捨てるので、
// でたらめの名前（ログインの名前・届いたメールの Message-ID など）で増え続けません。
// 通信の取り込み（同じ印の記録を2枚作らない）・下書きの送信・ログインの名前が、同じ形を写していたのを寄せました（2026-10-29）。
//
// ⚠ 錠は再入できません（同じ名前の錠を、取ったまま取り直すと止まる）。
package keylock

import "sync"

// Set は名前ごとの錠の集まりです。ゼロの値のまま使えます。
type Set struct {
	mu sync.Mutex
	m  map[string]*entry
}

type entry struct {
	sync.Mutex
	users int // 取っている人と、取るのを待っている人の数
}

// Lock は名前 key の錠を取り、外す関数を返します（defer s.Lock(key)() の形で使う）。
// ⚠ 外す関数は1回だけ呼ぶこと（2回呼ぶと sync.Mutex の二重解放で、プロセスごと落ちる）。
func (s *Set) Lock(key string) func() {
	s.mu.Lock()
	if s.m == nil {
		s.m = map[string]*entry{}
	}
	l := s.m[key]
	if l == nil {
		l = &entry{}
		s.m[key] = l
	}
	l.users++
	s.mu.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		s.mu.Lock()
		if l.users--; l.users == 0 {
			delete(s.m, key)
		}
		s.mu.Unlock()
	}
}

// size は、いま持っている名前の数です（試験が、捨てたことを確かめる）。
func (s *Set) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
