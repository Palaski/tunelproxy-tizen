// tunelproxy: proxy local que manda o tráfego do IPTV por um túnel WireGuard.
//
// O app de IPTV usa este proxy como se fosse o servidor Xtream. O proxy
// repassa tudo pelo túnel e reescreve os endereços nas respostas, para que
// listas, canais e segmentos HLS também passem por ele.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func main() {
	conf := flag.String("conf", "proton.conf", "arquivo de configuração WireGuard")
	servidor := flag.String("servidor", "http://line.march93.xyz", "servidor Xtream original")
	escuta := flag.String("escuta", "0.0.0.0:8080", "endereço local do proxy")
	direto := flag.Bool("direto", false, "não usa WireGuard (só para teste)")
	ipv6 := flag.Bool("ipv6", false, "permite IPv6 até o servidor (padrão: só IPv4)")
	flag.Parse()

	base, err := url.Parse(*servidor)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		log.Fatalf("servidor inválido: %q", *servidor)
	}

	var dial dialFunc
	if *direto {
		d := &net.Dialer{Timeout: 15 * time.Second}
		dial = d.DialContext
		log.Print("modo direto: sem WireGuard")
	} else {
		dial, err = iniciarWireGuard(*conf)
		if err != nil {
			log.Fatalf("WireGuard: %v", err)
		}
		if !*ipv6 {
			dial = soIPv4(dial)
		}
		testarTunel(dial, base)
	}

	p := novoProxy(base, dial)
	log.Printf("proxy em http://%s -> %s", *escuta, base)
	for _, ip := range ipsLocais() {
		_, porta, _ := net.SplitHostPort(*escuta)
		fmt.Fprintf(os.Stderr, "  no app de IPTV use: http://%s:%s\n", ip, porta)
	}
	log.Fatal(http.ListenAndServe(*escuta, p))
}

// testarTunel abre uma conexão TCP com o servidor pelo túnel e registra o
// resultado, para facilitar o diagnóstico.
func testarTunel(dial dialFunc, base *url.URL) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	inicio := time.Now()
	c, err := dial(ctx, "tcp", hostComPorta(base))
	if err != nil {
		log.Printf("AVISO: túnel não alcançou %s: %v", base.Host, err)
		return
	}
	defer c.Close()
	log.Printf("túnel OK: alcançou %s (%s) em %s", base.Host, c.RemoteAddr(), time.Since(inicio).Round(time.Millisecond))
}

// soIPv4 força conexões IPv4: alguns painéis recusam acesso vindo de IPv6.
func soIPv4(dial dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network == "tcp" {
			network = "tcp4"
		}
		return dial(ctx, network, addr)
	}
}

func hostComPorta(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	if u.Scheme == "https" {
		return net.JoinHostPort(u.Hostname(), "443")
	}
	return net.JoinHostPort(u.Hostname(), "80")
}

func ipsLocais() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			ips = append(ips, n.IP.String())
		}
	}
	return ips
}
