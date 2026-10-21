package comm

// 記録が届いたことを知らせる口（2026-10-21 夜・docs/考察/【考察】通信箱の改善.md §7.5）。
//
// 電話に出たとき・FAX が届いたとき・自動の読み込みでメールが入ったときに、入口が NotifyArrival を呼びます。聞き手（スマホへの
// 知らせなど）は RegisterArrivalListener で登録します。聞き手は裏で呼ぶので、入口の応答を待たせません。

import (
	"log"
	"sync"
)

// Arrival は届いた記録1つです。
type Arrival struct {
	PageID  string // 記録のページ番号（6桁）
	Title   string // 記録の題
	Channel string // チャネル（電話・FAX・メール）
}

var arrivalListeners struct {
	sync.Mutex
	fns []func(Arrival)
}

// RegisterArrivalListener は、記録が届いたときに呼ぶ聞き手を登録します（拡張の init() から）。
func RegisterArrivalListener(fn func(Arrival)) {
	arrivalListeners.Lock()
	defer arrivalListeners.Unlock()
	arrivalListeners.fns = append(arrivalListeners.fns, fn)
}

// NotifyArrival は、記録が届いたことを聞き手へ裏で知らせます（聞き手が落ちても入口は止めない）。
func NotifyArrival(a Arrival) {
	arrivalListeners.Lock()
	fns := append([]func(Arrival){}, arrivalListeners.fns...)
	arrivalListeners.Unlock()
	for _, fn := range fns {
		go func(fn func(Arrival)) {
			defer func() {
				if p := recover(); p != nil {
					log.Printf("届いた通知の聞き手が落ちました: %v", p)
				}
			}()
			fn(a)
		}(fn)
	}
}
