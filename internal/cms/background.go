package cms

// 裏で動き続ける仕事の登録（2026-10-21 夜）。
//
// 拡張が、サーバーが起きたときに始める仕事（決まった間隔の見張りなど）を登録します。コアは中身を知らず、main が1度
// StartBackgroundTasks を呼んで、登録された順に始めるだけです。試験（go test）は main を通らないので、裏の仕事は動きません。
//
// 使い手の例: メールの拡張の「決まった間隔で新しいメールを読む」（ext/comm/mail/auto_fetch.go）。

import "log"

// backgroundTask は登録された仕事1つです。
type backgroundTask struct {
	name  string
	start func()
}

// backgroundTasks は登録された仕事です。**`init()` の中からだけ**登録します（StartBackgroundTasks より前に揃っている前提）。
var backgroundTasks []backgroundTask

// RegisterBackgroundTask は、サーバーが起きたときに始める仕事を登録します（拡張の `init()` から呼ぶ）。
// start は、裏の仕事を goroutine で始めてすぐ戻ること（待ち受けを止めない）。
func RegisterBackgroundTask(name string, start func()) {
	backgroundTasks = append(backgroundTasks, backgroundTask{name: name, start: start})
}

// StartBackgroundTasks は登録された仕事を順に始めます（main が1度呼ぶ）。1つが落ちても、ほかは始めます。
func StartBackgroundTasks() {
	for _, t := range backgroundTasks {
		func() {
			defer func() {
				if p := recover(); p != nil {
					log.Printf("裏の仕事 %s を始められません: %v", t.name, p)
				}
			}()
			t.start()
			log.Printf("裏の仕事を始めました: %s", t.name)
		}()
	}
}
