package cms

// ─────────────────────────────────────────────────────────────────────────
// QRの配信——`GET /api/qr?page_id=010268` → そのページのURLのQR（SVG）
//
// ── URLはサーバーが組みます（クライアントに言わせません）──
//
// 「見えているURLをそのまま渡してもらう」ほうが正確に思えますが、**任意の文字列を
// QRにする口**になります。自社のドメインが、他所へ誘導するQRを刷る道具になるのは
// 割に合いません（QRは中身が目で読めないので、なおさら）。
//
// **`page_id` だけ受け取り、基底URLは `requestBaseURL` が決めます**
// ——canonical・sitemap と同じ関数なので、**表記が1つに揃います**
// （`WCMS_BASE_URL` → プロキシのヘッダ → リクエスト、の順）。
//
// ── 読めない相手には出しません ──
//
// QRはURLしか含みませんが、**「そのページが在る」ことは漏れます**。匿名に
// 「読めない」と「存在しない」を区別させない規律に合わせ、404で揃えます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// QRAPIHandler は GET /api/qr?page_id=X です（SVGを返します）。
func QRAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pageID, ok := page.NormalizeID(r.URL.Query().Get("page_id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !page.CanView(auth.CurrentUser(r), idInt) {
		http.NotFound(w, r)
		return
	}

	url := requestBaseURL(r) + "/" + pageID
	m, err := NewQR(url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	// **キャッシュさせません**——同じページでも、社内（ホスト名）と社外（公開名）で
	// 中身が変わります。取り違えると「社外の人に社内URLのQRを見せる」が起きます。
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// SVGは中でスクリプトが動けるので、添付と同じく**この応答だけ何も動かせなく**します。
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Write([]byte(QRSVG(m, 4)))
}
