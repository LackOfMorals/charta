package config

import "testing"

func TestValidate(t *testing.T) {
	base := func() Config {
		return Config{ListenAddr: ":7474", Username: "u", Password: "p"}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		ok     bool
	}{
		{"valid", func(*Config) {}, true},
		{"no credentials", func(c *Config) { c.Username, c.Password = "", "" }, false},
		{"no password", func(c *Config) { c.Password = "" }, false},
		{"tls cert without key", func(c *Config) { c.TLSCert = "c.pem" }, false},
		{"negative tx timeout", func(c *Config) { c.TxTimeout = -1 }, false},
		{"empty listen", func(c *Config) { c.ListenAddr = "" }, false},
		{"public url ok", func(c *Config) { c.PublicURL = "https://graph.example.com" }, true},
		{"public url without scheme", func(c *Config) { c.PublicURL = "graph.example.com" }, false},
		{"public url wrong scheme", func(c *Config) { c.PublicURL = "ftp://graph.example.com" }, false},
	} {
		c := base()
		tc.mutate(&c)
		if err := c.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
