# TunelProxy (Tizen)

Túnel WireGuard + proxy local para assistir IPTV na TV Samsung quando a
operadora bloqueia o servidor.

- `proxy/`: programa em Go. Sobe o WireGuard dentro do próprio processo
  (sem VPN do sistema) e expõe um proxy HTTP que repassa o painel Xtream,
  as listas e os streams pelo túnel, reescrevendo os endereços.
- `App/`: app .NET para Tizen que roda o `tunelproxy` (ARMv7) como processo
  filho, mostra o status na tela e oferece uma página de configuração.
- `.github/workflows/build.yml`: compila tudo e publica o `.tpk` em Releases.

## Usar

1. Baixe o `TunelProxy.tpk` da release mais recente e instale com o
   Apps2Samsung (ele assina com o seu certificado).
2. Abra o app na TV. Ele mostra o endereço da página de configuração.
3. No celular, abra `http://IP-da-TV:8081`, cole o `.conf` do WireGuard
   (Proton) e salve.
4. Quando aparecer "Túnel ativo", aperte Voltar (o app fica rodando atrás).
5. No M3U IPTV, use uma conta Xtream Codes com servidor
   `http://127.0.0.1:8080` e o seu usuário e senha.

A configuração do WireGuard fica só na TV; nunca vai para o repositório.

O modelo de empacotar um binário Go dentro de um app Tizen .NET segue o
[tailscale/tailscale-tizen](https://github.com/tailscale/tailscale-tizen).
