package label

import (
	"context"
	"net/http"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// labelPlugin はルートを持ち込むためのプラグインです（表もスキーマも持たない）。
type labelPlugin struct{}

func init() {
	cms.RegisterExtension("label", "ラベルプリンター")
	cms.Register(labelPlugin{})
}

func (labelPlugin) Name() string     { return "label" }
func (labelPlugin) Schema() []string { return nil }
func (labelPlugin) Tables() []string { return nil }

// Routes はラベルプリンターの口です。
//
//	GET /api/label/state … {available, connected, width_mm, error} いま刷れるか（画面が「刷る」の横に出す）
func (labelPlugin) Routes() []cms.Route {
	return []cms.Route{
		{Pattern: "/api/label/state", Handler: StateAPIHandler},
	}
}

// StateAPIHandler は GET /api/label/state です。
func StateAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "GET だけです")
		return
	}
	if auth.CurrentUser(r) == nil {
		cms.JSONFail(w, http.StatusUnauthorized, "ログインしてください")
		return
	}
	if !Available() {
		cms.WriteJSON(w, map[string]any{"success": true, "available": false, "error": ErrUnavailable.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	s, err := GetState(ctx)
	if err != nil {
		cms.WriteJSON(w, map[string]any{"success": true, "available": true, "connected": false, "error": err.Error()})
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "available": true, "connected": s.Connected, "width_mm": s.WidthMM, "error": s.Error})
}
