package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/mercadopago"
	"github.com/nawesan12/mano/worker/internal/store"
)

// Transfers manda plata desde la cuenta (mercadopago.Payouts).
type Transfers interface {
	Send(ctx context.Context, ref, description string, d mercadopago.Destination, amount float64, notifyURL string) (mercadopago.PayoutResult, error)
	Status(ctx context.Context, payoutID string) (mercadopago.PayoutResult, error)
}

// TransferLimits son topes duros en pesos, además de la aprobación con botón.
type TransferLimits struct {
	PerOp, PerDay float64
}

// SetTransfers activa las transferencias. Sin esto, /transferir avisa que están apagadas.
func (a *Agent) SetTransfers(t Transfers, limits TransferLimits, notifyURL string) {
	a.transfers, a.transferLimits, a.transferNotify = t, limits, notifyURL
}

func (a *Agent) checkLimits(ctx context.Context, amount float64) string {
	l := a.transferLimits
	if amount > l.PerOp {
		return fmt.Sprintf("supera el tope por transferencia (%s)", mercadopago.Money(l.PerOp))
	}
	today, err := a.store.PayoutsToday(ctx, a.loc)
	if err != nil {
		return "no pude leer lo transferido hoy"
	}
	if today+amount > l.PerDay {
		return fmt.Sprintf("con esta te pasás del tope diario (%s; hoy ya van %s)", mercadopago.Money(l.PerDay), mercadopago.Money(today))
	}
	return ""
}

// ProposePayout deja lista una transferencia. Riesgo alto, topes duros y solo con el botón.
func (a *Agent) ProposePayout(ctx context.Context, d mercadopago.Destination, amount float64, reason string) (string, *store.Approval) {
	if a.transfers == nil {
		return "Las transferencias están apagadas. Mercado Pago tiene que habilitarlas para tu cuenta (ver README: Transferencias).", nil
	}
	if err := d.Validate(); err != nil {
		return "No la armé: " + err.Error() + ".", nil
	}
	if reason == "" {
		return "Decime el motivo de la transferencia (queda en el registro).", nil
	}
	if why := a.checkLimits(ctx, amount); why != "" {
		return "No la armé: " + why + ".", nil
	}
	dest, _ := json.Marshal(d)
	ap := store.Approval{Area: "Transferencias", Risk: "alto", Title: core.Truncate(fmt.Sprintf("Transferir %s a %s", mercadopago.Money(amount), d.String()), 90),
		Detail: fmt.Sprintf("Motivo: %s\nSale de tu saldo de Mercado Pago y no se puede deshacer. Se aprueba solo con este botón.", reason),
		Action: store.Action{Type: "mp.payout", Ref: string(dest), Subject: d.String(), Text: core.Truncate(reason, 100), Amount: amount}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return "No pude dejarla para que la apruebes: " + err.Error(), nil
	}
	ap.ID = id
	return "", &ap
}

func (a *Agent) payout(ctx context.Context, ap store.Approval, by string) string {
	if a.transfers == nil {
		return "Las transferencias están apagadas."
	}
	var d mercadopago.Destination
	if err := json.Unmarshal([]byte(ap.Action.Ref), &d); err != nil {
		return "El destino estaba roto, no transferí nada."
	}
	if why := a.checkLimits(ctx, ap.Action.Amount); why != "" { // se vuelve a mirar: pudo haber otras en el medio
		return "No transferí: " + why + "."
	}
	ref := "mano-payout-" + ap.ID
	res, err := a.transfers.Send(ctx, ref, ap.Action.Text, d, ap.Action.Amount, a.transferNotify)
	if err != nil {
		_ = a.store.Log(ctx, store.LogEntry{Area: "Transferencias", Tool: "mp.payout", Summary: "Falló la transferencia a " + d.String() + ": " + core.Truncate(err.Error(), 120), Via: "code", ApprovedBy: by})
		return "No pude transferir: " + err.Error()
	}
	_ = a.store.InsertPayout(ctx, store.Payout{ID: ref, PayoutID: res.ID, Destination: d.String(), Amount: ap.Action.Amount, Reason: ap.Action.Text, Status: res.Status})
	_ = a.store.Log(ctx, store.LogEntry{Area: "Transferencias", Tool: "mp.payout", Summary: fmt.Sprintf("Transfirió %s a %s (%s)", mercadopago.Money(ap.Action.Amount), d.String(), ap.Action.Text), Via: "code", ApprovedBy: by})
	return fmt.Sprintf("Transferencia de %s a %s en camino. Te aviso cuando se acredite.", mercadopago.Money(ap.Action.Amount), d.String())
}

// CheckPayouts consulta las transferencias sin resultado y devuelve los avisos de las que terminaron.
func (a *Agent) CheckPayouts(ctx context.Context) []string {
	if a.transfers == nil {
		return nil
	}
	open, err := a.store.OpenPayouts(ctx)
	if err != nil {
		return nil
	}
	var lines []string
	for _, p := range open {
		if p.PayoutID == "" {
			continue
		}
		res, err := a.transfers.Status(ctx, p.PayoutID)
		if err != nil {
			continue
		}
		final, ok := mercadopago.PayoutFinal(res.Status)
		if !final {
			if res.Status != p.Status {
				_ = a.store.SetPayoutStatus(ctx, p.ID, res.Status)
			}
			continue
		}
		_ = a.store.ClosePayout(ctx, p.ID, res.Status)
		if ok {
			lines = append(lines, fmt.Sprintf("Se acreditó la transferencia de %s a %s.", mercadopago.Money(p.Amount), p.Destination))
		} else {
			lines = append(lines, fmt.Sprintf("Falló la transferencia de %s a %s (%s): la plata no salió o volvió.", mercadopago.Money(p.Amount), p.Destination, res.Status))
		}
	}
	return lines
}
