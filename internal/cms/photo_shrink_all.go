package cms

// ─────────────────────────────────────────────────────────────────────────
// 写真をまとめて小さくする——管理画面の「🗜 写真を小さくする」（2026-10-27）
//
// 利用者が選んだ置き場の1つ（photo_shrink.go の冒頭）。全ページの添付のうち、設定 `photo_shrink` の長い辺より
// 長い JPEG を、1枚ずつ縮めます（1枚 0.5 秒ほど・2,600 枚で20分ほど）。
//
//   GET  /api/admin/photo-shrink … 縮める写真の数と大きさ・いま動いているかと、どこまで進んだか
//   POST /api/admin/photo-shrink … 始める（{action:"start"}）——裏で動き、ページを閉じても続く
//
// 1度に1つだけ動きます。サーバーを入れ替えると止まりますが、もう一度押せば続きから縮めます
// （縮めた写真は長い辺が設定の大きさ以下なので、数えるときに外れる）。
// 1枚ずつの監査は残しません（数千行になる）——始めと終わりを1行ずつ残し、どの写真を縮めたかは
// 各ページの目録（meta.json の `shrunk_from`）にあります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// photoCandidate は縮める写真1枚です。
type photoCandidate struct {
	PageID string
	Stored string
	Size   int64
}

// photoShrinkCandidates は、縮める写真（書き換えてよい範囲の、長い辺が longSide より長い JPEG）を返します。
// 大きさは頭だけ読んで確かめます（全部は復号しない）。
func photoShrinkCandidates(longSide int) ([]photoCandidate, error) {
	rows, err := database.DB.Query(`SELECT id FROM pages`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, page.FormatID(id))
		}
	}
	rows.Close()
	sort.Strings(ids)
	var out []photoCandidate
	for _, pid := range ids {
		entries, err := os.ReadDir(page.AttachmentDir(pid))
		if err != nil {
			continue
		}
		var photos []os.DirEntry
		for _, e := range entries {
			if e.Type().IsRegular() && isPhotoName(e.Name()) && !IsAttachmentMetaFile(e.Name()) {
				photos = append(photos, e)
			}
		}
		if len(photos) == 0 || inReadOnlyArea(pid) {
			continue
		}
		for _, e := range photos {
			path := filepath.Join(page.AttachmentDir(pid), e.Name())
			w, h, size, ok := jpegSizeOf(path)
			if ok && max(w, h) > longSide {
				out = append(out, photoCandidate{PageID: pid, Stored: e.Name(), Size: size})
			}
		}
	}
	return out, nil
}

// jpegSizeOf はファイルの頭だけ読んで、JPEG の画素の大きさとファイルの大きさを返します。
func jpegSizeOf(path string) (w, h int, size int64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, 0, 0, false
	}
	// 大きさの印（SOF）は、たいてい頭の数十 KB にある（EXIF の縮小画像のあと）。
	head, _ := io.ReadAll(io.LimitReader(f, 512<<10))
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(head))
	if err != nil {
		return 0, 0, 0, false
	}
	return cfg.Width, cfg.Height, st.Size(), true
}

// photoShrinkJob は、まとめて縮める仕事の様子です。
type photoShrinkJob struct {
	Running  bool     `json:"running"`
	By       string   `json:"by,omitempty"`
	Started  string   `json:"started,omitempty"`
	Finished string   `json:"finished,omitempty"`
	Total    int      `json:"total"`
	Done     int      `json:"done"`
	Shrunk   int      `json:"shrunk"`
	Skipped  int      `json:"skipped"` // 小さくならなかった・ほかで書き換えられた
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"` // 初めの20件
	Before   int64    `json:"before"`           // 縮めた写真の、縮める前の合計（バイト）
	After    int64    `json:"after"`
}

var (
	photoJobMu sync.Mutex
	photoJob   photoShrinkJob
)

// startPhotoShrinkAll は、まとめて縮める仕事を裏で始めます（もう動いていれば false）。
func startPhotoShrinkAll(user *auth.User, list []photoCandidate) bool {
	photoJobMu.Lock()
	if photoJob.Running {
		photoJobMu.Unlock()
		return false
	}
	photoJob = photoShrinkJob{Running: true, By: user.Username, Started: time.Now().In(time.Local).Format(time.RFC3339), Total: len(list)}
	photoJobMu.Unlock()
	auth.Audit(user.Username, "attach.shrink-all.start", fmt.Sprintf("%d 枚", len(list)))
	go func() {
		for _, c := range list {
			res, err := shrinkPhotoFile(user, c.PageID, c.Stored)
			photoJobMu.Lock()
			photoJob.Done++
			switch {
			case err == nil:
				photoJob.Shrunk++
				photoJob.Before += res.Before
				photoJob.After += res.After
			case errors.Is(err, errPhotoSmall), errors.Is(err, errPhotoNotSmaller), errors.Is(err, errPhotoChanged):
				photoJob.Skipped++
			default:
				photoJob.Failed++
				if len(photoJob.Errors) < 20 {
					photoJob.Errors = append(photoJob.Errors, c.PageID+"/"+c.Stored+": "+err.Error())
				}
			}
			photoJobMu.Unlock()
		}
		photoJobMu.Lock()
		photoJob.Running = false
		photoJob.Finished = time.Now().In(time.Local).Format(time.RFC3339)
		j := photoJob
		photoJobMu.Unlock()
		msg := fmt.Sprintf("縮めた %d 枚（%.1f MB → %.1f MB）・そのまま %d 枚・失敗 %d 枚", j.Shrunk,
			float64(j.Before)/1e6, float64(j.After)/1e6, j.Skipped, j.Failed)
		auth.Audit(user.Username, "attach.shrink-all.done", msg)
		log.Printf("写真をまとめて小さくしました: %s", msg)
	}()
	return true
}

// PhotoShrinkAllAPIHandler は /api/admin/photo-shrink です（admin 限定）。
func PhotoShrinkAllAPIHandler(w http.ResponseWriter, r *http.Request) {
	if !page.RequireAdmin(w, r) {
		return
	}
	conf := PhotoShrinkConfig()
	switch r.Method {
	case http.MethodGet:
		photoJobMu.Lock()
		job := photoJob
		photoJobMu.Unlock()
		out := map[string]any{"success": true, "job": job, "enabled": conf != nil}
		if conf != nil {
			out["long_side"] = conf.LongSide
			// 動いているあいだは数えない（同じ写真を読み合う・数は進み具合で分かる）。
			if !job.Running {
				list, err := photoShrinkCandidates(conf.LongSide)
				if err != nil {
					JSONFail(w, http.StatusInternalServerError, "写真を数えられませんでした: "+err.Error())
					return
				}
				var total int64
				for _, c := range list {
					total += c.Size
				}
				out["count"], out["bytes"] = len(list), total
			}
		}
		WriteJSON(w, out)
	case http.MethodPost:
		user, ok := GateJSONPost(w, r)
		if !ok {
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		if !DecodeJSONBody(w, r, &req) {
			return
		}
		if req.Action != "start" {
			JSONFail(w, http.StatusBadRequest, "action は start です")
			return
		}
		if conf == nil {
			JSONFail(w, http.StatusConflict, errPhotoShrinkOff.Error())
			return
		}
		list, err := photoShrinkCandidates(conf.LongSide)
		if err != nil {
			JSONFail(w, http.StatusInternalServerError, "写真を数えられませんでした: "+err.Error())
			return
		}
		if len(list) == 0 {
			JSONFail(w, http.StatusConflict, "小さくする写真はありません")
			return
		}
		if !startPhotoShrinkAll(user, list) {
			JSONFail(w, http.StatusConflict, "もう動いています")
			return
		}
		WriteJSON(w, map[string]any{"success": true, "total": len(list)})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
