package owner

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nawesan12/mano/worker/internal/store"
	"github.com/nawesan12/mano/worker/internal/testutil"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in, cmd, args string
		ok            bool
	}{
		{"/recordar@ManoBot Tema: dato", "recordar", "Tema: dato", true},
		{" /Resumen ", "resumen", "", true},
		{"hola", "", "", false},
	}
	for _, c := range cases {
		cmd, args, ok := ParseCommand(c.in)
		if cmd != c.cmd || args != c.args || ok != c.ok {
			t.Errorf("%q → %q %q %v", c.in, cmd, args, ok)
		}
	}
	if !DecisionRe.MatchString("ap:00000001-0000-4000-8000-000000000000:ok") || DecisionRe.MatchString("ap:x:ok") {
		t.Fatal("DecisionRe")
	}
}

func TestCallPolicyCommands(t *testing.T) {
	db := testutil.NewFakeRest(nil)
	srv := httptest.NewServer(db)
	defer srv.Close()
	s := &Service{Store: store.New(srv.URL, "service", testutil.Tenant)}
	var said []string
	say := func(t string) { said = append(said, t) }
	ctx := context.Background()

	s.callPolicy(ctx, "silencio", "22 a 7", say)
	s.callPolicy(ctx, "llamadas", "5", say)
	p, _ := s.Store.CallPolicy(ctx)
	if p.QuietFrom != "22:00" || p.QuietTo != "07:00" || p.MaxPerDay != 5 || !p.Enabled {
		t.Fatalf("política: %+v · %v", p, said)
	}
	s.callPolicy(ctx, "llamadas", "off", say)
	if p, _ = s.Store.CallPolicy(ctx); p.Enabled {
		t.Fatal("off apaga las llamadas")
	}
	s.callPolicy(ctx, "llamadas", "muchas", say)
	if !strings.Contains(said[len(said)-1], "Usalo así") {
		t.Fatal(said)
	}
}
