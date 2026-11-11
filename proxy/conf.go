package main

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

type confWG struct {
	enderecos []netip.Addr
	dns       []netip.Addr
	mtu       int
	endpoint  string
	ipc       string
}

// lerConf lê o arquivo .conf no formato padrão do WireGuard (o que o Proton gera).
func lerConf(caminho string) (*confWG, error) {
	f, err := os.Open(caminho)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := &confWG{mtu: 1420}
	var ipc strings.Builder
	secao := ""
	temPeer := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		linha := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(linha, '#'); i >= 0 {
			linha = strings.TrimSpace(linha[:i])
		}
		if linha == "" {
			continue
		}
		if strings.HasPrefix(linha, "[") {
			secao = strings.ToLower(strings.Trim(linha, "[] "))
			if secao == "peer" {
				temPeer = true
			}
			continue
		}
		k, v, ok := strings.Cut(linha, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)

		switch secao + "." + k {
		case "interface.privatekey":
			h, err := chaveHex(v)
			if err != nil {
				return nil, fmt.Errorf("PrivateKey: %w", err)
			}
			ipc.WriteString("private_key=" + h + "\n")
		case "interface.address":
			for _, s := range lista(v) {
				pfx, err := netip.ParsePrefix(s)
				if err != nil {
					a, err2 := netip.ParseAddr(s)
					if err2 != nil {
						return nil, fmt.Errorf("Address %q inválido", s)
					}
					cfg.enderecos = append(cfg.enderecos, a)
					continue
				}
				cfg.enderecos = append(cfg.enderecos, pfx.Addr())
			}
		case "interface.dns":
			for _, s := range lista(v) {
				if a, err := netip.ParseAddr(s); err == nil {
					cfg.dns = append(cfg.dns, a)
				}
			}
		case "interface.mtu":
			if n, err := strconv.Atoi(v); err == nil {
				cfg.mtu = n
			}
		case "peer.publickey":
			h, err := chaveHex(v)
			if err != nil {
				return nil, fmt.Errorf("PublicKey: %w", err)
			}
			ipc.WriteString("public_key=" + h + "\n")
		case "peer.presharedkey":
			h, err := chaveHex(v)
			if err != nil {
				return nil, fmt.Errorf("PresharedKey: %w", err)
			}
			ipc.WriteString("preshared_key=" + h + "\n")
		case "peer.endpoint":
			ua, err := net.ResolveUDPAddr("udp", v)
			if err != nil {
				return nil, fmt.Errorf("Endpoint %q: %w", v, err)
			}
			cfg.endpoint = ua.String()
			ipc.WriteString("endpoint=" + cfg.endpoint + "\n")
		case "peer.allowedips":
			for _, s := range lista(v) {
				ipc.WriteString("allowed_ip=" + s + "\n")
			}
		case "peer.persistentkeepalive":
			ipc.WriteString("persistent_keepalive_interval=" + v + "\n")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !temPeer || cfg.endpoint == "" || len(cfg.enderecos) == 0 {
		return nil, fmt.Errorf("%s incompleto: precisa de Address, [Peer] e Endpoint", caminho)
	}
	if len(cfg.dns) == 0 {
		cfg.dns = []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	}
	if !strings.Contains(ipc.String(), "persistent_keepalive_interval") {
		ipc.WriteString("persistent_keepalive_interval=25\n")
	}
	cfg.ipc = ipc.String()
	return cfg, nil
}

func chaveHex(b64 string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != 32 {
		return "", fmt.Errorf("chave inválida")
	}
	return hex.EncodeToString(b), nil
}

func lista(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
