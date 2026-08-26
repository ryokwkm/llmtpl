// Package mergejson は JSON 断片（settings.json 等）の構造マージを担う。
//
// テキストとして差し込むとカンマ・階層が壊れるため、断片ごとに独立した完全な JSON として
// parse し、構造として重ねる。マージ規則:
//
//	オブジェクト … 再帰マージ
//	配列         … 追加 + 重複排除（順序保持。permissions.allow / hooks.<Event> 等）
//	スカラー     … 後勝ち
//	型が違う     … エラー（設定の書き間違いを黙って通さない）
//
// 数値は json.Number として保持するので、120 が 1.2e+02 になるような書き換えは起きない。
package mergejson

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ryokwkm/llmtpl/internal/msg"
)

// Parse は JSON 断片をオブジェクトとして読む。label はエラーメッセージ用の出典表示。
func Parse(content []byte, label string) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber() // 数値の字面を保つ
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf(msg.M.JSON.NotJSON, label, err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(msg.M.JSON.NotObject, label)
	}
	return obj, nil
}

// Format は生成物として書き出す整形（キーはソート・2 スペース・末尾改行）。
func Format(v map[string]any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf(msg.M.JSON.FormatFailed, err)
	}
	return append(b, '\n'), nil
}

// Merge は base に over を重ねた新しいオブジェクトを返す（引数は変更しない）。
// overLabel は型不一致エラーで「どの断片が原因か」を示すための表示名。
func Merge(base, over map[string]any, overLabel string) (map[string]any, error) {
	return mergeObjects(base, over, overLabel, "")
}

func mergeObjects(base, over map[string]any, label, path string) (map[string]any, error) {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = deepCopy(v)
	}
	for k, ov := range over {
		cur, exists := out[k]
		if !exists {
			out[k] = deepCopy(ov)
			continue
		}
		merged, err := mergeValues(cur, ov, label, joinPath(path, k))
		if err != nil {
			return nil, err
		}
		out[k] = merged
	}
	return out, nil
}

func mergeValues(bv, ov any, label, path string) (any, error) {
	bo, bObj := bv.(map[string]any)
	oo, oObj := ov.(map[string]any)
	ba, bArr := bv.([]any)
	oa, oArr := ov.([]any)

	switch {
	case bObj && oObj:
		return mergeObjects(bo, oo, label, path)
	case bArr && oArr:
		return appendUnique(ba, oa)
	case bObj || oObj || bArr || oArr:
		return nil, fmt.Errorf(msg.M.JSON.TypeMismatch,
			label, path, typeName(bv), typeName(ov))
	default:
		return deepCopy(ov), nil // スカラーは後勝ち
	}
}

// appendUnique は base の後ろに over を足し、深い等価で重複を落とす（順序は保持）。
func appendUnique(base, over []any) ([]any, error) {
	out := make([]any, 0, len(base)+len(over))
	seen := map[string]bool{}
	for _, list := range [][]any{base, over} {
		for _, v := range list {
			key, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf(msg.M.JSON.CannotCompare, err)
			}
			if seen[string(key)] {
				continue
			}
			seen[string(key)] = true
			out = append(out, deepCopy(v))
		}
	}
	return out, nil
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopy(val)
		}
		return out
	default:
		return v // スカラー（string / bool / json.Number / nil）は不変
	}
}

func typeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return msg.M.JSON.TypeObject
	case []any:
		return msg.M.JSON.TypeArray
	default:
		return msg.M.JSON.TypeScalar
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
