// TunelProxy para TV Samsung (Tizen).
//
// Sobe o binário Go "tunelproxy" (WireGuard + proxy local) como processo
// filho e mostra o status na tela. A configuração do WireGuard é enviada pelo
// celular em http://IP-da-TV:8081, para a chave privada nunca ficar no pacote.
//
// O modelo de empacotar um binário Go dentro de um app .NET segue o
// tailscale/tailscale-tizen.
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Tizen.NUI;
using Tizen.NUI.BaseComponents;
using Path = System.IO.Path;
using File = System.IO.File;

namespace TunelProxy
{
    class Program : NUIApplication
    {
        const int PortaProxy = 8080;
        const int PortaConfig = 8081;
        const string ServidorPadrao = "http://line.march93.xyz";
        const int MaxLinhas = 300;
        const int LinhasNaTela = 14;

        static readonly Color Fundo = new Color(0.09f, 0.15f, 0.23f, 1f);
        static readonly Color Cartao = new Color(0.14f, 0.21f, 0.30f, 1f);
        static readonly Color Texto = new Color(0.95f, 0.96f, 0.97f, 1f);
        static readonly Color TextoFraco = new Color(0.65f, 0.70f, 0.76f, 1f);
        static readonly Color Verde = new Color(0.25f, 0.77f, 0.55f, 1f);
        static readonly Color Amarelo = new Color(0.98f, 0.78f, 0.30f, 1f);
        static readonly Color Vermelho = new Color(0.95f, 0.40f, 0.40f, 1f);

        SynchronizationContext _ui;
        TextLabel _status, _enderecos, _log;

        string _exe, _confPath, _servidorPath;
        readonly object _lockProc = new object();
        Process _proc;
        int _geracao;
        volatile bool _encerrando;
        string _statusAtual = "Iniciando…";

        readonly LinkedList<string> _linhas = new LinkedList<string>();

        protected override void OnCreate()
        {
            base.OnCreate();
            _ui = SynchronizationContext.Current;
            string dados = Tizen.Applications.Application.Current.DirectoryInfo.Data;
            _exe = Path.Combine(dados, "tunelproxy");
            _confPath = Path.Combine(dados, "wireguard.conf");
            _servidorPath = Path.Combine(dados, "servidor.txt");

            try { MontarTela(); } catch (Exception ex) { Log("erro na tela: " + ex.Message); }
            Task.Run(Iniciar);
            Task.Run(ServidorConfig);
        }

        protected override void OnTerminate()
        {
            _encerrando = true;
            Parar();
            base.OnTerminate();
        }

        // ---- Tela -------------------------------------------------------------

        void MontarTela()
        {
            Window.Instance.BackgroundColor = Fundo;
            Window.Instance.KeyEvent += AoApertarTecla;

            var pilha = new View
            {
                WidthResizePolicy = ResizePolicyType.FillToParent,
                HeightResizePolicy = ResizePolicyType.FillToParent,
                Padding = new Extents(120, 120, 80, 60),
                Layout = new LinearLayout
                {
                    LinearOrientation = LinearLayout.Orientation.Vertical,
                    HorizontalAlignment = HorizontalAlignment.Begin,
                    VerticalAlignment = VerticalAlignment.Top,
                    CellPadding = new Size2D(0, 28),
                },
            };

            pilha.Add(new TextLabel("TunelProxy") { TextColor = Texto, PointSize = 40f });

            _status = new TextLabel(_statusAtual) { TextColor = Amarelo, PointSize = 30f };
            pilha.Add(_status);

            var cartao = new View
            {
                BackgroundColor = Cartao,
                CornerRadius = new Vector4(24, 24, 24, 24),
                Padding = new Extents(48, 48, 32, 32),
                WidthSpecification = 1680,
                Layout = new LinearLayout
                {
                    LinearOrientation = LinearLayout.Orientation.Vertical,
                    CellPadding = new Size2D(0, 12),
                },
            };
            _enderecos = new TextLabel(TextoEnderecos())
            {
                TextColor = Texto,
                PointSize = 22f,
                MultiLine = true,
                WidthSpecification = 1580,
            };
            cartao.Add(_enderecos);
            pilha.Add(cartao);

            _log = new TextLabel("")
            {
                TextColor = TextoFraco,
                PointSize = 15f,
                FontFamily = "monospace",
                MultiLine = true,
                WidthSpecification = 1680,
            };
            pilha.Add(_log);

            pilha.Add(new TextLabel("Voltar: deixa o túnel rodando em segundo plano  ·  Botão vermelho: fecha o app")
            {
                TextColor = TextoFraco,
                PointSize = 17f,
            });

            Window.Instance.GetDefaultLayer().Add(pilha);
        }

        string TextoEnderecos()
        {
            var ips = IpsLocais();
            string ip = ips.FirstOrDefault() ?? "IP-da-TV";
            var sb = new StringBuilder();
            sb.AppendLine($"No M3U IPTV (Xtream Codes), servidor:  http://127.0.0.1:{PortaProxy}");
            sb.AppendLine($"Configurar pelo celular:  http://{ip}:{PortaConfig}");
            sb.Append($"Servidor IPTV atual:  {LerServidor()}");
            return sb.ToString();
        }

        void AoApertarTecla(object sender, Window.KeyEventArgs e)
        {
            if (e.Key.State != Key.StateType.Down) return;
            string tecla = e.Key.KeyPressedName;
            if (tecla == "XF86Red")
            {
                Exit();
            }
            else if (tecla == "XF86Back" || tecla == "Escape")
            {
                // Não fecha: só manda o app para trás, para o túnel continuar.
                try { Window.Instance.Lower(); } catch (Exception ex) { Log("Lower: " + ex.Message); }
            }
        }

        void NaTela(Action a)
        {
            if (_ui == null) return;
            _ui.Post(_ => { try { a(); } catch { } }, null);
        }

        void DefinirStatus(string texto, Color cor)
        {
            _statusAtual = texto;
            NaTela(() => { _status.Text = texto; _status.TextColor = cor; });
        }

        void Log(string linha)
        {
            string l = DateTime.Now.ToString("HH:mm:ss") + " " + linha;
            string tela;
            lock (_linhas)
            {
                _linhas.AddLast(l);
                while (_linhas.Count > MaxLinhas) _linhas.RemoveFirst();
                tela = string.Join("\n", _linhas.Skip(Math.Max(0, _linhas.Count - LinhasNaTela)));
            }
            try { Tizen.Log.Info("TunelProxy", linha); } catch { }
            NaTela(() => _log.Text = tela);

            if (linha.Contains("túnel OK")) DefinirStatus("Túnel ativo", Verde);
            else if (linha.Contains("AVISO: túnel")) DefinirStatus("Túnel sem conexão com o servidor", Amarelo);
        }

        // ---- Processo do túnel -----------------------------------------------

        [DllImport("libc", SetLastError = true)]
        static extern int chmod(string path, uint mode);

        void Iniciar()
        {
            try
            {
                string instalado = Path.GetFullPath(Path.Combine(
                    Tizen.Applications.Application.Current.DirectoryInfo.Resource, "..", "lib", "tunelproxy"));
                File.Copy(instalado, _exe, overwrite: true);
                chmod(_exe, 0x1ED); // 0755
                Log("binário pronto");

                if (File.Exists(_confPath))
                    IniciarProxy();
                else
                    DefinirStatus("Sem configuração: abra o endereço abaixo no celular", Amarelo);
            }
            catch (Exception ex)
            {
                Log("erro ao iniciar: " + ex.Message);
                DefinirStatus("Erro: " + ex.Message, Vermelho);
            }
        }

        void IniciarProxy()
        {
            lock (_lockProc)
            {
                if (_encerrando || (_proc != null && !_proc.HasExited)) return;
                int geracao = ++_geracao;

                var psi = new ProcessStartInfo
                {
                    FileName = _exe,
                    UseShellExecute = false,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                };
                psi.ArgumentList.Add("-conf");
                psi.ArgumentList.Add(_confPath);
                psi.ArgumentList.Add("-servidor");
                psi.ArgumentList.Add(LerServidor());
                psi.ArgumentList.Add("-escuta");
                psi.ArgumentList.Add("0.0.0.0:" + PortaProxy);

                var p = new Process { StartInfo = psi, EnableRaisingEvents = true };
                p.OutputDataReceived += (s, e) => { if (e.Data != null) Log(SemData(e.Data)); };
                p.ErrorDataReceived += (s, e) => { if (e.Data != null) Log(SemData(e.Data)); };
                p.Exited += (s, e) => AoSair(geracao, p);
                p.Start();
                p.BeginOutputReadLine();
                p.BeginErrorReadLine();
                _proc = p;
                DefinirStatus("Conectando o túnel…", Amarelo);
                Log("túnel iniciado (pid " + p.Id + ")");
            }
        }

        void AoSair(int geracao, Process p)
        {
            int codigo = -1;
            try { codigo = p.ExitCode; } catch { }
            lock (_lockProc)
            {
                if (_encerrando || geracao != _geracao) return;
            }
            Log("túnel parou (código " + codigo + "), tentando de novo em 5s");
            DefinirStatus("Túnel parou, reiniciando…", Vermelho);
            Task.Delay(5000).ContinueWith(_ => IniciarProxy());
        }

        void Parar()
        {
            lock (_lockProc)
            {
                _geracao++;
                if (_proc == null) return;
                try
                {
                    if (!_proc.HasExited)
                    {
                        _proc.Kill();
                        _proc.WaitForExit(3000);
                    }
                }
                catch { }
                _proc = null;
            }
        }

        // O log do Go já vem com data e hora; a tela mostra a nossa.
        static string SemData(string linha)
        {
            if (linha.Length > 20 && linha[4] == '/' && linha[7] == '/' && linha[10] == ' ')
                return linha.Substring(20);
            return linha;
        }

        string LerServidor()
        {
            try
            {
                if (File.Exists(_servidorPath))
                {
                    string s = File.ReadAllText(_servidorPath).Trim();
                    if (s.Length > 0) return s;
                }
            }
            catch { }
            return ServidorPadrao;
        }

        static List<string> IpsLocais()
        {
            var ips = new List<string>();
            try
            {
                foreach (var nic in NetworkInterface.GetAllNetworkInterfaces())
                {
                    if (nic.OperationalStatus != OperationalStatus.Up) continue;
                    foreach (var a in nic.GetIPProperties().UnicastAddresses)
                    {
                        if (a.Address.AddressFamily == AddressFamily.InterNetwork && !IPAddress.IsLoopback(a.Address))
                            ips.Add(a.Address.ToString());
                    }
                }
            }
            catch { }
            return ips;
        }

        // ---- Página de configuração (porta 8081) ------------------------------

        void ServidorConfig()
        {
            try
            {
                var l = new HttpListener();
                l.Prefixes.Add($"http://+:{PortaConfig}/");
                l.Start();
                Log($"página de configuração na porta {PortaConfig}");
                while (true)
                {
                    var ctx = l.GetContext();
                    Task.Run(() => Atender(ctx));
                }
            }
            catch (Exception ex)
            {
                Log("página de configuração falhou: " + ex.Message);
            }
        }

        void Atender(HttpListenerContext ctx)
        {
            try
            {
                string caminho = ctx.Request.Url.AbsolutePath;
                if (caminho == "/salvar" && ctx.Request.HttpMethod == "POST")
                {
                    string corpo;
                    using (var r = new StreamReader(ctx.Request.InputStream, Encoding.UTF8))
                        corpo = r.ReadToEnd();
                    var form = LerFormulario(corpo);
                    string msg = Salvar(form);
                    Responder(ctx, 200, "text/html; charset=utf-8", Pagina(msg));
                }
                else if (caminho == "/log")
                {
                    string texto;
                    lock (_linhas) texto = string.Join("\n", _linhas);
                    Responder(ctx, 200, "text/plain; charset=utf-8", texto);
                }
                else
                {
                    Responder(ctx, 200, "text/html; charset=utf-8", Pagina(null));
                }
            }
            catch (Exception ex)
            {
                Log("erro na página: " + ex.Message);
                try { ctx.Response.Abort(); } catch { }
            }
        }

        string Salvar(Dictionary<string, string> form)
        {
            form.TryGetValue("conf", out string conf);
            form.TryGetValue("servidor", out string servidor);
            conf = (conf ?? "").Replace("\r\n", "\n").Trim();
            servidor = (servidor ?? "").Trim().TrimEnd('/');

            if (conf.Length > 0)
            {
                if (!conf.Contains("[Interface]") || !conf.Contains("[Peer]"))
                    return "Configuração inválida: precisa ter [Interface] e [Peer].";
                File.WriteAllText(_confPath, conf + "\n");
                chmod(_confPath, 0x180); // 0600
            }
            if (servidor.Length > 0)
            {
                if (!Uri.TryCreate(servidor, UriKind.Absolute, out var u) || (u.Scheme != "http" && u.Scheme != "https"))
                    return "Servidor inválido: use algo como http://line.march93.xyz";
                File.WriteAllText(_servidorPath, servidor);
            }
            if (!File.Exists(_confPath))
                return "Cole a configuração do WireGuard antes de salvar.";

            Log("configuração salva, reiniciando o túnel");
            NaTela(() => _enderecos.Text = TextoEnderecos());
            Parar();
            IniciarProxy();
            return "Salvo. O túnel está reiniciando; veja o status abaixo em alguns segundos.";
        }

        static Dictionary<string, string> LerFormulario(string corpo)
        {
            var d = new Dictionary<string, string>();
            foreach (var par in corpo.Split('&'))
            {
                int i = par.IndexOf('=');
                if (i <= 0) continue;
                string k = WebUtility.UrlDecode(par.Substring(0, i));
                string v = WebUtility.UrlDecode(par.Substring(i + 1));
                d[k] = v;
            }
            return d;
        }

        string Pagina(string mensagem)
        {
            string ultimas;
            lock (_linhas) ultimas = string.Join("\n", _linhas.Skip(Math.Max(0, _linhas.Count - 20)));
            bool temConf = File.Exists(_confPath);
            string H(string s) => WebUtility.HtmlEncode(s ?? "");
            string blocoMsg = mensagem != null ? "<div class=\"msg\">" + H(mensagem) + "</div>" : "";
            string dica = temConf
                ? "Já existe uma configuração salva. Cole outra só se quiser trocar."
                : "Cole aqui o conteúdo do arquivo .conf do Proton";

            return $@"<!doctype html>
<html lang=""pt-BR""><head><meta charset=""utf-8"">
<meta name=""viewport"" content=""width=device-width, initial-scale=1"">
<title>TunelProxy</title>
<style>
body{{font-family:system-ui,sans-serif;margin:0;padding:16px;background:#17263a;color:#eef2f5}}
main{{max-width:640px;margin:auto}}
h1{{font-size:22px;margin:4px 0 12px}}
label{{display:block;margin:16px 0 6px;font-weight:600}}
textarea,input{{width:100%;box-sizing:border-box;padding:10px;border-radius:8px;border:1px solid #3a4f68;background:#0f1b2a;color:#eef2f5;font:14px monospace}}
textarea{{height:220px}}
button{{margin-top:16px;width:100%;padding:14px;border:0;border-radius:8px;background:#40c48c;color:#0b1520;font-size:16px;font-weight:700}}
.msg{{background:#24364d;padding:12px;border-radius:8px;margin:12px 0}}
.st{{color:#a6b3c2}}
pre{{background:#0f1b2a;padding:10px;border-radius:8px;white-space:pre-wrap;font-size:12px;color:#a6b3c2}}
a{{color:#7fd8b0}}
</style></head><body><main>
<h1>TunelProxy</h1>
<div class=""st"">Status: {H(_statusAtual)}</div>
{blocoMsg}
<form method=""post"" action=""/salvar"">
<label for=""conf"">Configuração WireGuard (.conf)</label>
<textarea id=""conf"" name=""conf"" placeholder=""{H(dica)}""></textarea>
<label for=""servidor"">Servidor IPTV</label>
<input id=""servidor"" name=""servidor"" value=""{H(LerServidor())}"">
<button type=""submit"">Salvar e reiniciar o túnel</button>
</form>
<label>Últimas linhas do log (<a href=""/log"">ver tudo</a>)</label>
<pre>{H(ultimas)}</pre>
</main></body></html>";
        }

        static void Responder(HttpListenerContext ctx, int status, string tipo, string corpo)
        {
            var bytes = Encoding.UTF8.GetBytes(corpo);
            ctx.Response.StatusCode = status;
            ctx.Response.ContentType = tipo;
            ctx.Response.ContentLength64 = bytes.Length;
            ctx.Response.OutputStream.Write(bytes, 0, bytes.Length);
            ctx.Response.Close();
        }

        static void Main(string[] args)
        {
            var app = new Program();
            app.Run(args);
        }
    }
}
