package cms

import "w-cms/internal/auth"

// 新しい端末を登録したら、本人のスマホなどへ通知します（docs/考察/【考察】端末の登録.md §3.5・問い6）。
// 身に覚えのない通知が来たら、端末の一覧から取り消せます。送るのに時間がかかるので、裏で送ります。
func init() {
	auth.OnDeviceRegistered = func(username, name, ip string) {
		go func() {
			defer RecoverBackground("端末の登録の通知")
			SendPush(username, PushMessage{
				Title: "新しい端末を登録しました",
				Body:  name + "（心当たりが無ければ、端末の一覧から取り消してください）",
				URL:   "/assets/devices.html",
				Tag:   "device-registered",
			})
		}()
	}
}
