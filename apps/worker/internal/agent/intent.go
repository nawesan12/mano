package agent

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/nawesan12/mano/worker/internal/core"
)

// actionHint: solo si el texto suena a pedido de acción se gasta el clasificador. Charla normal, gratis.
var actionHint = regexp.MustCompile(`\b(viaj|vacacion|ausent|volvi|cobr|llam|transfer|devol|reembols|record|acorda|anota|avisa|avises|silencio|pagale|pasale|mandale|regla|envio|pendiente|resumen|gasto)`)

const intentSystem = `Traducís un pedido del dueño de un negocio a UNA acción de Mano en JSON. Si no es un pedido de acción, {"accion":"ninguna"}.
Acciones (usá solo estos campos):
- {"accion":"cobrar","quien":"nombre o teléfono","monto":150000,"concepto":"factura 123"}  (mandar un link de pago)
- {"accion":"llamar","quien":"nombre o teléfono","objetivo":"qué decirle"}  (llamar a un cliente)
- {"accion":"llamame"}  (que Mano llame al dueño ahora)
- {"accion":"transferir","destino":"mail o CBU","monto":50000,"motivo":"…","titular":"si lo dice","dni":"si lo dice"}
- {"accion":"devolver","pago":"número de pago","monto":0}  (0 = total)
- {"accion":"recordar","tema":"…","dato":"…"}  (guardar un dato del negocio)
- {"accion":"regla","texto":"el pedido tal cual"}  (cuándo avisar, llamar o ignorar algo)
- {"accion":"silencio","desde":"22:00","hasta":"07:00"}  (horario sin llamadas)
- {"accion":"ausente","hasta":"el lunes | 20/10 | 3 días","mensaje":"para los clientes, solo si lo dicta"}  (viaje, vacaciones, enfermedad)
- {"accion":"volvi"}  (terminó la ausencia)
- {"accion":"consulta","que":"cobros|envios|pendientes|resumen|gasto"}
Montos en pesos como número: "150 lucas" = 150000, "1,5 palos" = 1500000. No inventes datos que no estén.`

type intent struct {
	Accion   string  `json:"accion"`
	Quien    string  `json:"quien"`
	Monto    float64 `json:"monto"`
	Concepto string  `json:"concepto"`
	Objetivo string  `json:"objetivo"`
	Destino  string  `json:"destino"`
	Motivo   string  `json:"motivo"`
	Titular  string  `json:"titular"`
	DNI      string  `json:"dni"`
	Pago     string  `json:"pago"`
	Tema     string  `json:"tema"`
	Dato     string  `json:"dato"`
	Texto    string  `json:"texto"`
	Desde    string  `json:"desde"`
	Hasta    string  `json:"hasta"`
	Que      string  `json:"que"`
	Mensaje  string  `json:"mensaje"`
}

// field limpia lo que devuelve el modelo antes de armar un comando: sin barras, saltos ni largos raros.
func field(s string) string {
	s = strings.NewReplacer("/", " ", "\n", " ", "\r", " ").Replace(s)
	return core.Truncate(strings.Join(strings.Fields(s), " "), 200)
}

func amount(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// command arma el comando equivalente con campos validados. Nunca se ejecuta texto crudo del modelo.
func (i intent) command(original string) (string, bool) {
	who := field(strings.ReplaceAll(i.Quien, ":", " "))
	switch i.Accion {
	case "cobrar":
		if who == "" || i.Monto <= 0 {
			return "", false
		}
		return "/cobrar " + who + " " + amount(i.Monto) + " " + field(i.Concepto), true
	case "llamar":
		if who == "" || field(i.Objetivo) == "" {
			return "", false
		}
		return "/llamar " + who + ": " + field(i.Objetivo), true
	case "llamame":
		return "/llamame", true
	case "transferir":
		dest := strings.ReplaceAll(field(i.Destino), " ", "")
		if dest == "" || i.Monto <= 0 || field(i.Motivo) == "" {
			return "", false
		}
		c := "/transferir " + dest + " " + amount(i.Monto) + " " + field(i.Motivo)
		if t, d := field(i.Titular), field(i.DNI); t != "" && d != "" {
			c += " titular " + t + " dni " + d
		}
		return c, true
	case "devolver":
		p := strings.ReplaceAll(field(i.Pago), " ", "")
		if p == "" {
			return "", false
		}
		c := "/devolver " + p
		if i.Monto > 0 {
			c += " " + amount(i.Monto)
		}
		return c, true
	case "recordar":
		t := field(strings.ReplaceAll(i.Tema, ":", " "))
		if t == "" || field(i.Dato) == "" {
			return "", false
		}
		return "/recordar " + t + ": " + field(i.Dato), true
	case "regla":
		return "/regla " + field(original), true
	case "silencio":
		if field(i.Desde) == "" || field(i.Hasta) == "" {
			return "", false
		}
		return "/silencio " + field(i.Desde) + "-" + field(i.Hasta), true
	case "ausente":
		h := strings.ReplaceAll(field(i.Hasta), ":", " ")
		if h == "" {
			return "", false
		}
		c := "/ausente " + h
		if m := field(i.Mensaje); m != "" {
			c += ": " + m
		}
		return c, true
	case "volvi":
		return "/volvi", true
	case "consulta":
		switch i.Que {
		case "cobros", "envios", "pendientes", "resumen", "gasto":
			return "/" + i.Que, true
		}
	}
	return "", false
}

// Route traduce un pedido en lenguaje natural ("cobrale 150 lucas a Pérez por la factura") al comando que lo hace.
// Lo que mueve plata o habla con clientes sigue saliendo como tarjeta para aprobar: Route solo arma el comando.
func (a *Agent) Route(ctx context.Context, text string) (string, bool) {
	if !actionHint.MatchString(core.Norm(text)) {
		return "", false
	}
	r, err := a.models.Complete(ctx, core.Cheap, core.Request{
		System: intentSystem, Messages: []core.Message{{Role: "user", Content: core.Truncate(text, 600)}}, MaxTokens: 200, JSON: true,
	}, CallOpts{})
	if err != nil {
		return "", false
	}
	var i intent
	if core.ParseJSON(r.Text, &i) != nil {
		return "", false
	}
	return i.command(text)
}
