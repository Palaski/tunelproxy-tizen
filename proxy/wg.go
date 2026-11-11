//go:build !nowg

package main

import (
	"fmt"
	"log"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// iniciarWireGuard sobe o túnel inteiro dentro do programa (sem precisar de
// VPN do sistema) e devolve uma função que abre conexões por ele.
func iniciarWireGuard(caminho string) (dialFunc, error) {
	cfg, err := lerConf(caminho)
	if err != nil {
		return nil, err
	}
	tunDev, tnet, err := netstack.CreateNetTUN(cfg.enderecos, cfg.dns, cfg.mtu)
	if err != nil {
		return nil, fmt.Errorf("criando interface: %w", err)
	}
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "wg: "))
	if err := dev.IpcSet(cfg.ipc); err != nil {
		return nil, fmt.Errorf("configurando: %w", err)
	}
	if err := dev.Up(); err != nil {
		return nil, fmt.Errorf("ligando: %w", err)
	}
	log.Printf("WireGuard ligado: endpoint %s, IP %v, DNS %v", cfg.endpoint, cfg.enderecos, cfg.dns)
	return tnet.DialContext, nil
}
