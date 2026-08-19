package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_無ければ空(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), ".claude"))
	if err != nil {
		t.Fatalf("失敗: %v", err)
	}
	if s.Get("settings.json") != "" {
		t.Errorf("空でない: %+v", s)
	}
}

func TestSaveとLoadの往復(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude")

	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Set("settings.json", "abc123")
	if err := Save(dir, s); err != nil {
		t.Fatalf("Save が失敗: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("settings.json") != "abc123" {
		t.Errorf("往復で値が変わった: %+v", got)
	}
	if got.Version != Version {
		t.Errorf("version が不正: %d", got.Version)
	}
}

func TestSave_親ディレクトリを作る(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", ".claude")
	s := State{}
	s.Set("a", "1")

	if err := Save(dir, s); err != nil {
		t.Fatalf("失敗: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Errorf("state ファイルが無い: %v", err)
	}
}

// 壊れた state を黙って無視すると「外部の書き込み」を毎回誤検知するので、明示的に落とす。
func TestLoad_壊れたJSONはエラー(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{壊れている"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil {
		t.Error("壊れた state がエラーになりません")
	}
}

// 新しい形式を古いバイナリが誤読すると「外部の書き込み」と誤判定して退避を撒くので落とす
func TestLoad_新しいversionはエラー(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName),
		[]byte(`{"version": 99, "generated": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil {
		t.Error("新しい version がエラーになりません")
	}
}

func TestSet_同じキーは上書き(t *testing.T) {
	var s State
	s.Set("a", "1")
	s.Set("a", "2")
	if s.Get("a") != "2" {
		t.Errorf("上書きされていない: %+v", s)
	}
}

func TestSave_整形が決定的(t *testing.T) {
	dir := t.TempDir()
	s := State{}
	s.Set("z.json", "1")
	s.Set("a.json", "2")

	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, FileName))
	if string(first) != string(second) {
		t.Errorf("保存内容が非決定的:\n%s\n%s", first, second)
	}
}
