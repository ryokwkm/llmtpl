// Package state はターゲットごとの llmtpl の記録（生成物と並ぶ .llmtpl-state.json）を扱う。
//
// 用途は 1 つだけ: **前回書いた生成物の内容ハッシュ**を覚えておくこと。
// Markdown は先頭行の GENERATED マーカで「自分が書いたもの」と判定できるが、JSON には
// コメントが書けない。ハッシュが一致すれば前回の生成物、違えば外部が書き換えた（= 退避して警告）
// と判断する。
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ryokwkm/llmtpl/internal/fileout"
)

// FileName は state ファイル名（ターゲットのルート直下に置く）。
const FileName = ".llmtpl-state.json"

// Version は state 形式のバージョン。
//
// 2: キーが**ターゲット相対パス**（.claude/settings.json）。置き場もターゲットのルート直下。
// 1: キーが basename（settings.json）。置き場は .claude/ の中。
//
// **この番号が新旧バイナリの唯一のキルスイッチ**。version 2 の state を古いバイナリが読むと
// 下の Version 比較で loud に落ちるので、レイアウトだけ移行してバイナリを更新し忘れた環境で
// 生成物が黙って壊れることがない（実測: 旧バイナリは exit 1・生成物もリンクも無傷・退避ゼロ）。
const Version = 2

// State は生成物名 → 前回書いた内容のハッシュ。
type State struct {
	Version   int               `json:"version"`
	Generated map[string]string `json:"generated"`
}

// Load は state を読む。無ければ空の State を返す（初回は正常）。
func Load(claudeDir string) (State, error) {
	p := filepath.Join(claudeDir, FileName)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{Version: Version, Generated: map[string]string{}}, nil
		}
		return State{}, fmt.Errorf("%s を読めません: %w", p, err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		// 黙って無視すると「外部の書き込み」を毎回誤検知して退避を繰り返すため、明示的に落とす
		return State{}, fmt.Errorf("%s が壊れています（削除すれば次回 apply で作り直します）: %w", p, err)
	}
	if s.Version > Version {
		// 新しい形式を古いバイナリが誤読すると「外部の書き込み」と誤判定して退避を撒く
		return State{}, fmt.Errorf("%s は新しい形式です（version %d > %d）。llmtpl を更新してください", p, s.Version, Version)
	}
	if s.Generated == nil {
		s.Generated = map[string]string{}
	}
	return s, nil
}

// Save は state を原子的に書き出す。
func Save(claudeDir string, s State) error {
	if s.Version == 0 {
		s.Version = Version
	}
	if s.Generated == nil {
		s.Generated = map[string]string{}
	}
	b, err := json.MarshalIndent(s, "", "  ") // キーはソートされるので出力は決定的
	if err != nil {
		return err
	}
	b = append(b, '\n')

	// 原子書き込みの実装は fileout に 1 本だけ置く（設計 doc §7 の安全機構を二重定義しない）
	return fileout.WriteAtomic(filepath.Join(claudeDir, FileName), b, 0o644)
}

// Set は生成物のハッシュを記録する。
func (s *State) Set(name, hash string) {
	if s.Generated == nil {
		s.Generated = map[string]string{}
	}
	s.Generated[name] = hash
}

// Get は記録済みハッシュを返す（無ければ空）。
func (s State) Get(name string) string { return s.Generated[name] }
