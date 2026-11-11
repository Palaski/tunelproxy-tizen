package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProxyPontaAPonta(t *testing.T) {
	// Servidor B: CDN de vídeo, com HLS de caminhos relativos.
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stream.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			w.Write([]byte(strings.Repeat("G", 200000)))
		case "/hls/idx.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"chave.bin\"\n#EXTINF:6,\nseg1.ts?t=1\n#EXTINF:6,\n/abs/seg2.ts\n")
		default:
			io.WriteString(w, "cdn:"+r.URL.Path)
		}
	}))
	defer cdn.Close()

	// Servidor A: painel Xtream.
	var painel *httptest.Server
	painel = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/player_api.php" && r.URL.Query().Get("action") == "":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"user_info":   map[string]any{"auth": 1},
				"server_info": map[string]any{"url": "line.exemplo", "port": "80", "server_protocol": "http"},
			})
		case r.URL.Path == "/player_api.php":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `[{"stream_id":1,"direct_source":"`+strings.ReplaceAll(painel.URL, "/", `\/`)+`\/x"}]`)
		case r.URL.Path == "/get.php":
			w.Header().Set("Content-Type", "audio/x-mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXTINF:-1,Canal\n"+painel.URL+"/live/u/p/1.ts\n")
		case r.URL.Path == "/live/u/p/1.ts":
			http.Redirect(w, r, cdn.URL+"/stream.ts", http.StatusFound)
		case r.URL.Path == "/live/u/p/2.m3u8":
			http.Redirect(w, r, cdn.URL+"/hls/idx.m3u8", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer painel.Close()

	base, _ := url.Parse(painel.URL)
	d := &net.Dialer{Timeout: 5 * time.Second}
	px := httptest.NewServer(novoProxy(base, d.DialContext))
	defer px.Close()
	pxHost := strings.TrimPrefix(px.URL, "http://")
	cdnHost := strings.TrimPrefix(cdn.URL, "http://")

	get := func(caminho string) (int, string) {
		resp, err := http.Get(px.URL + caminho)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Login: server_info aponta para o proxy.
	_, corpo := get("/player_api.php?username=u&password=p")
	var login map[string]map[string]any
	if err := json.Unmarshal([]byte(corpo), &login); err != nil {
		t.Fatal(err, corpo)
	}
	host, porta, _ := net.SplitHostPort(pxHost)
	if login["server_info"]["url"] != host || login["server_info"]["port"] != porta {
		t.Fatalf("server_info não reescrito: %v", login["server_info"])
	}

	// JSON com endereço escapado.
	_, corpo = get("/player_api.php?username=u&password=p&action=get_live_streams")
	if !strings.Contains(corpo, strings.ReplaceAll(px.URL, "/", `\/`)) || strings.Contains(corpo, base.Host) {
		t.Fatalf("JSON não reescrito: %s", corpo)
	}

	// Lista M3U.
	_, corpo = get("/get.php?username=u&password=p&type=m3u_plus")
	if !strings.Contains(corpo, px.URL+"/live/u/p/1.ts") {
		t.Fatalf("M3U não reescrita: %s", corpo)
	}

	// Canal .ts com redirecionamento para outro servidor.
	st, corpo := get("/live/u/p/1.ts")
	if st != 200 || len(corpo) != 200000 {
		t.Fatalf("stream: status %d, %d bytes", st, len(corpo))
	}

	// HLS com redirecionamento e caminhos relativos.
	_, corpo = get("/live/u/p/2.m3u8")
	esperado := []string{
		`URI="` + px.URL + "/_h/http/" + cdnHost + `/hls/chave.bin"`,
		px.URL + "/_h/http/" + cdnHost + "/hls/seg1.ts?t=1",
		px.URL + "/_h/http/" + cdnHost + "/abs/seg2.ts",
	}
	for _, e := range esperado {
		if !strings.Contains(corpo, e) {
			t.Fatalf("m3u8 sem %q:\n%s", e, corpo)
		}
	}

	// Segmento pedido pelo caminho /_h/ chega no CDN.
	_, corpo = get("/_h/http/" + cdnHost + "/hls/seg1.ts?t=1")
	if corpo != "cdn:/hls/seg1.ts" {
		t.Fatalf("segmento: %q", corpo)
	}

	// Caminho /_h/ inválido.
	if st, _ := get("/_h/ftp/x/y"); st != 400 {
		t.Fatalf("esperava 400, veio %d", st)
	}
}

func TestOcultar(t *testing.T) {
	u, _ := url.Parse("http://x/live/usuario/senha/1.ts")
	if got := ocultar(u); got != "x/live/***/***/1.ts" {
		t.Fatal(got)
	}
}

func TestLerConf(t *testing.T) {
	conf := `[Interface]
# Key for teste
PrivateKey = YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=
Address = 10.2.0.2/32
DNS = 10.2.0.1

[Peer]
# BR#1
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 192.0.2.10:51820
`
	p := filepath.Join(t.TempDir(), "proton.conf")
	os.WriteFile(p, []byte(conf), 0o600)
	cfg, err := lerConf(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.endpoint != "192.0.2.10:51820" || cfg.enderecos[0].String() != "10.2.0.2" || cfg.dns[0].String() != "10.2.0.1" {
		t.Fatalf("conf errada: %+v", cfg)
	}
	for _, e := range []string{"private_key=", "public_key=", "allowed_ip=0.0.0.0/0", "allowed_ip=::/0", "endpoint=192.0.2.10:51820", "persistent_keepalive_interval=25"} {
		if !strings.Contains(cfg.ipc, e) {
			t.Fatalf("ipc sem %q:\n%s", e, cfg.ipc)
		}
	}
}
