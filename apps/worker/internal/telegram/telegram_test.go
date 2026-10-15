package telegram

import "testing"

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in, cmd, args string
		ok            bool
	}{
		{"/recordar@ManoBot Tema: dato", "recordar", "Tema: dato", true},
		{"/Resumen", "resumen", "", true},
		{"hola", "", "", false},
	}
	for _, c := range cases {
		cmd, args, ok := ParseCommand(c.in)
		if cmd != c.cmd || args != c.args || ok != c.ok {
			t.Errorf("%q → %q %q %v", c.in, cmd, args, ok)
		}
	}
	if !callbackRe.MatchString("ap:00000001-0000-4000-8000-000000000000:ok") || callbackRe.MatchString("ap:x:ok") {
		t.Fatal("callbackRe")
	}
}
