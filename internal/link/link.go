// Package link はバンドルが持つディレクトリ（rules / skills / agents / commands / hooks …）を
// ターゲットの出力先（実運用では .claude/）へ symlink で配る。
//
// 所有権は「バンドルルート配下を指す symlink かどうか」で判定する。状態ファイルを持たずに
// 冪等な付け外しができ、フラグを OFF にすれば自分が張ったリンクだけが消える。
// 未知の実体（インストーラが直接書いたディレクトリ等）は消さず、原本と内容が違えば退避する。
//
// バンドルの中身はターゲットの中へそのまま重なる。層（.claude 等のドット始まり）ごとに
// 「器 → エントリ」の 2 段を配る。
//
// 粒度は 2 種類:
//   - 畳む（foldedDirs）: .claude/rules/<フラグ名> → <バンドル>/.claude/rules
//     .claude/rules/ は .md を再帰探索し、ディレクトリ symlink も公式にサポートされるため 1 本で済む
//   - エントリ単位: .claude/skills/<skill 名> → <バンドル>/skills/<skill 名>
//     skills は <name>/SKILL.md が発見規約なので階層を挟めない。agents / commands / hooks も同様に扱う
package link

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ryokwkm/llmtpl/internal/bundle"
	"github.com/ryokwkm/llmtpl/internal/fileout"
)

// foldedDirs は「フラグ 1 個 = ディレクトリ 1 本の symlink」に畳める器（**バンドル相対パス**）。
//
// basename 一致にしないこと —— それだと将来 <flag>/.claude/docs/rules/ のような深い rules まで
// 畳んでしまう。バンドルの列挙は 2 段までなのでキーは常にこの形になる。
var foldedDirs = map[string]bool{".claude/rules": true}

// Kind はリンク 1 本に対して行った（DryRun なら行う予定の）操作の種類。
type Kind string

const (
	KindCreated  Kind = "created"  // 張った
	KindRemoved  Kind = "removed"  // 外した（OFF になった）
	KindKept     Kind = "kept"     // 既に正しい
	KindArchived Kind = "archived" // リンク先に未知の実体があったので退避した
	KindConflict Kind = "conflict" // バンドル間の名前衝突（先勝ちでスキップ）
)

// Options はリンク合成の設定。
type Options struct {
	TplHome    string // 所有権判定の基準（この配下を指す symlink = llmtpl が張ったもの）
	ArchiveDir string // 未知の実体の退避先（ターゲット直下の .archive）
	DryRun     bool

	// ScanLayers は所有リンクを探す層（outDir 相対）。通常は Root.Layers（棚から集めた層）を渡す。
	// 空なら {"."} = outDir 直下 2 段（旧レイアウトの挙動）。
	//
	// ⚠️ **すべてドット始まりの 1 セグメントであること**（bundle.Layers が保証する）。
	// 層に "." が混ざると削除の探索範囲がターゲット全体へ広がり、無関係な symlink を巻き込む。
	ScanLayers []string
}

// Action は 1 リンクに対して行った（DryRun なら行う予定の）操作。
type Action struct {
	Path   string // .claude 配下のリンクパス
	Target string // リンク先（相対）
	Kind   Kind
	Note   string
}

// wanted は「張りたいリンク 1 本」。
type wanted struct {
	target string // リンク先（絶対パス）
	bundle string // 提供したバンドル名
}

// Sync は ON バンドルのディレクトリを outDir へ反映する。
// bundles の順序が名前衝突時の優先順（先勝ち）。
func Sync(outDir string, bundles []bundle.Bundle, o Options) ([]Action, error) {
	desired, actions, err := plan(bundles)
	if err != nil {
		return nil, err
	}

	// 既に張ってある所有リンク（rel → 現在の向き先）。要らなくなったものを外す
	existing, err := ownedLinks(outDir, o.TplHome, o.ScanLayers)
	if err != nil {
		return nil, err
	}
	for _, rel := range slices.Sorted(maps.Keys(existing)) {
		if _, keep := desired[rel]; keep {
			continue
		}
		linkPath := filepath.Join(outDir, rel)
		actions = append(actions, Action{Path: linkPath, Kind: KindRemoved})
		if !o.DryRun {
			if err := os.Remove(linkPath); err != nil {
				return nil, fmt.Errorf("%s を外せません: %w", linkPath, err)
			}
		}
	}

	// 欲しいリンクを張る（既に正しければ触らない）
	for _, rel := range slices.Sorted(maps.Keys(desired)) {
		w := desired[rel]
		linkPath := filepath.Join(outDir, rel)
		relTarget, err := filepath.Rel(filepath.Dir(linkPath), w.target)
		if err != nil {
			return nil, err
		}
		if existing[rel] == relTarget {
			actions = append(actions, Action{Path: linkPath, Target: relTarget, Kind: KindKept})
			continue
		}

		archived, planned, err := o.clear(linkPath, w.target, w.bundle)
		if err != nil {
			return nil, err
		}
		switch {
		case archived != "":
			actions = append(actions, Action{Path: linkPath, Kind: KindArchived, Note: archived})
		case planned:
			actions = append(actions, Action{Path: linkPath, Kind: KindArchived, Note: "(dry-run) 退避が必要"})
		}

		if !o.DryRun {
			if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
				return nil, err
			}
			if err := os.Symlink(relTarget, linkPath); err != nil {
				return nil, fmt.Errorf("%s を張れません: %w", linkPath, err)
			}
		}
		actions = append(actions, Action{Path: linkPath, Target: relTarget, Kind: KindCreated})
	}

	slices.SortStableFunc(actions, func(a, b Action) int {
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		return strings.Compare(string(a.Kind), string(b.Kind))
	})
	return actions, nil
}

// clear は linkPath を張り替え可能な状態にする。
// 未知の実体があれば退避し（DryRun では planned=true を返すだけ）、所有リンクなら外す。
func (o Options) clear(linkPath, target, bundleName string) (archived string, planned bool, err error) {
	fi, err := os.Lstat(linkPath)
	if err != nil {
		return "", false, nil // 何も無い
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		// 張りたい位置に居る symlink が**自分のものとは限らない**（人が張った近道など）。
		// 所有していないものは黙って消さず退避する。
		linkTarget, err := os.Readlink(linkPath)
		if err != nil {
			return "", false, err
		}
		resolvedHome, err := filepath.EvalSymlinks(o.TplHome)
		if err != nil {
			resolvedHome = ""
		}
		if !ownedBy(linkPath, linkTarget, o.TplHome, resolvedHome) {
			if o.DryRun {
				return "", true, nil
			}
			backup, err := o.archive(linkPath, bundleName)
			return backup, false, err
		}
		if o.DryRun {
			return "", false, nil
		}
		return "", false, os.Remove(linkPath) // 向き先が違う所有リンク
	}

	// 未知の実体: 原本と同内容なら消して張り替え、違えば退避してから張る
	same, err := sameContent(linkPath, target)
	if err != nil {
		return "", false, err
	}
	if !same {
		if o.DryRun {
			return "", true, nil
		}
		backup, err := o.archive(linkPath, bundleName)
		return backup, false, err
	}
	if o.DryRun {
		return "", false, nil
	}
	return "", false, os.RemoveAll(linkPath)
}

// archive は未知の実体を退避先へ移動し、退避パスを返す。
// 命名は fileout.ArchivePath に委ねる（退避の規則を 1 か所に保つ）。
func (o Options) archive(path, bundleName string) (string, error) {
	parts := make([]string, 0, 2)
	for _, s := range []string{bundleName, filepath.Base(path)} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	backup, err := fileout.ArchivePath(o.ArchiveDir, strings.Join(parts, "-"))
	if err != nil {
		return "", err
	}
	if err := os.Rename(path, backup); err != nil {
		return "", fmt.Errorf("%s を退避できません: %w", path, err)
	}
	return backup, nil
}

// plan は「.claude 相対のリンクパス → 張りたいリンク」を決める。
// actions は名前衝突（先勝ちでスキップ）の報告。
func plan(bundles []bundle.Bundle) (map[string]wanted, []Action, error) {
	desired := map[string]wanted{}
	var actions []Action

	for _, b := range bundles {
		// 「何を配るか」の除外規則は bundle パッケージが持つ（表示側 Contents と同じ判定）
		dirNames, err := bundle.DistDirs(b)
		if err != nil {
			return nil, nil, err
		}
		for _, dirName := range dirNames {
			srcDir := filepath.Join(b.Dir, dirName)

			if foldedDirs[dirName] {
				rel := filepath.Join(dirName, b.Name) // .claude/rules/<フラグ名>
				desired[rel] = wanted{target: srcDir, bundle: b.Name}
				continue
			}

			children, err := os.ReadDir(srcDir)
			if err != nil {
				return nil, nil, fmt.Errorf("%s を読めません: %w", srcDir, err)
			}
			for _, ch := range children {
				if strings.HasPrefix(ch.Name(), ".") {
					continue
				}
				rel := filepath.Join(dirName, ch.Name())
				if prev, dup := desired[rel]; dup {
					actions = append(actions, Action{
						Path: rel,
						Kind: KindConflict,
						Note: fmt.Sprintf("%s の %s は %s が先に提供しているためスキップ", b.Name, rel, prev.bundle),
					})
					continue
				}
				desired[rel] = wanted{target: filepath.Join(srcDir, ch.Name()), bundle: b.Name}
			}
		}
	}
	return desired, actions, nil
}

// ownedLinks は「バンドルルート配下を指す symlink」を集める（キーは outDir 相対、値は現在の向き先）。
//
// 走査は **層 × 器 × エントリ**の 3 段。層は呼び出し側が渡す（通常 Root.Layers）。
// 層が空なら {"."} で旧レイアウトどおり outDir 直下 2 段を見る。
//
// 走査する物理パスの集合は「outDir=<T> ＋ 層=.claude」でも「outDir=<T>/.claude ＋ 層=.」でも同一。
// ターゲットがプロジェクトルートへ上がっても、削除の探索範囲は <T>/.claude/<器>/<エントリ> に閉じる。
func ownedLinks(outDir, tplHome string, layers []string) (map[string]string, error) {
	out := map[string]string{}
	if len(layers) == 0 {
		layers = []string{"."}
	}
	// tplHome 自身が symlink の場合に備えた実体解決は 1 回だけ（エントリごとに解決しない）
	resolvedHome, err := filepath.EvalSymlinks(tplHome)
	if err != nil {
		resolvedHome = ""
	}

	for _, layer := range layers {
		if layer != "." && !filepath.IsLocal(layer) {
			return nil, fmt.Errorf("走査層が outDir の外を指しています: %s", layer)
		}
		layerDir := filepath.Join(outDir, layer)
		dirs, err := os.ReadDir(layerDir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, d := range dirs {
			if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
				continue
			}
			holder := filepath.Join(layerDir, d.Name())
			// 棚そのものを器と誤認しない（棚をリポジトリ内に置く構成で llm-tpl/<flag> を舐めてしまう）
			if under(filepath.Clean(holder), filepath.Clean(tplHome)) {
				continue
			}
			entries, err := os.ReadDir(holder)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				rel := filepath.Join(layer, d.Name(), e.Name())
				p := filepath.Join(outDir, rel)
				target, err := os.Readlink(p)
				if err != nil {
					continue // symlink でない
				}
				if ownedBy(p, target, tplHome, resolvedHome) {
					out[rel] = target
				}
			}
		}
	}
	return out, nil
}

// ownedBy は symlink の向き先が tplHome 配下かを返す。
// バンドルを削除した後の壊れたリンクも掃除できるよう、字面での判定を先に行う
// （実体解決は tplHome 自身が symlink の場合の保険）。
func ownedBy(linkPath, target, tplHome, resolvedHome string) bool {
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(filepath.Dir(linkPath), target)
	}
	abs = filepath.Clean(abs)
	if under(abs, filepath.Clean(tplHome)) {
		return true
	}
	if resolvedHome == "" {
		return false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	return err == nil && under(resolved, resolvedHome)
}

func under(p, base string) bool {
	return p == base || strings.HasPrefix(p, base+string(os.PathSeparator))
}

// sameContent は 2 つのパスの内容が同一かを返す（ファイル・ディレクトリとも再帰比較）。
func sameContent(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if fa.IsDir() != fb.IsDir() {
		return false, nil
	}
	if !fa.IsDir() {
		ba, err := os.ReadFile(a)
		if err != nil {
			return false, err
		}
		bb, err := os.ReadFile(b)
		if err != nil {
			return false, err
		}
		return bytes.Equal(ba, bb), nil
	}

	ea, err := os.ReadDir(a)
	if err != nil {
		return false, err
	}
	eb, err := os.ReadDir(b)
	if err != nil {
		return false, err
	}
	if len(ea) != len(eb) {
		return false, nil
	}
	for i := range ea {
		if ea[i].Name() != eb[i].Name() {
			return false, nil
		}
		same, err := sameContent(filepath.Join(a, ea[i].Name()), filepath.Join(b, eb[i].Name()))
		if err != nil || !same {
			return same, err
		}
	}
	return true, nil
}
