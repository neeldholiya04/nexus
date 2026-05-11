package memory

import (
	"math"
	"strings"
)

func FTSQuerySafe(q string) string {
	var safe strings.Builder
	for _, ch := range q {
		switch ch {
		case '"', '\'', '(', ')', '*', '^', '-', '+', '~':
			safe.WriteRune(' ')
		default:
			safe.WriteRune(ch)
		}
	}
	return `"` + safe.String() + `"`
}

func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	score := dot / (math.Sqrt(na) * math.Sqrt(nb))
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
