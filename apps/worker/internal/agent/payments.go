package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/mercadopago"
	"github.com/nawesan12/mano/worker/internal/store"
)

// Payments es lo que Mano usa de Mercado Pago (mercadopago.Client).
type Payments interface {
	CreateLink(ctx context.Context, title string, amount float64, ref, notifyURL string, ttl time.Duration) (mercadopago.Link, error)
	Refund(ctx context.Context, paymentID string, amount float64, key string) (string, error)
	Payment(ctx context.Context, id string) (mercadopago.Payment, error)
	Recent(ctx context.Context, days int) (mercadopago.Summary, error)
}

// LinkTTL: un link de pago vence a la semana.
const LinkTTL = 7 * 24 * time.Hour

// ChargeRemindAfter: si no pagó en 3 días, se le avisa al dueño (una vez).
const ChargeRemindAfter = 72 * time.Hour

// SetPayments activa los cobros por Mercado Pago. notifyURL puede ir vacío si el webhook está en el panel.
func (a *Agent) SetPayments(p Payments, notifyURL string) { a.pay, a.payNotify = p, notifyURL }

// moneyActions mueven plata: se aprueban solo con el botón, nunca por voz.
// Confiar en una cuenta nueva también: es la puerta de una estafa.
var moneyActions = map[string]bool{"mp.refund": true, "mp.payout": true, "account.trust": true}

func chargeMessage(name, desc string, amount float64, url string) string {
	hi := "Hola"
	if name != "" && !strings.HasPrefix(name, "+") {
		hi += " " + strings.Fields(name)[0]
	}
	return fmt.Sprintf("%s, te paso el link para pagar %s (%s): %s", hi, desc, mercadopago.Money(amount), url)
}

// ProposeCharge deja listo un link de pago para un cliente. Sale solo con la aprobación del dueño.
func (a *Agent) ProposeCharge(ctx context.Context, who string, amount float64, desc string) (string, *store.Approval) {
	if a.pay == nil {
		return "Mercado Pago no está conectado (MP_ACCESS_TOKEN).", nil
	}
	if desc = strings.TrimSpace(desc); desc == "" {
		desc = "tu compra"
	}
	c, problem := a.findContact(ctx, who)
	if problem != "" {
		return problem, nil
	}
	name := c.Name
	if name == "" {
		name = "+" + c.Phone
	}
	preview := chargeMessage(name, desc, amount, "[link de Mercado Pago]")
	ap := store.Approval{Area: "Cobros", Risk: "medio", Title: core.Truncate(fmt.Sprintf("Cobrarle %s a %s: %s", mercadopago.Money(amount), name, desc), 90),
		Detail: "Armo el link (vence en 7 días) y le mando por WhatsApp:\n" + preview,
		Action: store.Action{Type: "mp.link", To: c.Phone, Subject: name, Text: core.Truncate(desc, 200), Amount: amount}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return "No pude dejarlo para que lo apruebes: " + err.Error(), nil
	}
	ap.ID = id
	return "", &ap
}

func (a *Agent) sendCharge(ctx context.Context, ap store.Approval, by string) string {
	if a.pay == nil {
		return "Mercado Pago no está conectado."
	}
	ref := mercadopago.LinkRef + ap.ID
	link, err := a.pay.CreateLink(ctx, ap.Action.Text, ap.Action.Amount, ref, a.payNotify, LinkTTL)
	if err != nil {
		return "No pude armar el link: " + err.Error()
	}
	_ = a.store.InsertCharge(ctx, store.Charge{ID: ref, PrefID: link.ID, Phone: ap.Action.To, Name: ap.Action.Subject,
		Description: ap.Action.Text, Amount: ap.Action.Amount, URL: link.URL, ExpiresAt: &link.Expires})
	summary := fmt.Sprintf("Link de %s a %s (%s)", mercadopago.Money(ap.Action.Amount), ap.Action.Subject, ap.Action.Text)
	if a.wa == nil {
		_ = a.store.Log(ctx, store.LogEntry{Area: "Cobros", Tool: "mp.link", Summary: summary + ", para reenviar", Via: "code", ApprovedBy: by})
		return "Armé el link. Mandáselo vos: " + link.URL
	}
	if err := a.wa.ReplyInWindow(ctx, ap.Action.To, chargeMessage(ap.Action.Subject, ap.Action.Text, ap.Action.Amount, link.URL)); err != nil {
		_ = a.store.Log(ctx, store.LogEntry{Area: "Cobros", Tool: "mp.link", Summary: summary + ", sin enviar", Via: "code", ApprovedBy: by})
		return "Armé el link pero no se lo pude mandar a " + ap.Action.Subject + " (" + err.Error() + "). Mandáselo vos: " + link.URL
	}
	_ = a.store.Log(ctx, store.LogEntry{Area: "Cobros", Tool: "mp.link", Summary: "Mandó " + strings.ToLower(summary[:1]) + summary[1:], Via: "code", ApprovedBy: by})
	return "Le mandé a " + ap.Action.Subject + " el link por " + mercadopago.Money(ap.Action.Amount) + ". Te aviso cuando pague."
}

// ProposeRefund deja lista una devolución. Riesgo alto: se aprueba solo con el botón.
func (a *Agent) ProposeRefund(ctx context.Context, paymentID string, amount float64) (string, *store.Approval) {
	if a.pay == nil {
		return "Mercado Pago no está conectado (MP_ACCESS_TOKEN).", nil
	}
	p, err := a.pay.Payment(ctx, paymentID)
	if err != nil {
		return "No encontré ese pago: " + err.Error(), nil
	}
	if p.Status != "approved" {
		return "Ese pago no está acreditado (" + p.Status + "): no hay nada para devolver.", nil
	}
	if amount <= 0 || amount > p.TransactionAmount {
		amount = p.TransactionAmount
	}
	who := strings.TrimSpace(p.Payer.FirstName + " " + p.Payer.LastName)
	if who == "" {
		who = p.Payer.Email
	}
	kind := "total"
	if amount < p.TransactionAmount {
		kind = "parcial"
	}
	ap := store.Approval{Area: "Cobros", Risk: "alto", Title: core.Truncate(fmt.Sprintf("Devolver %s a %s", mercadopago.Money(amount), who), 90),
		Detail: fmt.Sprintf("Devolución %s del pago %s (%s, %s). Sale de tu saldo de Mercado Pago y no se puede deshacer.", kind, paymentID, mercadopago.Money(p.TransactionAmount), p.Description),
		Action: store.Action{Type: "mp.refund", Ref: paymentID, Subject: who, Amount: amount}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return "No pude dejarlo para que lo apruebes: " + err.Error(), nil
	}
	ap.ID = id
	return "", &ap
}

func (a *Agent) refund(ctx context.Context, ap store.Approval, by string) string {
	if a.pay == nil {
		return "Mercado Pago no está conectado."
	}
	status, err := a.pay.Refund(ctx, ap.Action.Ref, ap.Action.Amount, "mano-refund-"+ap.ID)
	if err != nil {
		return "No pude devolverlo: " + err.Error()
	}
	_ = a.store.Log(ctx, store.LogEntry{Area: "Cobros", Tool: "mp.refund", Summary: fmt.Sprintf("Devolvió %s a %s (pago %s)", mercadopago.Money(ap.Action.Amount), ap.Action.Subject, ap.Action.Ref), Via: "code", ApprovedBy: by})
	return fmt.Sprintf("Listo, devolví %s a %s (%s).", mercadopago.Money(ap.Action.Amount), ap.Action.Subject, status)
}

// Movements resume lo cobrado sin IA: últimos 7 días, hoy y links sin pagar.
func (a *Agent) Movements(ctx context.Context) string {
	if a.pay == nil {
		return "Mercado Pago no está conectado (MP_ACCESS_TOKEN)."
	}
	week, err := a.pay.Recent(ctx, 7)
	if err != nil {
		return "No pude leer los movimientos: " + err.Error()
	}
	today, _ := a.pay.Recent(ctx, 1)
	var b strings.Builder
	fmt.Fprintf(&b, "Últimas 24 h: %d pago(s), %s.\n", today.Count, mercadopago.Money(today.Gross))
	fmt.Fprintf(&b, "Últimos 7 días: %d pago(s), %s cobrado, %s neto", week.Count, mercadopago.Money(week.Gross), mercadopago.Money(week.Net))
	if week.PendingRelease > 0 {
		fmt.Fprintf(&b, " (%s todavía por liberar)", mercadopago.Money(week.PendingRelease))
	}
	b.WriteString(".")
	if week.Refunded > 0 {
		fmt.Fprintf(&b, " Devuelto: %s.", mercadopago.Money(week.Refunded))
	}
	for i, p := range week.Top {
		if i == 0 {
			b.WriteString("\nÚltimos:")
		}
		who := strings.TrimSpace(p.Payer.FirstName + " " + p.Payer.LastName)
		if who == "" {
			who = p.Payer.Email
		}
		if who == "" {
			who = p.Description
		}
		fmt.Fprintf(&b, "\n· %s %s (pago %d)", who, mercadopago.Money(p.TransactionAmount), p.ID)
	}
	if pend, _ := a.store.PendingCharges(ctx); len(pend) > 0 {
		var total float64
		for _, c := range pend {
			total += c.Amount
		}
		fmt.Fprintf(&b, "\nLinks sin pagar: %d (%s).", len(pend), mercadopago.Money(total))
	}
	return b.String()
}

// chargeFollowUp: links sin pagar a los 3 días (una vez) y vencidos. Sin IA. Devuelve líneas para el mensaje.
func (a *Agent) chargeFollowUp(ctx context.Context, now time.Time) []string {
	pend, err := a.store.PendingCharges(ctx)
	if err != nil {
		return nil
	}
	var lines []string
	for _, c := range pend {
		switch {
		case c.ExpiresAt != nil && now.After(*c.ExpiresAt):
			_ = a.store.SetChargeStatus(ctx, c.ID, "expired")
			lines = append(lines, fmt.Sprintf("Venció sin pagar el link de %s a %s (%s).", mercadopago.Money(c.Amount), c.Name, c.Description))
		case c.RemindedAt == nil && !c.CreatedAt.IsZero() && now.Sub(c.CreatedAt) >= ChargeRemindAfter:
			_ = a.store.MarkChargeReminded(ctx, c.ID, now)
			lines = append(lines, fmt.Sprintf("%s todavía no pagó el link de %s (%s), hace %s. Si querés: /llamar %s: recordarle el pago",
				c.Name, mercadopago.Money(c.Amount), c.Description, ago(now.Sub(c.CreatedAt)), c.Name))
		}
	}
	return lines
}
