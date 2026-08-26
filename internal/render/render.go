// Package render は設定ファイル（CLAUDE.md / settings.json 等）を Go text/template で評価する
// 純粋なレンダラ。
//
// フラグの実体（INI の中身・バンドルの構成）は一切知らず、呼び出し側が決めた bool マップ・
// slot の内容・テンプレパスを受け取ってバイト列を返すだけ。状態判定・ファイル配置・
// INI 解析・バンドル探索は上位（orchestrator）の責務。標準ライブラリのみに依存する。
//
// テンプレ文法:
//   - 条件:   {{if .wiki}} ... {{else}} ... {{end}}
//   - 部品:   {{template "name.tmpl" .}}   ← PartialDirs の *.tmpl を basename で参照
//   - slot:   {{slot "memory"}}             ← Slots[name] を verbatim に差し込む
//
// 未定義フラグの参照は missingkey=error で即エラー（タイポの静かな素通しを防ぐ）。
//
// slot の改行の作法は呼び出し側の責務: 内容を「先頭 \n・末尾改行なし」で渡し、消費側テンプレは
// {{- slot "name"}} を専用行に置く。こうすると OFF（内容なし）のとき空行が残らない
// （部品テンプレの whitespace イディオムと同じ）。
package render

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"text/template"

	"github.com/ryokwkm/llmtpl/internal/msg"
)

// Options は 1 回のレンダリングに必要な入力。
type Options struct {
	TmplPath    string            // 必須: レンダリング対象のテンプレ（Source 指定時は名前・エラー表示にのみ使う）
	Source      []byte            // 指定時はファイルではなくこの内容をテンプレとして評価する（バンドル断片用）
	PartialDirs []string          // 部品テンプレのディレクトリ（同名は後勝ち = 後ろが優先）
	Header      string            // 先頭に付ける 1 行（空なら付けない。コメントを書けない JSON では空）
	Flags       map[string]bool   // {{if .name}} で参照されるフラグ集合
	Slots       map[string]string // {{slot "name"}} に差し込む内容
}

// Result はレンダリング結果。
type Result struct {
	Content   []byte          // 生成物（Header 指定時は 1 行目がヘッダ）
	UsedSlots map[string]bool // テンプレ内で実際に参照された slot 名（宣言と受け口の不一致検出用）
}

// Render は Options に従ってテンプレを評価する。
func Render(o Options) (Result, error) {
	if o.TmplPath == "" {
		return Result{}, errors.New(msg.M.Render.NoTmplPath)
	}

	used := map[string]bool{}
	funcs := template.FuncMap{
		// slot: 上位が用意した差し込み内容を verbatim に置く。未提供なら空
		// （ON のフラグが誰もその slot へ寄稿していないだけなので正常）
		"slot": func(name string) (string, error) {
			if name == "" {
				return "", errors.New(msg.M.Render.EmptySlotName)
			}
			used[name] = true
			return o.Slots[name], nil
		},
	}

	root := template.New(filepath.Base(o.TmplPath)).Funcs(funcs).Option("missingkey=error")

	// 部品テンプレ: 指定順にパースし、同名は後勝ち（共有 → スコープ専用の順で渡すと専用側が優先される）
	for _, dir := range o.PartialDirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.tmpl"))
		if err != nil {
			return Result{}, fmt.Errorf(msg.M.Render.PartialsGlobFailed, err)
		}
		if len(matches) == 0 {
			continue // 部品なし・ディレクトリ未作成は正常
		}
		if _, err := root.ParseFiles(matches...); err != nil {
			return Result{}, fmt.Errorf(msg.M.Render.PartialsParseFailed, err)
		}
	}

	if o.Source != nil {
		// Source は「名前 = filepath.Base(TmplPath)」のテンプレとして定義する（Execute の対象になる）
		if _, err := root.Parse(string(o.Source)); err != nil {
			return Result{}, fmt.Errorf(msg.M.Render.SourceParseFailed, o.TmplPath, err)
		}
	} else if _, err := root.ParseFiles(o.TmplPath); err != nil {
		return Result{}, fmt.Errorf(msg.M.Render.TemplateParseFailed, err)
	}

	var buf bytes.Buffer
	if o.Header != "" {
		buf.WriteString(o.Header)
		buf.WriteByte('\n')
	}
	if err := root.Execute(&buf, o.Flags); err != nil {
		return Result{}, fmt.Errorf(msg.M.Render.ExecFailed, err)
	}
	return Result{Content: buf.Bytes(), UsedSlots: used}, nil
}
