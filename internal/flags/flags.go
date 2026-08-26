// Package flags はフラグ設定ファイル（`key = value` の平坦な conf）の解析とマージを担う。
//
// 2 層構成:
//   - バンドルルートの defaults.conf … 全ターゲット共通の既定値
//   - 各ターゲットの llmtpl.conf … そのディレクトリで変えるフラグだけ（後勝ち）
//
// 値は true / false のみ。**唯一の例外が予約キー**（現在は bundle_root だけ）で、これは
// パスを値に取る。フラグの母集合はバンドルディレクトリの一覧なので、conf 側に母集合を
// 宣言する必要はない（未知のフラグ名は Validate がエラーにする）。
//
// **不変条件: 予約キーは Set に入らない**。Conf.Flags と Conf.BundleRoot は名前空間が別で、
// Validate も Root.Flags の母集合もこれを前提に無改造で成立している。ここを崩すと
// 「未知のフラグ: bundle_root」で全ターゲットが落ち、テンプレからもフラグ変数として見えてしまう。
//
// セクション（[global] 等）は書けない。階層を持つ conf をそのまま持ち込むとセクション内の
// 上書きが黙って失われるため、`[` 始まりの行は明示的にエラーにする。
package flags

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ryokwkm/llmtpl/internal/msg"
)

// Set はフラグ名 → 真偽値。
type Set map[string]bool

// KeyBundleRoot はバンドルルートの場所を conf から指定する予約キー。
//
// CLI 側は歴史的経緯で --tpl-home だが、そちらは deprecate できる。conf キーは他人の
// リポジトリに書かれたら回収できないので、最初から概念名（バンドルルート）に揃える。
const KeyBundleRoot = "bundle_root"

// IsReserved はフラグ名ではなく予約キーとして解釈されるキーかを返す。
// バンドル名の衝突ガード（同名ディレクトリを作られると永久に ON にできなくなる）からも使う。
func IsReserved(key string) bool { return key == KeyBundleRoot }

// Conf は conf 1 ファイルの解析結果。
// BundleRoot は解決済みの絶対パス（未指定ならゼロ値）。Line は表示用の行番号。
type Conf struct {
	Flags          Set
	BundleRoot     string
	BundleRootLine int
}

// ParseConf は conf を読む。ファイルが無ければ空の Conf を返す（未設定 = 既定のみ、は正常）。
// 不正な行は行番号付きでエラー（静かな素通しを防ぐ）。
func ParseConf(path string) (Conf, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Conf{Flags: Set{}}, nil
		}
		return Conf{}, err
	}
	defer f.Close()

	out := Conf{Flags: Set{}}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		// bufio.Scanner は行末 \n を落とす。CRLF の \r は TrimSpace が落とす
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		// セクションを弾くのは移行時の事故対策（階層のある conf を平坦なパーサへ流し込むと、
		// セクション内の上書きが黙って失われる）
		if strings.HasPrefix(text, "[") {
			return Conf{}, fmt.Errorf(msg.M.Flags.Section, path, line, text)
		}
		key, val, ok := strings.Cut(text, "=")
		if !ok {
			return Conf{}, fmt.Errorf(msg.M.Flags.NoEquals, path, line, text)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			return Conf{}, fmt.Errorf(msg.M.Flags.EmptyKey, path, line, text)
		}
		// **分岐はキー名で行う**。「値が true/false でないなら予約キー」と書くと
		// `commit = mayb` のようなタイポが全部パスとして受理され、下の検証が丸ごと死ぬ
		if IsReserved(key) {
			resolved, err := resolveConfPath(val, path, line, key)
			if err != nil {
				return Conf{}, err
			}
			out.BundleRoot, out.BundleRootLine = resolved, line // 重複は後勝ち（真偽値と同規約）
			continue
		}
		switch val {
		case "true":
			out.Flags[key] = true
		case "false":
			out.Flags[key] = false
		default:
			return Conf{}, fmt.Errorf(msg.M.Flags.BoolOnly, path, line, key, val)
		}
	}
	if err := sc.Err(); err != nil {
		return Conf{}, fmt.Errorf(msg.M.Flags.ReadFailed, path, err)
	}
	return out, nil
}

// resolveConfPath は予約キーの値を絶対パスへ解決する。
//
// 相対パスの基準は **conf ファイルのあるディレクトリ**（cwd 基準にすると実行場所で結果が変わる）。
// symlink は解決しない —— link の所有権判定が字面ベースなので、片方だけ実体解決すると噛み合わなくなる。
func resolveConfPath(val, path string, line int, key string) (string, error) {
	switch val {
	case "":
		return "", fmt.Errorf(msg.M.Flags.EmptyValue, path, line, key)
	case "true", "false":
		return "", fmt.Errorf(msg.M.Flags.PathKeyNotBool, path, line, key, key, val)
	}
	// $VAR は展開しない（未定義変数が黙って空になり、壊れた相対パスを作る）
	if val == "~" || strings.HasPrefix(val, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf(msg.M.Flags.TildeExpandFailed, path, line, key, err)
		}
		val = filepath.Join(home, strings.TrimPrefix(val, "~"))
	} else if strings.HasPrefix(val, "~") {
		return "", fmt.Errorf(msg.M.Flags.TildeUserForm, path, line, key, val)
	}
	if !filepath.IsAbs(val) {
		val = filepath.Join(filepath.Dir(path), val)
	}
	return filepath.Clean(val), nil
}

// Merge は base に over を後勝ちで重ねた新しい Set を返す（引数は変更しない）。
func Merge(base, over Set) Set {
	out := make(Set, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// Validate は known（存在するバンドル名の集合）に無いフラグが指定されていればエラーを返す。
// タイポや削除済みバンドルの参照を静かに無視しないための門番。
func Validate(s Set, known map[string]bool, srcLabel string) error {
	if len(s) == 0 {
		return nil
	}
	var unknown []string
	for _, name := range slices.Sorted(maps.Keys(s)) { // 列挙は決定的
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	avail := msg.M.Flags.NoBundles
	if len(known) > 0 {
		avail = strings.Join(slices.Sorted(maps.Keys(known)), ", ")
	}
	return fmt.Errorf(msg.M.Flags.UnknownFlag,
		srcLabel, strings.Join(unknown, ", "), avail)
}
