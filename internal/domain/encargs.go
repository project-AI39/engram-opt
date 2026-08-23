package domain

import (
	"fmt"
	"strings"
)

// 管理対象オプション名（ダッシュ無しの正規化名）。
// ユーザー引数は先頭の "-" を全て剥いた名前でこの表と比較し、
// 完全一致または "名前=値" 形式の場合に拒否する（フェイルファスト）。
// 接頭辞マッチにしないのは、"-tune" が "-t" に誤ヒットする類の事故を防ぐため。
var forbiddenExtraArgNames = []string{
	// 品質基準・レート制御（二分探索の中核。上書きされると探索が無意味化する）
	"crf", "qp", "qscale", "b:v", "b", "preset",
	// ストリーム構成・フィルタ（本ツールが管理する領域）
	"c:v", "c:a", "vf", "filter", "filter_complex", "pix_fmt",
	"frames:v", "g", "keyint", "an", "vn", "map",
	// 入出力・コンテナ操作（呼び出し側の専権事項）
	"i", "f", "y", "n", "nostdin", "ss", "to", "t", "copyts",
}

func isForbiddenExtraArg(tok string) bool {
	name := strings.ToLower(strings.TrimLeft(tok, "-"))
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	for _, bad := range forbiddenExtraArgNames {
		if name == bad {
			return true
		}
	}
	return false
}

// ParseExtraArgs はユーザー指定の追加エンコード引数テキストを argv トークンへ分割する。
//
// 区切りは空白のみ（クォート対応は現行スコープ外）。x265-params のような
// コロン区切りオプションは空白を含まないためそのまま1トークンになる。
// 空文字列は追加なし(nil)。トークンは必ず "-" で始まること、
// 禁止接頭辞に触れないことを検証する（フェイルファスト）。
func ParseExtraArgs(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	tokens := strings.Fields(s)
	for _, tok := range tokens {
		if !strings.HasPrefix(tok, "-") {
			// 直前オプションの値（例: -tune の "film"）。検証対象外でそのまま渡す
			continue
		}
		if isForbiddenExtraArg(tok) {
			return nil, fmt.Errorf(
				"extra args %q: option %q is managed by engram-opt (CRF search / chunking contract) and cannot be overridden",
				s, tok)
		}
	}
	return tokens, nil
}
