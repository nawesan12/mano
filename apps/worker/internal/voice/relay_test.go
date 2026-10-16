package voice

import (
	"context"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// peer es la otra punta: hace de WhatsApp o del modelo. Manda un tono y cuenta lo que recibe.
type peer struct {
	pc  *webrtc.PeerConnection
	out *webrtc.TrackLocalStaticRTP
	got chan []byte
}

func newPeer(t *testing.T) *peer {
	t.Helper()
	api, err := newAPI()
	if err != nil {
		t.Fatal(err)
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	out, _ := webrtc.NewTrackLocalStaticRTP(opus, "a", "a")
	if _, err := pc.AddTrack(out); err != nil {
		t.Fatal(err)
	}
	p := &peer{pc: pc, out: out, got: make(chan []byte, 16)}
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			select {
			case p.got <- pkt.Payload:
			default:
			}
		}
	})
	return p
}

// answer responde una oferta del puente (como harían Meta o el modelo).
func (p *peer) answer(t *testing.T, offer string) string {
	t.Helper()
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		t.Fatal(err)
	}
	ans, err := p.pc.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	sdp, err := gathered(context.Background(), p.pc, ans)
	if err != nil {
		t.Fatal(err)
	}
	return sdp
}

// beep manda paquetes hasta que el otro lado reciba algo con ese contenido.
func (p *peer) beep(stop <-chan struct{}, payload []byte) {
	seq := uint16(0)
	for {
		select {
		case <-stop:
			return
		case <-time.After(20 * time.Millisecond):
		}
		seq++
		_ = p.out.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: uint32(seq) * 960}, Payload: payload})
	}
}

func waitFor(t *testing.T, ch <-chan []byte, want string) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case b := <-ch:
			if string(b) == want {
				return
			}
		case <-deadline:
			t.Fatalf("no llegó %q", want)
		}
	}
}

// El audio cruza en los dos sentidos sin transcodificar y los eventos del modelo llegan por el data channel.
func TestBridgeRelaysBothWays(t *testing.T) {
	if testing.Short() {
		t.Skip("WebRTC en loopback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, err := NewBridge(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	phone, brain := newPeer(t), newPeer(t)
	brainEvents := make(chan []byte, 4)
	brain.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() { _ = dc.SendText(`{"type":"session.created"}`) })
		dc.OnMessage(func(m webrtc.DataChannelMessage) { brainEvents <- m.Data })
	})

	offer, err := b.PhoneOffer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetPhoneAnswer(phone.answer(t, offer)); err != nil {
		t.Fatal(err)
	}
	offer, err = b.BrainOffer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetBrainAnswer(brain.answer(t, offer)); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	defer close(stop)
	go phone.beep(stop, []byte("hola-mano"))
	go brain.beep(stop, []byte("hola-duenio"))

	waitFor(t, brain.got, "hola-mano")
	waitFor(t, phone.got, "hola-duenio")
	waitFor(t, b.Events(), `{"type":"session.created"}`)
	if err := b.Send(ctx, map[string]string{"type": "response.create"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, brainEvents, `{"type":"response.create"}`)
}
