//go:build nowg

package main

import "errors"

// Versão sem WireGuard, usada só para testar a lógica do proxy.
func iniciarWireGuard(string) (dialFunc, error) {
	return nil, errors.New("compilado sem WireGuard (tag nowg)")
}
