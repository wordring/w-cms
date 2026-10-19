package toho

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReadOnlyHandlersRejectPOST は、読むだけの口が GET 以外を自分で断ることを固定します（2026-10-19・【考察】文書の食い違い §3.3
// ——それまではメソッドを見ていなかった）。
func TestReadOnlyHandlersRejectPOST(t *testing.T) {
	for _, c := range []struct {
		path string
		fn   http.HandlerFunc
	}{
		{"/api/filing-search?q=x", FilingSearchAPIHandler},
		{"/api/rfq/folder-products?folder=000001", RFQFolderProductsAPIHandler},
		{"/api/estimate/folder-products?folder=000001", EstimateFolderProductsAPIHandler},
	} {
		rr := httptest.NewRecorder()
		c.fn(rr, httptest.NewRequest("POST", c.path, nil))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s を断っていません: %d", c.path, rr.Code)
		}
	}
}
