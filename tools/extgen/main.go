// extgen は、拡張のフォルダ（ext/ の下）から、実行ファイルへの取り付け口 cmd/w-cms/ext_*_gen.go を作り直します（2026-10-20）。
//
// 使い方（リポジトリの根で）: go generate ./cmd/w-cms （cmd/w-cms/main.go の //go:generate が、この道具を呼ぶ）
// 拡張のフォルダを足したり外したりしたら流します。取り付け口と、その拡張の assets/（JS・CSS・HTML）が組み込まれます。
// 決まりは internal/extgen にあります。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"w-cms/internal/extgen"
)

func main() {
	// go generate は、//go:generate を書いたファイルのフォルダ（cmd/w-cms）で走る。根は2つ上。
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		root = "." // リポジトリの根から直に流したとき
	}
	out := filepath.Join(root, "cmd", "w-cms")
	exts, err := extgen.Scan(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "拡張を探せません:", err)
		os.Exit(1)
	}
	want := extgen.Files("w-cms", exts)
	ents, _ := os.ReadDir(out)
	for _, e := range ents {
		if extgen.IsGenerated(e.Name()) {
			if _, keep := want[e.Name()]; !keep {
				os.Remove(filepath.Join(out, e.Name()))
				fmt.Println("外した:", e.Name())
			}
		}
	}
	for name, body := range want {
		if err := os.WriteFile(filepath.Join(out, name), []byte(body), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "書けません:", err)
			os.Exit(1)
		}
	}
	for _, e := range exts {
		fmt.Printf("拡張 ext/%s（%s）\n", e.Dir, e.Tags)
	}
}
