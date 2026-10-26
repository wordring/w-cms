package auth

// 業務サーバーの中だけで開く入口（2026-10-26）。
//
// w-cms は、ふだんの待ち受け（:8443 など）に加えて、業務サーバーの中のファイル（Unix ソケット・WCMS_LOCAL_SOCKET）でも
// 待ち受けられます（cmd/w-cms/local_socket.go）。そのファイルを開けるのは、w-cms を動かしている利用者と root だけです。
// この入口から来た要求は、決めた1人の w-cms の利用者（WCMS_LOCAL_SOCKET_USER）として扱います。
//
//   - パスワードもセッションも使わない。だれが通れるかは、ファイルを開けるか（業務サーバーへ SSH の鍵で入り、sudo できるか）で決まる。
//   - 利用者は要求ごとに読み直す。無効にした利用者なら、ログインしていないのと同じ（401）。権限はその利用者の権限のまま。
//   - 監査（だれが何をしたか）には、その利用者の名前が残る。
//   - ブラウザはこのファイルを開けないので、Origin の確かめ（CSRF）はしない。届いた Cookie は捨てる（別の人として通らない）。
//
// > 2026-10-26 利用者:「ではClaudeが入るためのアカウントを作りましょう」「この変更を許します。」

import (
	"context"
	"net/http"
)

type localSocketKey struct{}

// LocalSocket は、業務サーバーの中の入口から来た要求を包みます。要求は利用者 username として扱います。
func LocalSocket(next http.Handler, username string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Cookie")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localSocketKey{}, username)))
	})
}

// localSocketUser は、要求が業務サーバーの中の入口から来たなら、その利用者の名前を返します。
func localSocketUser(r *http.Request) (string, bool) {
	name, ok := r.Context().Value(localSocketKey{}).(string)
	return name, ok && name != ""
}
