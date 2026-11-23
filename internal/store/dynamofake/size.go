package dynamofake

import (
	"encoding/base64"
	"math"
	"strconv"
	"strings"
)

func parseNum(s string) (float64, bool) { f, err := strconv.ParseFloat(s, 64); return f, err == nil }

func formatNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// sizeOfItem estimates DynamoDB's item size: attribute names plus values. Strings and binary
// count their bytes, numbers about one byte per two digits plus one, other types a small
// constant. It is an estimate; the exact rules matter only near the 1 MB and 400 KB limits.
func sizeOfItem(it map[string]av) int {
	n := 0
	for k, v := range it {
		n += len(k) + sizeOfValue(v)
	}
	return n
}

func sizeOfValue(v av) int {
	for t, x := range v {
		switch t {
		case "S":
			return len(x.(string))
		case "B":
			b, _ := base64.StdEncoding.DecodeString(x.(string))
			return len(b)
		case "N":
			return len(strings.TrimLeft(x.(string), "-+"))/2 + 1
		case "BOOL", "NULL":
			return 1
		case "L":
			n := 3
			for _, e := range x.([]any) {
				n += 1 + sizeOfValue(e.(map[string]any))
			}
			return n
		case "M":
			n := 3
			for k, e := range x.(map[string]any) {
				n += 1 + len(k) + sizeOfValue(e.(map[string]any))
			}
			return n
		case "SS", "NS", "BS":
			n := 0
			for _, e := range x.([]any) {
				n += len(e.(string))
			}
			return n
		}
	}
	return 0
}
