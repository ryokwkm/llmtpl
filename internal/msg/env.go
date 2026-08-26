package msg

import "os"

// env は os.Getenv の薄い別名（テストから pick を環境非依存に呼べるようにするため分けている）。
func env(key string) string { return os.Getenv(key) }
