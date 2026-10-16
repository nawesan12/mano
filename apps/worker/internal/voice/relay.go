// Package voice hace que Mano llame al dueño: WhatsApp Calling (WebRTC) de un lado,
// voz en tiempo real del otro, y el worker en el medio reenviando el audio Opus tal cual.
package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// Media es el puente de audio entre la llamada (phone) y el modelo de voz (brain).
// En producción es Bridge (pion); en los tests, un doble.
type Media interface {
	// PhoneOffer arma nuestra oferta para una llamada saliente.
	PhoneOffer(ctx context.Context) (string, error)
	SetPhoneAnswer(sdp string) error
	// AnswerPhone responde la oferta de una llamada entrante.
	AnswerPhone(ctx context.Context, offer string) (string, error)
	BrainOffer(ctx context.Context) (string, error)
	SetBrainAnswer(sdp string) error
	// Events son los eventos JSON del modelo; Send le manda uno.
	Events() <-chan []byte
	Send(ctx context.Context, v any) error
	// Done se cierra si se cae cualquiera de las dos puntas.
	Done() <-chan struct{}
	Close() error
}

// opus es el único códec: WhatsApp y el modelo hablan Opus, así no hay que transcodificar.
var opus = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}

// Bridge implementa Media con dos PeerConnection de pion y reenvío de RTP.
type Bridge struct {
	phone, brain     *webrtc.PeerConnection
	toPhone, toBrain *webrtc.TrackLocalStaticRTP
	dc               *webrtc.DataChannel
	dcOpen           chan struct{}
	events           chan []byte
	done             chan struct{}
	doneOnce         sync.Once
}

func newAPI() (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: opus, PayloadType: 111}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(m)), nil
}

// NewBridge arma las dos puntas. stun puede venir vacío (servidor con IP pública).
func NewBridge(stun []string) (*Bridge, error) {
	api, err := newAPI()
	if err != nil {
		return nil, err
	}
	cfg := webrtc.Configuration{}
	if len(stun) > 0 {
		cfg.ICEServers = []webrtc.ICEServer{{URLs: stun}}
	}
	b := &Bridge{dcOpen: make(chan struct{}), events: make(chan []byte, 64), done: make(chan struct{})}
	if b.phone, err = api.NewPeerConnection(cfg); err != nil {
		return nil, err
	}
	if b.brain, err = api.NewPeerConnection(cfg); err != nil {
		_ = b.phone.Close()
		return nil, err
	}
	fail := func(err error) (*Bridge, error) { _ = b.Close(); return nil, err }
	if b.toPhone, err = webrtc.NewTrackLocalStaticRTP(opus, "mano", "mano"); err != nil {
		return fail(err)
	}
	if b.toBrain, err = webrtc.NewTrackLocalStaticRTP(opus, "duenio", "duenio"); err != nil {
		return fail(err)
	}
	if _, err = b.phone.AddTrack(b.toPhone); err != nil {
		return fail(err)
	}
	if _, err = b.brain.AddTrack(b.toBrain); err != nil {
		return fail(err)
	}
	// El canal de eventos se crea antes de la oferta para que entre en el SDP.
	if b.dc, err = b.brain.CreateDataChannel("oai-events", nil); err != nil {
		return fail(err)
	}
	var openOnce sync.Once
	b.dc.OnOpen(func() { openOnce.Do(func() { close(b.dcOpen) }) })
	b.dc.OnMessage(func(m webrtc.DataChannelMessage) {
		select {
		case b.events <- m.Data:
		case <-b.done:
		}
	})
	b.phone.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { b.pump(t, b.toBrain) })
	b.brain.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { b.pump(t, b.toPhone) })
	watch := func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			b.finish()
		}
	}
	b.phone.OnConnectionStateChange(watch)
	b.brain.OnConnectionStateChange(watch)
	return b, nil
}

// pump copia los paquetes de una punta a la otra. WriteRTP reescribe SSRC y payload type por cada destino.
func (b *Bridge) pump(from *webrtc.TrackRemote, to *webrtc.TrackLocalStaticRTP) {
	for {
		pkt, _, err := from.ReadRTP()
		if err != nil {
			return
		}
		_ = to.WriteRTP(pkt)
	}
}

func (b *Bridge) finish() { b.doneOnce.Do(func() { close(b.done) }) }

// localOffer crea la oferta y espera a juntar todos los candidatos: Meta y el modelo no aceptan trickle ICE.
func localOffer(ctx context.Context, pc *webrtc.PeerConnection) (string, error) {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	return gathered(ctx, pc, offer)
}

func gathered(ctx context.Context, pc *webrtc.PeerConnection, desc webrtc.SessionDescription) (string, error) {
	wait := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(desc); err != nil {
		return "", err
	}
	select {
	case <-wait:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(10 * time.Second):
		return "", errors.New("no se pudieron juntar candidatos ICE en 10 s")
	}
	return pc.LocalDescription().SDP, nil
}

func (b *Bridge) PhoneOffer(ctx context.Context) (string, error) { return localOffer(ctx, b.phone) }
func (b *Bridge) BrainOffer(ctx context.Context) (string, error) { return localOffer(ctx, b.brain) }

func (b *Bridge) SetPhoneAnswer(sdp string) error {
	return b.phone.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
}

func (b *Bridge) SetBrainAnswer(sdp string) error {
	return b.brain.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
}

func (b *Bridge) AnswerPhone(ctx context.Context, offer string) (string, error) {
	if err := b.phone.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", err
	}
	answer, err := b.phone.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	return gathered(ctx, b.phone, answer)
}

func (b *Bridge) Events() <-chan []byte { return b.events }
func (b *Bridge) Done() <-chan struct{} { return b.done }

func (b *Bridge) Send(ctx context.Context, v any) error {
	select {
	case <-b.dcOpen:
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return errors.New("la llamada terminó")
	case <-time.After(15 * time.Second):
		return errors.New("el canal de eventos del modelo no abrió")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return b.dc.SendText(string(raw))
}

func (b *Bridge) Close() error {
	b.finish()
	var errs []error
	for _, pc := range []*webrtc.PeerConnection{b.phone, b.brain} {
		if pc != nil {
			if err := pc.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cerrando el puente: %w", errors.Join(errs...))
	}
	return nil
}
