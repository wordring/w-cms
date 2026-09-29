//go:build windows

// Command w-cms-edit は、w-cms の添付を**ローカルのアプリで編集する**ための、各 PC に置く小さな常駐ヘルパーです
// （2026-09-29）。w-cms の画面の「📝 ローカルで編集」（`w-cms-edit:` のリンク）を受けて、
//
//  1. そのファイルを手元（%LOCALAPPDATA%\w-cms-edit\…）へ落とし、
//  2. 既定のアプリで開き、
//  3. **保存（Ctrl+S）で中身が変わるたびに w-cms へ上げます**（前の中身は w-cms の側で版に残る）。
//
// 利用者:「実運用するには、ローカルアプリで添付ファイルを編集して保存できる必要があります」「Webdavを諦めたはずです」。
// ⚠ WebDAV は Ctrl+S をアプリを閉じるまで送らなかった（2026-09-18・実物の CAD で）——だからこの形（考察 案B）。
//
//   - **鍵はそのファイル1つに限る・使い捨て**（w-cms が出す・12時間で切れる・w-cms を再起動すると切れる）。
//   - ⚠ `w-cms-edit:` は**どのページからでも**叩ける——**登録した w-cms にしか繋がず**、実行できる種類は開かない（link.go）。
//   - **ほかの人が先に書き換えていたら上げない**（409）——手元のファイルの場所を知らせて止まる。書けない所（通信箱の下・
//     権限の無いページ）のファイルは**読み取り専用**で開く（アプリが保存のときに知らせる）。
//   - 見張るのは、最後に保存してから12時間まで（それを過ぎたら終わる）。記録は %LOCALAPPDATA%\w-cms-edit\記録.log。
//
// 使い方（各 PC で1回・管理者は要らない）:
//
//	go build -ldflags "-H=windowsgui" -o w-cms-edit.exe ./cmd/w-cms-edit   … 組み立て（黒い窓を出さない）
//	w-cms-edit.exe -install https://<w-cms のホスト>:8443                   … 登録（アドレスはカンマで複数可）
//	w-cms-edit.exe -uninstall                                               … 登録を消す
//
// ⚠ 前提: その PC で w-cms の証明書を信頼していること（ガイド §6.2a——自己署名の証明書を各 PC に入れる）。
package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const idleLimit = 12 * time.Hour // 最後に保存してから、これだけ何も無ければ見張りを終える

func main() {
	install := flag.String("install", "", "この PC に w-cms-edit: を登録する（w-cms のアドレス・例 https://localhost:8443・カンマで複数）")
	uninstall := flag.Bool("uninstall", false, "登録を消す")
	allow := flag.String("allow", "", "繋いでよい w-cms のアドレス（登録が付ける・カンマで複数）")
	flag.Parse()
	var err error
	switch {
	case *install != "":
		err = doInstall(*install)
	case *uninstall:
		err = run("reg", "delete", `HKCU\Software\Classes\w-cms-edit`, "/f")
		if err == nil {
			showMessage("w-cms-edit の登録を消しました。")
		}
	case flag.NArg() == 1:
		err = edit(flag.Arg(0), strings.Split(*allow, ","))
	default:
		err = errors.New("使い方: w-cms-edit.exe -install https://<w-cms のホスト>:8443")
	}
	if err != nil {
		logf("失敗: %v", err)
		showMessage("w-cms-edit: " + err.Error())
		os.Exit(1)
	}
}

// home は手元の置き場（%LOCALAPPDATA%\w-cms-edit）です。
func home() string {
	d := os.Getenv("LOCALAPPDATA")
	if d == "" {
		d = os.TempDir()
	}
	return filepath.Join(d, "w-cms-edit")
}

func logf(format string, a ...any) {
	os.MkdirAll(home(), 0o755)
	if f, err := os.OpenFile(filepath.Join(home(), "記録.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "%s  %s\r\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
		f.Close()
	}
}

// edit は1つのファイルを落として開き、保存のたびに上げます。
func edit(link string, allowed []string) error {
	base, token, err := parseLink(link, allowed)
	if err != nil {
		return err
	}
	fileURL := base + "/api/local-edit/file?token=" + url.QueryEscape(token)
	res, err := http.Get(fileURL)
	if err != nil {
		return fmt.Errorf("w-cms に繋がりません（%s）: %v", base, err)
	}
	content, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("落とせません（%d）: %s", res.StatusCode, strings.TrimSpace(string(content)))
	}
	etag := res.Header.Get("ETag")
	rawName, _ := url.PathUnescape(res.Header.Get("X-WCMS-Name"))
	name, err := safeLocalName(rawName)
	if err != nil {
		return err
	}
	writable := res.Header.Get("X-WCMS-Writable") == "1"

	cleanOld()
	dir := filepath.Join(home(), strings.NewReplacer(":", "_", "/", "_").Replace(strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")),
		time.Now().Format("20060102-150405")+"-"+token[:6])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	local := filepath.Join(dir, name)
	if err := os.WriteFile(local, content, 0o644); err != nil {
		return err
	}
	if !writable {
		os.Chmod(local, 0o444) // 読み取り専用で開く（アプリが保存のときに知らせる）
	}
	logf("開く: %s（%s・書ける=%v）", local, base, writable)
	if err := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", local).Start(); err != nil {
		return fmt.Errorf("アプリで開けません: %v", err)
	}
	if !writable {
		return nil // 見るだけ——見張らない
	}
	return watch(local, fileURL, base, etag, content)
}

// watch は手元のファイルを見張り、保存で中身が変わるたびに上げます。
func watch(local, fileURL, base, etag string, last []byte) error {
	lastSum := sha256.Sum256(last)
	st, _ := os.Stat(local)
	var lastMod time.Time
	var lastSize int64
	if st != nil {
		lastMod, lastSize = st.ModTime(), st.Size()
	}
	lastActive := time.Now()
	for {
		time.Sleep(time.Second)
		if time.Since(lastActive) > idleLimit {
			logf("見張りを終える（%s・12時間なにも無い）", local)
			return nil
		}
		st, err := os.Stat(local)
		if err != nil {
			logf("見張りを終える（%s が無くなった）", local)
			return nil
		}
		if st.ModTime().Equal(lastMod) && st.Size() == lastSize {
			continue
		}
		// 書き終わるのを待つ（大きさと時刻が2回続けて同じ・読める）。
		time.Sleep(1500 * time.Millisecond)
		st2, err := os.Stat(local)
		if err != nil || !st2.ModTime().Equal(st.ModTime()) || st2.Size() != st.Size() {
			continue
		}
		b, err := os.ReadFile(local)
		if err != nil || len(b) == 0 {
			continue // アプリが書いている最中・掴んでいる——次の回に
		}
		lastMod, lastSize = st2.ModTime(), st2.Size()
		sum := sha256.Sum256(b)
		if sum == lastSum {
			continue
		}
		newTag, err := put(fileURL, base, etag, b)
		if err != nil {
			var stop stopError
			if errors.As(err, &stop) {
				logf("止める: %s: %v", local, err)
				showMessage("w-cms-edit: " + err.Error() + "\n\n手元のファイルはここにあります:\n" + local)
				return nil
			}
			logf("上げられない（あとでもう一度）: %s: %v", local, err)
			lastMod = time.Time{} // 次の回にもう一度試す
			time.Sleep(10 * time.Second)
			continue
		}
		etag, lastSum, lastActive = newTag, sum, time.Now()
		logf("上げた: %s（%d バイト）", local, len(b))
	}
}

// stopError は、もう上げても無駄な失敗（ほかの人が先に書き換えた・書けない・鍵が切れた）です。
type stopError struct{ msg string }

func (e stopError) Error() string { return e.msg }

func put(fileURL, base, etag string, content []byte) (string, error) {
	req, err := http.NewRequest(http.MethodPut, fileURL, bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	req.Header.Set("If-Match", etag)
	req.Header.Set("Origin", base) // w-cms の CSRF の守り（同じオリジンか）を通す
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	msg, _ := io.ReadAll(res.Body)
	res.Body.Close()
	switch res.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return res.Header.Get("ETag"), nil
	case http.StatusConflict, http.StatusForbidden, http.StatusUnauthorized, http.StatusNotFound,
		http.StatusRequestEntityTooLarge, http.StatusBadRequest:
		return "", stopError{strings.TrimSpace(string(msg))}
	}
	return "", fmt.Errorf("%d %s", res.StatusCode, strings.TrimSpace(string(msg)))
}

// cleanOld は7日より前に落とした置き場を片付けます（見張りの終わったもの）。
func cleanOld() {
	hosts, _ := os.ReadDir(home())
	for _, h := range hosts {
		if !h.IsDir() {
			continue
		}
		dirs, _ := os.ReadDir(filepath.Join(home(), h.Name()))
		for _, d := range dirs {
			if info, err := d.Info(); err == nil && d.IsDir() && time.Since(info.ModTime()) > 7*24*time.Hour {
				os.RemoveAll(filepath.Join(home(), h.Name(), d.Name()))
			}
		}
	}
}

// doInstall は w-cms-edit: をこの利用者に登録します（HKCU・管理者は要らない）。
func doInstall(addresses string) error {
	var bases []string
	for _, a := range strings.Split(addresses, ",") {
		if strings.TrimSpace(a) == "" {
			continue
		}
		b, err := normBase(a)
		if err != nil {
			return err
		}
		bases = append(bases, b)
	}
	if len(bases) == 0 {
		return errors.New("w-cms のアドレスを渡してください（例 https://localhost:8443）")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	key := `HKCU\Software\Classes\w-cms-edit`
	for _, args := range [][]string{
		{"add", key, "/ve", "/d", "URL:w-cms-edit", "/f"},
		{"add", key, "/v", "URL Protocol", "/t", "REG_SZ", "/d", "", "/f"},
		{"add", key + `\shell\open\command`, "/ve", "/d",
			`"` + exe + `" -allow ` + strings.Join(bases, ",") + ` "%1"`, "/f"},
	} {
		if err := run("reg", args...); err != nil {
			return err
		}
	}
	showMessage("w-cms-edit を登録しました（" + strings.Join(bases, "・") + "）。\n" +
		"w-cms のファイル表示の「📝 ローカルで編集」で、アプリで開いて保存（Ctrl+S）すると、数秒で w-cms に入ります。")
	return nil
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return errors.New(name + " " + args[0] + ": " + strings.TrimSpace(string(out)))
	}
	return nil
}

// showMessage は知らせを小さな窓で出します（黒い窓を出さない組み立てなので、標準出力は見えない）。
func showMessage(text string) {
	q := strings.ReplaceAll(text, "'", "''")
	exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		"Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show('"+q+"', 'w-cms-edit') | Out-Null").Run()
}
