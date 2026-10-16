package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nawesan12/mano/worker/internal/arca"
	"github.com/nawesan12/mano/worker/internal/core"
	"github.com/nawesan12/mano/worker/internal/store"
)

// Invoicer emite comprobantes en ARCA (arca.Client).
type Invoicer interface {
	Authorize(ctx context.Context, inv arca.Invoice) (arca.Result, error)
	QRURL(inv arca.Invoice, r arca.Result) string
	Label(cbteTipo, number int) string
}

// InvoiceConfig: Fase 0 = Factura C (monotributo) a consumidor final.
type InvoiceConfig struct {
	CbteTipo, Concepto int
	CFMax              float64 // desde este monto ARCA exige identificar al consumidor final
	AutoPropose        bool    // proponer la factura al acreditarse un pago de Mercado Pago
}

func (a *Agent) SetInvoicer(i Invoicer, cfg InvoiceConfig) { a.invoicer, a.invoiceCfg = i, cfg }

// ProposeInvoice deja la factura para aprobar. dni es opcional (obligatorio desde CFMax).
func (a *Agent) ProposeInvoice(ctx context.Context, amount float64, customer, dni, concept, paymentRef string) (string, *store.Approval) {
	if a.invoicer == nil {
		return "La facturación con ARCA no está configurada (ver README: ARCA).", nil
	}
	if amount <= 0 {
		return "Decime el monto a facturar.", nil
	}
	dni = digitsOnly(dni)
	if dni == "" && a.invoiceCfg.CFMax > 0 && amount >= a.invoiceCfg.CFMax {
		return fmt.Sprintf("Desde %s ARCA pide el DNI del cliente: /facturar %s %s dni 12345678", pesos(a.invoiceCfg.CFMax), strconv.FormatFloat(amount, 'f', -1, 64), customer), nil
	}
	if paymentRef != "" && a.store.InvoicedPayment(ctx, paymentRef) {
		return "Ese pago ya está facturado.", nil
	}
	if concept = strings.TrimSpace(concept); concept == "" {
		concept = "Venta"
	}
	if customer = strings.TrimSpace(customer); customer == "" {
		customer = "Consumidor final"
	}
	to := "consumidor final"
	if dni != "" {
		to = "DNI " + dni
	}
	ap := store.Approval{Area: "Facturación", Risk: "medio", Title: core.Truncate(fmt.Sprintf("Facturar %s a %s", pesos(amount), customer), 90),
		Detail: fmt.Sprintf("Factura C a %s, concepto: %s. Queda informada en ARCA y no se puede borrar (solo anular con nota de crédito).", to, concept),
		Action: store.Action{Type: "arca.invoice", Amount: amount, Subject: customer, Text: core.Truncate(concept, 100), Ref: paymentRef, To: dni}}
	id, err := a.store.CreateApproval(ctx, ap)
	if err != nil {
		return "No pude dejarla para que la apruebes: " + err.Error(), nil
	}
	ap.ID = id
	return "", &ap
}

func (a *Agent) issueInvoice(ctx context.Context, ap store.Approval, by string) string {
	if a.invoicer == nil {
		return "La facturación con ARCA no está configurada."
	}
	cfg := a.invoiceCfg
	inv := arca.Invoice{CbteTipo: cfg.CbteTipo, Concepto: cfg.Concepto, Total: ap.Action.Amount, DocTipo: 99, Date: time.Now()}
	if ap.Action.To != "" {
		inv.DocTipo, inv.DocNro = 96, mustInt(ap.Action.To)
	}
	r, err := a.invoicer.Authorize(ctx, inv)
	if err != nil {
		_ = a.store.Log(ctx, store.LogEntry{Area: "Facturación", Tool: "arca.invoice", Summary: "No se pudo facturar a " + ap.Action.Subject + ": " + core.Truncate(err.Error(), 150), Via: "code", ApprovedBy: by})
		return "No pude facturar: " + err.Error()
	}
	label := a.invoicer.Label(cfg.CbteTipo, r.Number)
	qr := a.invoicer.QRURL(inv, r)
	_ = a.store.InsertInvoice(ctx, store.InvoiceRow{Label: label, CbteTipo: cfg.CbteTipo, Number: r.Number, CAE: r.CAE, CAEExpires: r.Expires,
		Customer: ap.Action.Subject, Concept: ap.Action.Text, Total: ap.Action.Amount, PaymentRef: ap.Action.Ref, QR: qr})
	_ = a.store.Log(ctx, store.LogEntry{Area: "Facturación", Tool: "arca.invoice", Summary: fmt.Sprintf("%s a %s por %s (CAE %s)", label, ap.Action.Subject, pesos(ap.Action.Amount), r.CAE), Via: "code", ApprovedBy: by})
	vto := r.Expires
	if len(vto) == 8 {
		vto = vto[6:8] + "/" + vto[4:6]
	}
	return fmt.Sprintf("%s emitida por %s. CAE %s (vence %s).\nVerificala en ARCA: %s", label, pesos(ap.Action.Amount), r.CAE, vto, qr)
}

func mustInt(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

// maybeProposeInvoice: al acreditarse un pago, la factura queda lista para aprobar con un toque.
func (a *Agent) maybeProposeInvoice(ctx context.Context, ev core.Event) {
	if a.invoicer == nil || !a.invoiceCfg.AutoPropose || ev.Source != "mercadopago" || ev.Kind != "payment.approved" || ev.Amount == nil {
		return
	}
	who := speakableName(ev.Sender)
	ref := ""
	if id, ok := ev.Payload["paymentId"]; ok {
		ref = "mp:" + fmt.Sprint(id)
	}
	if _, ap := a.ProposeInvoice(ctx, *ev.Amount, who, "", ev.Subject, ref); ap != nil {
		_ = a.notify.ApprovalCard(ctx, *ap)
	}
}
