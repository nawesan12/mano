package core

import (
	"regexp"
	"strings"
)

// ValidCBU comprueba los dos dígitos verificadores de un CBU/CVU (22 dígitos).
func ValidCBU(cbu string) bool {
	if len(cbu) != 22 {
		return false
	}
	d := make([]int, 22)
	for i, r := range cbu {
		if r < '0' || r > '9' {
			return false
		}
		d[i] = int(r - '0')
	}
	check := func(digits []int, weights []int) int {
		sum := 0
		for i, v := range digits {
			sum += v * weights[i%len(weights)]
		}
		return (10 - sum%10) % 10
	}
	// bloque 1: entidad (3) + sucursal (4), verificador en la posición 8; bloque 2: cuenta (13), verificador en la 22
	return check(d[0:7], []int{7, 1, 3, 9, 7, 1, 3}) == d[7] && check(d[8:21], []int{3, 9, 7, 1}) == d[21]
}

var (
	cbuRe   = regexp.MustCompile(`\b(\d[\d\s.-]{20,30}\d)\b`)
	aliasRe = regexp.MustCompile(`(?i)\balias\b(?:\s+(?:cbu|cvu|es|de\s+la\s+cuenta))?\s*[:=]?\s*([a-z0-9]+(?:[.\-][a-z0-9]+){1,4})`)
)

// ExtractAccounts encuentra CBU/CVU válidos y alias en un texto. Devuelve "cbu:<22 dígitos>" o "alias:<alias>".
func ExtractAccounts(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(a string) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, m := range cbuRe.FindAllString(text, -1) {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, m)
		if ValidCBU(digits) {
			add("cbu:" + digits)
		}
	}
	for _, m := range aliasRe.FindAllStringSubmatch(text, -1) {
		add("alias:" + strings.ToLower(m[1]))
	}
	return out
}

// MaskAccount deja ver lo justo para reconocerla: "CBU …6789" o el alias entero (no es secreto).
func MaskAccount(a string) string {
	kind, v, _ := strings.Cut(a, ":")
	if kind == "cbu" && len(v) > 4 {
		return "CBU …" + v[len(v)-4:]
	}
	return "alias " + v
}
