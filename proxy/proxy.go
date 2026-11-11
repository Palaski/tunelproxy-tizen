package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const limiteReescrita = 64 << 20 // listas maiores que isso passam sem reescrita

// Cabeçalhos que não devem ser repassados entre conexões.
var hopByHop = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Proxy-Connection": true,
}

type proxy struct {
	base   *url.URL
	client *http.Client
}

func novoProxy(base *url.URL, dial dialFunc) *proxy {
	t := &http.Transport{
		DialContext:           dial,
		Proxy:                 nil,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		DisableCompression:    true,
	}
	// O cliente segue redirecionamentos sozinho, então o app nunca é
	// mandado direto para um servidor fora do túnel.
	return &proxy{base: base, client: &http.Client{Transport: t}}
}

// alvo converte o caminho pedido ao proxy no endereço real.
// /_h/<esquema>/<host>/resto aponta para outros servidores (CDN de vídeo);
// qualquer outro caminho vai para o servidor Xtream configurado.
func (p *proxy) alvo(r *http.Request) (*url.URL, bool) {
	if strings.HasPrefix(r.URL.Path, "/_h/") {
		partes := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/_h/"), "/", 3)
		if len(partes) < 2 || (partes[0] != "http" && partes[0] != "https") || partes[1] == "" {
			return nil, false
		}
		caminho := "/"
		if len(partes) == 3 {
			caminho += partes[2]
		}
		return &url.URL{Scheme: partes[0], Host: partes[1], Path: caminho, RawQuery: r.URL.RawQuery}, true
	}
	return &url.URL{Scheme: p.base.Scheme, Host: p.base.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}, true
}

// proxificar transforma um endereço real em um endereço do proxy.
func (p *proxy) proxificar(u *url.URL, origem string) string {
	if u.Scheme == p.base.Scheme && u.Host == p.base.Host {
		return origem + u.RequestURI()
	}
	return origem + "/_h/" + u.Scheme + "/" + u.Host + u.RequestURI()
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	inicio := time.Now()
	alvo, ok := p.alvo(r)
	if !ok {
		http.Error(w, "caminho inválido", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, alvo.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for k, vs := range r.Header {
		if !hopByHop[k] && k != "Accept-Encoding" {
			req.Header[k] = vs
		}
	}

	resp, err := p.client.Do(req)
	if err != nil {
		log.Printf("ERRO %s %s: %v", r.Method, ocultar(alvo), err)
		http.Error(w, "falha no túnel: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	origem := "http://" + r.Host
	tipo := p.tipoReescrita(r, resp)
	if tipo != "" && (resp.ContentLength < 0 || resp.ContentLength <= limiteReescrita) {
		corpo, err := io.ReadAll(io.LimitReader(resp.Body, limiteReescrita+1))
		if err == nil && len(corpo) <= limiteReescrita {
			novo := p.reescrever(tipo, corpo, resp.Request.URL, r, origem)
			copiarCabecalhos(w.Header(), resp.Header)
			w.Header().Del("Content-Length")
			w.Header().Del("Content-Encoding")
			w.WriteHeader(resp.StatusCode)
			w.Write(novo)
			log.Printf("%d %s %s (%s, %s)", resp.StatusCode, r.Method, ocultar(alvo), tipo, time.Since(inicio).Round(time.Millisecond))
			if resp.StatusCode >= 400 {
				logErro(resp, corpo)
			}
			return
		}
		// Corpo grande demais ou erro de leitura: manda o que já leu e segue.
		copiarCabecalhos(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		w.Write(corpo)
		transmitir(w, resp.Body)
		return
	}

	copiarCabecalhos(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	log.Printf("%d %s %s (stream)", resp.StatusCode, r.Method, ocultar(alvo))
	if resp.StatusCode >= 400 {
		corpo, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		logErro(resp, corpo)
		w.Write(corpo)
	}
	transmitir(w, resp.Body)
}

// logErro mostra quem respondeu e o começo da mensagem de erro, para
// diferenciar bloqueio do painel, de Cloudflare ou de outro intermediário.
func logErro(resp *http.Response, corpo []byte) {
	trecho := strings.Join(strings.Fields(string(corpo)), " ")
	if len(trecho) > 200 {
		trecho = trecho[:200]
	}
	log.Printf("   servidor=%q cf-ray=%q motivo: %s",
		resp.Header.Get("Server"), resp.Header.Get("Cf-Ray"), trecho)
}

func copiarCabecalhos(dst, src http.Header) {
	for k, vs := range src {
		if !hopByHop[k] {
			dst[k] = vs
		}
	}
}

// transmitir copia o vídeo aos poucos, empurrando cada pedaço para o app.
func transmitir(w http.ResponseWriter, corpo io.Reader) {
	rc := http.NewResponseController(w)
	buf := make([]byte, 64<<10)
	for {
		n, err := corpo.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

func (p *proxy) tipoReescrita(r *http.Request, resp *http.Response) string {
	caminho := strings.ToLower(resp.Request.URL.Path)
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	switch {
	case strings.HasSuffix(caminho, "/player_api.php"), strings.HasSuffix(caminho, "/panel_api.php"):
		return "api"
	case strings.Contains(ct, "mpegurl"), strings.HasSuffix(caminho, ".m3u8"),
		strings.HasSuffix(caminho, ".m3u"), strings.HasSuffix(caminho, "/get.php"):
		return "lista"
	case strings.Contains(ct, "json"):
		return "json"
	}
	return ""
}

func (p *proxy) reescrever(tipo string, corpo []byte, final *url.URL, r *http.Request, origem string) []byte {
	switch tipo {
	case "api":
		if r.URL.Query().Get("action") == "" {
			if novo, ok := reescreverLogin(corpo, r.Host); ok {
				return novo
			}
		}
		return p.trocarOrigem(corpo, origem)
	case "lista":
		if !bytes.HasPrefix(bytes.TrimLeft(corpo, "\xef\xbb\xbf \r\n\t"), []byte("#EXTM3U")) {
			return corpo
		}
		return p.reescreverLista(corpo, final, origem)
	case "json":
		return p.trocarOrigem(corpo, origem)
	}
	return corpo
}

// reescreverLogin aponta o server_info do Xtream para o proxy, porque alguns
// apps montam os links dos canais a partir dele.
func reescreverLogin(corpo []byte, hostProxy string) ([]byte, bool) {
	var dados map[string]any
	if json.Unmarshal(corpo, &dados) != nil {
		return nil, false
	}
	info, ok := dados["server_info"].(map[string]any)
	if !ok {
		return nil, false
	}
	host, porta, err := net.SplitHostPort(hostProxy)
	if err != nil {
		host, porta = hostProxy, "80"
	}
	info["url"] = host
	info["port"] = porta
	info["https_port"] = porta
	info["server_protocol"] = "http"
	novo, err := json.Marshal(dados)
	if err != nil {
		return nil, false
	}
	return novo, true
}

// trocarOrigem troca o endereço do servidor original pelo do proxy em JSON.
func (p *proxy) trocarOrigem(corpo []byte, origem string) []byte {
	orig := p.base.Scheme + "://" + p.base.Host
	corpo = bytes.ReplaceAll(corpo, []byte(orig), []byte(origem))
	escapado := strings.ReplaceAll(orig, "/", `\/`)
	return bytes.ReplaceAll(corpo, []byte(escapado), []byte(strings.ReplaceAll(origem, "/", `\/`)))
}

// reescreverLista trata listas M3U e HLS (m3u8): cada endereço, absoluto ou
// relativo, passa a apontar para o proxy.
func (p *proxy) reescreverLista(corpo []byte, final *url.URL, origem string) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(corpo))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		linha := sc.Text()
		limpa := strings.TrimSpace(linha)
		switch {
		case limpa == "":
			out.WriteString(linha)
		case strings.HasPrefix(limpa, "#"):
			out.WriteString(p.reescreverURIAtributo(linha, final, origem))
		default:
			if u, err := final.Parse(limpa); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
				out.WriteString(p.proxificar(u, origem))
			} else {
				out.WriteString(linha)
			}
		}
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// reescreverURIAtributo cobre tags como #EXT-X-KEY:URI="..." e #EXT-X-MAP.
func (p *proxy) reescreverURIAtributo(linha string, final *url.URL, origem string) string {
	i := strings.Index(linha, `URI="`)
	if i < 0 {
		return linha
	}
	ini := i + len(`URI="`)
	fim := strings.IndexByte(linha[ini:], '"')
	if fim < 0 {
		return linha
	}
	u, err := final.Parse(linha[ini : ini+fim])
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return linha
	}
	return linha[:ini] + p.proxificar(u, origem) + linha[ini+fim:]
}

// ocultar tira usuário e senha dos caminhos Xtream antes de ir para o log.
func ocultar(u *url.URL) string {
	partes := strings.Split(u.Path, "/")
	for i := 1; i+2 < len(partes); i++ {
		switch partes[i] {
		case "live", "movie", "series", "timeshift":
			partes[i+1], partes[i+2] = "***", "***"
		}
	}
	return u.Host + strings.Join(partes, "/")
}
