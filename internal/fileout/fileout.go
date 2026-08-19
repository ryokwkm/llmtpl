// Package fileout は生成物の書き出しを担う。
//
// 守る性質:
//   - 原子性: 一時ファイル（<dest>.tmp.*）へ書いてから rename する。途中で失敗しても既存を壊さない
//   - 冪等性: 内容が同一なら書き込まない（mtime も動かさない）
//   - 手書きの保全: 既存が「生成物でない」（GeneratedMarker を先頭行に持たない）かつ内容が違う場合は
//     ArchiveDir へ退避してから上書きする。黙って人の編集を消さない
//   - DryRun: 何も書かずに「変わるか / 退避が必要か」だけ返す（--dry-run / check 用）
//
// GeneratedMarker が空文字のときは既存を常に「生成物でない」と扱う（先頭コメントを書けない
// JSON 等はこちら。差分があれば必ず退避される安全側の挙動）。
package fileout

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Options は書き出しの設定。
type Options struct {
	ArchiveDir      string // 手書き実体の退避先ディレクトリ
	ArchiveLabel    string // 退避ファイル名の基（空なら dest の basename）。呼び出し側が一意になる名前を渡す
	DryRun          bool   // true なら書き込まない
	GeneratedMarker string // 先頭行に含まれていれば「生成物」と判定する文字列（空なら常に非生成物扱い）
	KnownHash       string // 既存内容のハッシュがこれと一致すれば「前回の生成物」と扱う（JSON 等マーカを埋められない生成物用）
}

// Result は書き出しの結果。
type Result struct {
	Changed      bool   // 内容が変わった（新規作成を含む）
	Archived     string // 実際に退避したファイルのパス（退避しなければ空）
	WouldArchive bool   // DryRun 時: 退避が必要だった
}

// Write は content を dest へ原子的に書き出す。
func Write(dest string, content []byte, o Options) (Result, error) {
	var res Result

	existing, readErr := os.ReadFile(dest)
	exists := readErr == nil
	same := exists && bytes.Equal(existing, content)
	res.Changed = !same
	// 「前回自分が書いたもの」と判断できる根拠は 2 つ: 先頭行のマーカ（Markdown）と
	// 内容ハッシュの一致（JSON 等コメントを書けない生成物）。どちらでもなければ手書き扱い。
	knownGenerated := isGenerated(existing, o.GeneratedMarker) ||
		(o.KnownHash != "" && Hash(existing) == o.KnownHash)
	needArchive := exists && !same && !knownGenerated

	if o.DryRun {
		res.WouldArchive = needArchive
		return res, nil
	}
	if same {
		return res, nil // 冪等: 触らない
	}

	// 退避を先に済ませる（失敗したら書き込みに進まない = 手書きを失わない）
	if needArchive {
		label := o.ArchiveLabel
		if label == "" {
			label = filepath.Base(dest)
		}
		backup, err := ArchivePath(o.ArchiveDir, label)
		if err != nil {
			return res, err
		}
		if err := os.WriteFile(backup, existing, 0o644); err != nil {
			return res, fmt.Errorf("退避に失敗: %w", err)
		}
		res.Archived = backup
	}

	if err := WriteAtomic(dest, content, 0o644); err != nil {
		return res, err
	}
	return res, nil
}

// WriteAtomic は content を dest へ原子的に書き出す（一時ファイル → rename）。
// 途中で失敗しても既存を壊さない。生成物の判定・退避を伴う書き出しは Write を使い、
// 状態ファイルのように判定が不要なものはこれを直接使う（原子性の実装を 1 か所に保つ）。
func WriteAtomic(dest string, content []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("%s: 親ディレクトリを作れません: %w", dest, err)
	}
	// CreateTemp なら中断した実行の残骸と衝突しない（pid 決め打ちは踏む余地がある）
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".tmp.*")
	if err != nil {
		return fmt.Errorf("%s: 一時ファイルを作れません: %w", dest, err)
	}
	name := tmp.Name()
	defer os.Remove(name) // 成功時は rename 済みなので無害

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("%s: 一時ファイルを書けません: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%s: 一時ファイルを閉じられません: %w", name, err)
	}
	if err := os.Chmod(name, perm); err != nil {
		return fmt.Errorf("%s: 権限を設定できません: %w", name, err)
	}
	if err := os.Rename(name, dest); err != nil {
		return fmt.Errorf("%s: 置き換えに失敗: %w", dest, err)
	}
	return nil
}

// ArchivePath は退避先の**衝突しない**パスを返し、退避先ディレクトリを作る。
// 退避の命名規則をこの 1 か所に集約する（link パッケージからも使う）。
//
// タイムスタンプは秒精度なので、退避先が全ターゲット共通の場合、label が同じだと
// 同一秒の退避が上書きされて「守ったはずの手編集」が消える。label は呼び出し側が
// 一意になるように付け、それでも衝突したら連番を付ける。
func ArchivePath(archiveDir, label string) (string, error) {
	if archiveDir == "" {
		return "", fmt.Errorf("%s: 生成物でない実体があるのに退避先（ArchiveDir）が未設定です", label)
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("退避先を作れません: %w", err)
	}
	base := label + ".bk." + time.Now().Format("20060102150405")
	for i := 1; i <= 100; i++ {
		p := filepath.Join(archiveDir, base)
		if i > 1 {
			p = filepath.Join(archiveDir, fmt.Sprintf("%s-%d", base, i))
		}
		if _, err := os.Lstat(p); err != nil {
			return p, nil // 空いている
		}
	}
	return "", fmt.Errorf("退避先の名前が埋まっています: %s/%s", archiveDir, base)
}

// Hash は内容のハッシュ（sha256 の 16 進）を返す。前回の生成物かどうかの判定に使う。
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// isGenerated は content の先頭行に marker が含まれるかを返す（marker が空なら常に false）。
func isGenerated(content []byte, marker string) bool {
	if marker == "" {
		return false
	}
	first := content
	if nl := bytes.IndexByte(content, '\n'); nl >= 0 {
		first = content[:nl]
	}
	return bytes.Contains(first, []byte(marker))
}
