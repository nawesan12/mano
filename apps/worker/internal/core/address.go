package core

import (
	"net/mail"
	"regexp"
	"strings"
)

// MailAddress saca UNA dirección de un encabezado ("Juan <juan@x.com>"). Vacío si no hay exactamente una válida:
// una lista ("a@x, b@y") o algo con saltos de línea no se acepta, para no mandar a quien no corresponde.
func MailAddress(header string) string {
	if strings.ContainsAny(header, "\r\n") {
		return ""
	}
	a, err := mail.ParseAddress(strings.TrimSpace(header))
	if err != nil || strings.Count(a.Address, "@") != 1 {
		return ""
	}
	return strings.ToLower(a.Address)
}

var phoneInSender = regexp.MustCompile(`\(\+?(\d{8,15})\)`)

// SenderPhone saca el teléfono de "Marta (+5491111111111)", como arma el remitente el webhook de WhatsApp.
func SenderPhone(sender string) string {
	if m := phoneInSender.FindStringSubmatch(sender); m != nil {
		return m[1]
	}
	return ""
}

func isFullAddress(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && at < len(s)-1 && strings.Contains(s[at:], ".") && !strings.ContainsAny(s, " ,<>")
}

func onlyDigits(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// senderMatches compara un criterio de regla con el remitente:
//   - dirección completa ("juan@x.com"): igual a la dirección real, nunca por pedazos;
//   - dominio ("@x.com"): la dirección termina así;
//   - teléfono (solo dígitos): igual al teléfono del remitente;
//   - lo demás ("newsletter", "Liliana"): aparece en el remitente, como antes. Ojo: el nombre visible se falsifica.
func senderMatches(sender, needle string) bool {
	n := strings.ToLower(strings.TrimSpace(needle))
	switch {
	case n == "":
		return false
	case isFullAddress(n):
		return MailAddress(sender) == n
	case strings.HasPrefix(n, "@") && isFullAddress("x"+n):
		return strings.HasSuffix(MailAddress(sender), n)
	case onlyDigits(n):
		p := SenderPhone(sender)
		return p != "" && (p == n || strings.HasSuffix(p, n) || strings.HasSuffix(n, p))
	}
	return strings.Contains(Norm(sender), Norm(n))
}
