// Package bundle はバンドル（= フラグ 1 個に対応するミニ プロジェクトツリー）の
// 探索と、断片の合成を担う。
//
// バンドルルート直下のディレクトリ 1 つ = フラグ 1 つ（ディレクトリ名がフラグ名）。
//
// **バンドルの中身は、ターゲット（= プロジェクトルート）の中へそのまま重なる。**
//
//	<root>/<flag>/AGENTS.md.tmpl              … <T>/AGENTS.md へ差し込む断片
//	<root>/<flag>/.claude/CLAUDE.md.tmpl      … <T>/.claude/CLAUDE.md へ差し込む断片
//	<root>/<flag>/.claude/settings.json.tmpl  … <T>/.claude/settings.json へ deep merge する断片
//	<root>/<flag>/.claude/rules|skills|…      … <T>/.claude/<同名>/ へリンクされるディレクトリ
//	<root>/<flag>/bundle.conf                 … 任意のメタ情報（description）
//
// 列挙するのは「バンドル直下」と「OverlayDirs の各ディレクトリ直下」の **2 段だけ**で、
// 一般のサブディレクトリへは降りない。エントリは**バンドル相対パス**で返す。
//
// **差し込み先の slot 名はフラグ名そのもの**。消費側テンプレの {{slot "<フラグ名>"}} が
// そのバンドルの断片の置き場になる。1 バンドルは CLAUDE.md.tmpl を 1 つしか持てないので
// slot 名を独立した語彙にする意味がなく、断片側に配置先を宣言する必要もない
// （同じ場所へ複数バンドルを入れたいときは受け口を並べて書く。順序は書き順で決まる）。
//
// 受け口が無いターゲットでは、断片は生成物の末尾へ追記される。
//
// レンダリングは行わない（呼び出し側が internal/render で断片を評価し、その結果を
// Piece として Compose に渡す）。Compose は純関数。
package bundle

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// バンドルのレイアウト語彙はこのパッケージが所有する（配布側 link・合成側 apply が参照する）。
const (
	// ConfName はバンドルのメタ情報ファイル（配布対象外）。
	ConfName = "bundle.conf"
	// PartialsDirName はテンプレの部品置き場（配布対象外）。
	PartialsDirName = "partials"
)

// DefaultOverlay は必ず走査対象に含めるオーバーレイ層。
//
// バンドルが 1 つも無くなっても、以前配ったリンクを回収できるようにするために要る
// （走査層をバンドルの実データだけから作ると、最後のバンドルを消した瞬間に掃除できなくなる）。
const DefaultOverlay = ".claude"

// vcsDir は層として扱わないディレクトリ（バンドルが git リポジトリ自体でも中を舐めない）。
const vcsDir = ".git"

// overlayDirs はバンドル b が持つオーバーレイ層（ドット始まりの 1 階層目ディレクトリ）。
//
// **`.claude` を特別扱いしない。** バンドルの中身はターゲットの中へそのまま重なるので、
// `<flag>/.cursor/` や `<flag>/.github/` も同じ規則で配れる。
func overlayDirs(b Bundle) ([]string, error) {
	es, err := os.ReadDir(b.Dir)
	if err != nil {
		return nil, fmt.Errorf("%s を読めません: %w", b.Dir, err)
	}
	var out []string
	for _, e := range es {
		name := e.Name()
		if !isOverlayName(name) || !isDir(filepath.Join(b.Dir, name)) {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// isOverlayName は層として扱ってよい名前か（ドット始まりの 1 セグメント）。
//
// **ドット始まりであることが安全弁**。層が "." になると所有リンクの走査範囲が
// ターゲット全体へ広がり、無関係な symlink を巻き込む。
func isOverlayName(name string) bool {
	return strings.HasPrefix(name, ".") && name != "." && name != ".." &&
		name != vcsDir && !strings.ContainsRune(name, filepath.Separator)
}

// Layers は棚全体のオーバーレイ層（ソート順・DefaultOverlay を必ず含む）。
//
// **ON のバンドルだけでなく棚にある全バンドルから集める。** フラグを OFF にした
// バンドルの層も走査対象に残さないと、その層に配ったリンクを外せなくなる
// （＝ キルスイッチが片肺になる）。
func Layers(bundles []Bundle) ([]string, error) {
	seen := map[string]bool{DefaultOverlay: true}
	for _, b := range bundles {
		dirs, err := overlayDirs(b)
		if err != nil {
			return nil, err
		}
		for _, d := range dirs {
			seen[d] = true
		}
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// Bundle は 1 フラグ分のバンドル。
type Bundle struct {
	Name string // ディレクトリ名 = フラグ名
	Dir  string // バンドルディレクトリのパス
}

// Piece はレンダリング済みの断片 1 つ（Compose の入力）。
type Piece struct {
	Bundle string // どのバンドル由来か（= 差し込み先の slot 名）
	Text   string // render 済みの本文
}

// Composition は断片群を slot（= バンドル名）別にまとめた結果。
//
// 文字列は「先頭 \n・末尾改行なし」。消費側テンプレが {{- slot "name"}} を専用行に
// 置く前提の作法で、寄稿ゼロなら空文字なので空行も残らない。
type Composition struct {
	Slots map[string]string // バンドル名 → 差し込む文字列
}

// Discover はバンドルルート直下のディレクトリをフラグとして列挙する（名前のソート順）。
// ドット始まりのディレクトリ（.git 等）とファイルは無視する。
//
// **ディレクトリ判定は symlink を辿る**（isDir 参照）。バンドルルートを実ディレクトリにして
// 他リポジトリのバンドルだけを symlink で取り込む構成を許すため。
func Discover(root string) ([]Bundle, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("バンドルルートを読めません: %w", err)
	}
	var out []Bundle
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if !isDir(dir) {
			continue
		}
		out = append(out, Bundle{Name: e.Name(), Dir: dir})
	}
	slices.SortFunc(out, func(a, b Bundle) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// Names はバンドル名の一覧（Discover の順序を保つ = ソート済み）。
func Names(bundles []Bundle) []string {
	out := make([]string, 0, len(bundles))
	for _, b := range bundles {
		out = append(out, b.Name)
	}
	return out
}

// entries はバンドルが持つ配布対象エントリを**バンドル相対パス**で列挙する（ソート順）。
//
// 見るのは「バンドル直下」と「そのバンドルが持つオーバーレイ層の直下」の 2 段だけ。
// 層自身（.claude 等）はドット始まりなので distributed に弾かれ、エントリには出ない
// — 出すと <T>/.claude → <flag>/.claude のディレクトリ symlink がターゲットの層を乗っ取る。
func entries(b Bundle) ([]string, error) {
	layers, err := overlayDirs(b)
	if err != nil {
		return nil, err
	}
	var out []string
	collect := func(rel string) error {
		dir := filepath.Join(b.Dir, rel)
		es, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("%s を読めません: %w", dir, err)
		}
		for _, e := range es {
			if !distributed(e.Name()) {
				continue
			}
			out = append(out, filepath.Join(rel, e.Name()))
		}
		return nil
	}
	if err := collect(""); err != nil {
		return nil, err
	}
	for _, ov := range layers {
		if err := collect(ov); err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return out, nil
}

// Contents はバンドルが持つ要素の一覧（`llmtpl bundles` の表示用）。
// **配布されないもの（メタファイル・partials・ドット始まり）は除く**
// — 表示と配布で除外規則が食い違うと「表示には出るが配られない」を招くため、
// 判定は distributed 1 か所に集約する。
func Contents(b Bundle) ([]string, error) {
	rels, err := entries(b)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		if isDir(filepath.Join(b.Dir, rel)) {
			out = append(out, rel+"/")
			continue
		}
		out = append(out, rel)
	}
	return out, nil
}

// CheckLayout はバンドル直下に配布対象の**ディレクトリ**が無いことを確かめる。
//
// これは移行ガードではなく恒久仕様。バンドルがルート直下へディレクトリを配れると、
// link の所有リンク走査に層 "." が要り、削除の探索範囲がターゲット全体へ広がる
// （無関係な symlink を巻き込む事故が実測されている）。ファイル断片（AGENTS.md.tmpl 等）は
// ルート直下に置いてよい —— 生成物はリンクではないので走査範囲に影響しない。
func CheckLayout(b Bundle) error {
	es, err := os.ReadDir(b.Dir)
	if err != nil {
		return fmt.Errorf("%s を読めません: %w", b.Dir, err)
	}
	for _, e := range es {
		if !distributed(e.Name()) || !isDir(filepath.Join(b.Dir, e.Name())) {
			continue
		}
		return fmt.Errorf("バンドル %s: %s/ はバンドル直下に置けません（%s/%s/ のようにドット始まりの層へ移してください）",
			b.Name, e.Name(), DefaultOverlay, e.Name())
	}
	return nil
}

// distributed はエントリ名が配布対象かを返す（Contents と link の共通判定）。
// **どの深さでも同じ規則**で、ドット始まりの除外はオーバーレイ層の中でも緩めない。
func distributed(name string) bool {
	return !strings.HasPrefix(name, ".") && name != ConfName && name != PartialsDirName
}

// isDir は path がディレクトリかを返す（**symlink は辿る**）。
//
// os.ReadDir が返す DirEntry.IsDir() は lstat 相当で symlink を false にするため、
// 他リポジトリから symlink で取り込んだバンドル（や、バンドル内の rules 等）が
// 黙って無視されてしまう。バンドルの実体を別リポジトリで管理する構成を成立させるため、
// バンドル側の判定はすべてこの関数を通す。
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// DistDirs はターゲットへリンクするディレクトリを**バンドル相対パス**で返す（ソート順）。
// CheckLayout により実質すべて <overlay>/<器> の形になる。
func DistDirs(b Bundle) ([]string, error) {
	rels, err := entries(b)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range rels {
		if isDir(filepath.Join(b.Dir, rel)) {
			out = append(out, rel)
		}
	}
	return out, nil
}

// Fragments は断片ファイル（*.tmpl）を**バンドル相対パス**で返す（ソート順）。
//
// 断片の差し込み先は**ターゲットの同じ相対パスにある *.tmpl** なので、この一覧が
// 「このバンドルが寄稿しうる先」そのものになる。ターゲット側に受け皿が無い断片を
// 検出するために使う（apply が警告する）。
//
// 相対パスで照合するので <flag>/AGENTS.md.tmpl と <flag>/.claude/AGENTS.md.tmpl は別物として
// 共存できる（basename 照合だと衝突していた）。
func Fragments(b Bundle) ([]string, error) {
	rels, err := entries(b)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range rels {
		if !strings.HasSuffix(rel, ".tmpl") || isDir(filepath.Join(b.Dir, rel)) {
			continue
		}
		out = append(out, rel)
	}
	return out, nil
}

// LoadFragment はバンドル内の断片ファイルを読む。無ければ ok=false（エラーではない）。
func LoadFragment(b Bundle, name string) (content []byte, ok bool, err error) {
	p := filepath.Join(b.Dir, name)
	content, err = os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%s を読めません: %w", p, err)
	}
	return content, true, nil
}

// Meta は bundle.conf の内容（bundle.conf 自体は任意）。
type Meta struct {
	Description string
}

// LoadMeta は bundle.conf を読む。bundle.conf が無ければ既定値（description 空）。
func LoadMeta(b Bundle) (Meta, error) {
	var m Meta
	p := filepath.Join(b.Dir, ConfName)
	content, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return m, nil
		}
		return Meta{}, fmt.Errorf("%s を読めません: %w", p, err)
	}
	kvs, err := parseKeyValues(string(content))
	if err != nil {
		return Meta{}, fmt.Errorf("%s: %w", p, err)
	}
	for _, kv := range kvs {
		switch kv.key {
		case "description":
			m.Description = kv.val // 重複は後勝ち
		default:
			return Meta{}, fmt.Errorf("%s: %d 行目: 未知のキー: %s（使えるのは description）", p, kv.line, kv.key)
		}
	}
	return m, nil
}

type keyValue struct {
	key, val string
	line     int // 1 始まりの行番号
}

// parseKeyValues は `key: value` の行を順序どおりに読む（空行と # コメントは無視）。
// エラーにファイル名は付けない（呼び出し側の責務）。
func parseKeyValues(s string) ([]keyValue, error) {
	var out []keyValue
	for i, raw := range strings.Split(s, "\n") {
		n := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%d 行目: : がありません: %s", n, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%d 行目: キー名が空です: %s", n, line)
		}
		out = append(out, keyValue{key: key, val: strings.TrimSpace(val), line: n})
	}
	return out, nil
}

// Compose は断片を「バンドル名 → 差し込む文字列」へまとめる。
// **1 バンドルは CLAUDE.md.tmpl を 1 つしか持たないのでキーは衝突せず、連結も順序付けも要らない**
// （同じ場所へ複数バンドルを入れたいときは消費側が受け口を並べて書く）。
// レンダリング結果が空（フラグ横断条件で中身が消えた等）の断片は寄稿しない。
func Compose(pieces []Piece) Composition {
	comp := Composition{Slots: map[string]string{}}
	for _, p := range pieces {
		text := strings.TrimRight(p.Text, "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		comp.Slots[p.Bundle] = "\n" + text
	}
	return comp
}
