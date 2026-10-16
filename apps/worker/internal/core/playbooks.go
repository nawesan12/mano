package core

import "strings"

// Playbook es el arranque por rubro: reglas que resuelven sin IA lo típico de ese negocio, misiones y qué conviene contarle a Mano.
type Playbook struct {
	Key, Name string
	Aliases   []string
	Rules     []Rule
	Missions  [][2]string // área, objetivo
	Ask       []string    // datos que conviene que el dueño le cuente (/recordar)
}

func pr(match RuleMatch, u Urgency, action bool, note string) Rule {
	return Rule{Match: match, Outcome: RuleOutcome{Urgency: u, NeedsAction: action, Note: note}}
}

func text(words ...string) RuleMatch { return RuleMatch{TextIncludesAny: words} }

// Playbooks: un arranque razonable, no la verdad. Las reglas del dueño (prioridad 50) van antes que estas (60+).
var Playbooks = []Playbook{
	{
		Key: "inmobiliaria", Name: "Inmobiliaria", Aliases: []string{"inmobiliaria", "inmobiliario", "bienes raices", "corredor", "alquileres", "propiedades"},
		Rules: []Rule{
			pr(text("seña", "reserva de la propiedad", "boleto", "escritura", "firma del contrato"), High, true, "Operación en curso"),
			pr(text("tasacion", "tasar", "quiero vender", "cuanto vale mi"), High, true, "Pedido de tasación: lead para captar"),
			pr(text("visita", "ver la propiedad", "ver el depto", "ver la casa", "para conocer"), High, true, "Pide visitar una propiedad"),
			pr(RuleMatch{SenderIncludesAny: []string{"zonaprop", "argenprop", "properati", "inmuebles"}}, High, true, "Consulta de un portal"),
			pr(text("inquilino", "se rompio", "perdida de agua", "no funciona", "filtracion"), High, true, "Reclamo de inquilino"),
		},
		Missions: [][2]string{{"ventas", "Responder todo lead en menos de una hora"}, {"cobros", "Que no se caiga ningún alquiler sin cobrar"}},
		Ask:      []string{"Honorarios: 3% más IVA", "Zonas: Centro, Playa Grande y Los Troncos", "Horario de visitas: lunes a sábado de 10 a 18"},
	},
	{
		Key: "consultorio", Name: "Consultorio", Aliases: []string{"consultorio", "medico", "medica", "odontologo", "odontologa", "dentista", "psicologo", "psicologa", "kinesiologo", "nutricionista", "salud", "clinica"},
		Rules: []Rule{
			pr(text("urgencia", "dolor fuerte", "sangrado", "no puedo respirar", "me desmaye"), Critical, true, "Posible urgencia de salud: derivar a guardia"),
			pr(text("cancelar el turno", "no puedo ir", "reprogramar", "cambiar el turno"), High, true, "Cancela o reprograma un turno"),
			pr(text("turno", "consulta", "agendar", "disponibilidad"), High, true, "Pide turno"),
			pr(text("obra social", "prepaga", "reintegro"), Normal, true, "Consulta de obra social"),
		},
		Missions: [][2]string{{"agenda", "Que no quede ningún pedido de turno sin respuesta"}, {"agenda", "Recordar los turnos el día anterior"}},
		Ask:      []string{"Obras sociales: OSDE, Swiss Medical y particulares", "Valor de la consulta: $30.000", "Horario: lunes a viernes de 9 a 18"},
	},
	{
		Key: "gastronomia", Name: "Gastronomía", Aliases: []string{"gastronomia", "restaurante", "restaurant", "resto", "bar", "cafe", "cafeteria", "rotiseria", "pizzeria", "comida"},
		Rules: []Rule{
			pr(text("intoxic", "me cayo mal", "un pelo", "cucaracha", "en mal estado"), Critical, true, "Queja de salud: atender ya"),
			pr(text("reserva", "mesa para", "cumpleaños", "festejo"), High, true, "Pide reserva"),
			pr(text("pedido", "delivery", "para llevar", "envio a domicilio"), High, true, "Pedido"),
			pr(text("proveedor", "remito", "lista de precios"), Normal, false, "Proveedor"),
		},
		Missions: [][2]string{{"atencion", "Confirmar reservas y pedidos en el momento"}, {"reputacion", "Que ninguna queja quede sin respuesta"}},
		Ask:      []string{"Horario: martes a domingo de 12 a 16 y de 20 a 00", "Capacidad: 40 cubiertos", "Delivery: hasta 3 km, mínimo $15.000"},
	},
	{
		Key: "ecommerce", Name: "E-commerce", Aliases: []string{"ecommerce", "e-commerce", "tienda online", "tienda", "venta online", "tiendanube", "shopify", "mercado libre"},
		Rules: []Rule{
			pr(text("desconozco la compra", "contracargo", "no reconozco"), Critical, true, "Desconocimiento de compra: riesgo de contracargo"),
			pr(text("no me llego", "no llego", "donde esta mi pedido", "seguimiento", "demora"), High, true, "Reclamo de envío"),
			pr(text("devolucion", "cambio", "talle", "vino fallado", "defectuoso"), High, true, "Cambio o devolución"),
			pr(text("por mayor", "mayorista", "reventa"), High, true, "Lead mayorista"),
		},
		Missions: [][2]string{{"atencion", "Responder todo reclamo de envío en el día"}, {"ventas", "No perder ningún lead mayorista"}},
		Ask:      []string{"Política de cambios: 30 días con ticket", "Envíos: Correo Argentino y moto en CABA", "Mínimo mayorista: 12 unidades"},
	},
	{
		Key: "servicios", Name: "Servicios profesionales", Aliases: []string{"servicios", "profesional", "contador", "contadora", "estudio contable", "abogado", "abogada", "estudio juridico", "agencia", "consultora", "consultor", "arquitecto", "diseñador", "diseñadora", "programador"},
		Rules: []Rule{
			pr(text("vencimiento", "vence hoy", "intimacion", "carta documento", "plazo"), Critical, true, "Vencimiento o plazo legal"),
			pr(text("presupuesto", "cotizacion", "cuanto sale", "cuanto cobras", "honorarios"), High, true, "Pedido de presupuesto"),
			pr(text("reunion", "llamada", "zoom", "meet"), High, true, "Pide una reunión"),
			pr(text("factura", "comprobante de pago", "te transferi"), Normal, false, "Pagos y facturas"),
		},
		Missions: [][2]string{{"ventas", "Responder todo pedido de presupuesto en el día"}, {"cobros", "Que no se caiga ningún cobro"}},
		Ask:      []string{"Honorarios: desde $150.000 por proyecto", "Forma de pago: 50% de anticipo", "Horario: lunes a viernes de 9 a 18"},
	},
}

// FindPlaybook busca el rubro por nombre o alias ("tengo una inmobiliaria", "soy contadora").
func FindPlaybook(s string) *Playbook {
	n := Norm(s)
	for i := range Playbooks {
		for _, a := range Playbooks[i].Aliases {
			if strings.Contains(n, Norm(a)) {
				return &Playbooks[i]
			}
		}
	}
	return nil
}

// PlaybookNames lista los rubros para mostrarlos.
func PlaybookNames() string {
	var out []string
	for _, p := range Playbooks {
		out = append(out, p.Key)
	}
	return strings.Join(out, ", ")
}
