"""Edita a gravação da demonstração e gera o MP4 e o GIF.

Depois do gravar.py:

    docs/demo/.venv/bin/python docs/demo/pos_producao.py

Lê os vídeos e o linha_do_tempo.json de docs/demo/execucao/bruto/, converte as
marcas para o tempo de cada vídeo (somando os saltos do relógio de parede, que
congelam o vídeo das abas abertas, e a latência medida pelo pulso magenta do
fim da aba), corta as cenas do roteiro abaixo sem os congelamentos e grava
docs/demo/ecommerce-demo.mp4 (H.264, 1280x720) e docs/demo/ecommerce-demo.gif.
Falha se falta uma marca ou o pulso, se o alinhamento não fecha, se um trecho
sai do vídeo ou se o resultado foge da duração e do tamanho combinados.
"""

import json
import subprocess
from dataclasses import dataclass, replace
from pathlib import Path

import imageio_ffmpeg

PASTA = Path(__file__).resolve().parent
BRUTO = PASTA / "execucao" / "bruto"
MP4 = PASTA / "ecommerce-demo.mp4"
GIF = PASTA / "ecommerce-demo.gif"
FFMPEG = imageio_ffmpeg.get_ffmpeg_exe()
MB = 1024 * 1024

QUADROS_POR_SEGUNDO_BRUTO = 25
LATENCIA_MAXIMA_S = 0.5
QUADROS_POR_SEGUNDO_MP4 = 25
DURACAO_MP4_S = (60, 120)
TAMANHO_MAXIMO_MP4 = 15 * MB
QUADROS_POR_SEGUNDO_GIF = 12
LARGURA_GIF = 960
DURACAO_GIF_S = (8, 12)
TAMANHO_MAXIMO_GIF = 5 * MB


@dataclass(frozen=True)
class Trecho:
    """Pedaço do vídeo de uma aba entre duas marcas, com folgas em segundos."""

    aba: str
    de: str
    ate: str
    folga_de: float = 0.0
    folga_ate: float = 0.0
    velocidade: float = 1.0


@dataclass(frozen=True)
class Salto:
    """Salto do relógio de parede: em que instante da gravação e de quantos segundos."""

    instante: float
    segundos: float


@dataclass(frozen=True)
class Alinhamento:
    """Como um instante da gravação vira um segundo do vídeo de uma aba."""

    inicio: float
    saltos: tuple[Salto, ...]
    pulso: float
    latencia: float = 0.0

    def no_video(self, instante: float) -> float:
        """Cada salto anterior ao instante congelou o vídeo e empurrou o resto para depois."""
        congelado = sum(salto.segundos for salto in self.saltos if salto.instante < instante)
        return instante - self.inicio + self.latencia + congelado


@dataclass(frozen=True)
class Corte:
    """Um pedaço contínuo de um trecho, já no tempo do vídeo da aba."""

    rotulo: str
    video: Path
    inicio: float
    fim: float
    velocidade: float

    @property
    def duracao(self) -> float:
        return (self.fim - self.inicio) / self.velocidade


ROTEIRO_MP4 = (
    Trecho("loja", "loja_abrindo", "pedido1_visto", folga_de=0.2),
    Trecho("pagamento1", "checkout1", "aprovado1_resultado", folga_ate=1.6),
    Trecho("loja", "aprovar1_clique", "pedido2_visto", folga_de=-0.3),
    Trecho("pagamento2", "checkout2", "recusado2_resultado", folga_ate=1.6),
    Trecho("loja", "recusar2_clique", "pedido2_fim", folga_de=-0.3),
    Trecho("loja", "recarregar", "promocao_fim"),
    Trecho("rabbitmq", "rabbit_exchange", "rabbit_fim"),
)

ROTEIRO_GIF = (
    Trecho("loja", "pedido1_clique", "pedido1_aguardando_pagamento", folga_de=-0.6, folga_ate=0.9),
    Trecho("pagamento1", "aprovar1_clique", "aprovar1_clique", folga_de=-0.9, folga_ate=0.7),
    Trecho("loja", "aprovar1_clique", "pedido1_enviado", folga_de=-0.2, folga_ate=2.5),
)


def carregar_linha_do_tempo() -> dict:
    caminho = BRUTO / "linha_do_tempo.json"
    if not caminho.exists():
        raise FileNotFoundError(f"{caminho} não existe: rode o gravar.py antes")
    return json.loads(caminho.read_text())


def instante(linha: dict, marca: str) -> float:
    if marca not in linha["marcas"]:
        raise KeyError(f"a marca {marca!r} não está na linha do tempo; a gravação foi interrompida?")
    return linha["marcas"][marca]


def eh_magenta(quadro: bytes) -> bool:
    return all(r > 180 and g < 90 and b > 180 for r, g, b in zip(quadro[0::3], quadro[1::3], quadro[2::3]))


def instante_do_pulso(video: Path) -> float:
    """Segundo do vídeo em que aparece o primeiro quadro todo magenta."""
    tamanho = 4 * 4 * 3
    processo = subprocess.Popen(
        [FFMPEG, "-hide_banner", "-loglevel", "error", "-i", str(video),
         "-vf", "scale=4:4", "-f", "rawvideo", "-pix_fmt", "rgb24", "-"],
        stdout=subprocess.PIPE,
    )
    try:
        indice = 0
        while len(quadro := processo.stdout.read(tamanho)) == tamanho:
            if eh_magenta(quadro):
                return indice / QUADROS_POR_SEGUNDO_BRUTO
            indice += 1
    finally:
        processo.kill()
        processo.wait()
    raise ValueError(f"{video.name} não tem o pulso magenta de sincronia; a gravação terminou antes?")


def saltos_do_relogio(linha: dict) -> tuple[Salto, ...]:
    saltos = tuple(Salto(**salto) for salto in linha["saltos_do_relogio"])
    voltas = [salto for salto in saltos if salto.segundos < 0]
    if voltas:
        raise ValueError(f"o relógio de parede voltou durante a gravação ({voltas}); o vídeo perdeu quadros, regrave")
    return saltos


def alinhar(linha: dict) -> dict[str, Alinhamento]:
    """Monta o alinhamento de cada aba e mede a latência dela pelo pulso magenta."""
    saltos = saltos_do_relogio(linha)
    print("saltos do relógio: " + (", ".join(f"{s.segundos:+.2f}s em {s.instante:.1f}s" for s in saltos) or "nenhum"))
    print("alinhamento dos vídeos:")
    alinhamentos = {}
    for aba, info in linha["abas"].items():
        sincronia = instante(linha, f"{aba}_sincronia")
        sem_latencia = Alinhamento(
            inicio=info["inicio"],
            saltos=tuple(salto for salto in saltos if info["inicio"] < salto.instante < sincronia),
            pulso=instante_do_pulso(BRUTO / info["video"]),
        )
        latencia = sem_latencia.pulso - sem_latencia.no_video(sincronia)
        print(f"  {aba:<10} {len(sem_latencia.saltos)} salto(s) durante a aba, latência {latencia:+.2f}s")
        if abs(latencia) > LATENCIA_MAXIMA_S:
            raise ValueError(
                f"o pulso de {aba} está {latencia:+.2f}s fora do previsto pelos saltos do relógio; "
                "o vídeo não segue o modelo de alinhamento, regrave"
            )
        alinhamentos[aba] = replace(sem_latencia, latencia=latencia)
    return alinhamentos


def cortar(roteiro: tuple[Trecho, ...], linha: dict, alinhamentos: dict[str, Alinhamento]) -> list[Corte]:
    """Converte cada trecho para o tempo do vídeo da aba, em pedaços que pulam os
    congelamentos, e confere se tudo cabe antes do pulso."""
    cortes = []
    for trecho in roteiro:
        alinhamento = alinhamentos[trecho.aba]
        video = BRUTO / linha["abas"][trecho.aba]["video"]
        de = instante(linha, trecho.de) + trecho.folga_de
        ate = instante(linha, trecho.ate) + trecho.folga_ate
        if ate <= de:
            raise ValueError(f"{trecho} termina antes de começar ({de:.2f}s a {ate:.2f}s)")
        limites = [de, *(salto.instante for salto in alinhamento.saltos if de < salto.instante < ate), ate]
        for comeco, fim in zip(limites, limites[1:]):
            inicio_no_video = alinhamento.no_video(comeco + 1e-6)
            fim_no_video = alinhamento.no_video(fim - 1e-6)
            if not 0 <= inicio_no_video < fim_no_video <= alinhamento.pulso:
                raise ValueError(
                    f"{trecho} vira {inicio_no_video:.2f}s a {fim_no_video:.2f}s, mas o vídeo de "
                    f"{trecho.aba} só tem cena de 0 a {alinhamento.pulso:.2f}s"
                )
            if fim_no_video - inicio_no_video >= 1 / QUADROS_POR_SEGUNDO_BRUTO:
                rotulo = f"{trecho.aba} {trecho.de} → {trecho.ate}"
                cortes.append(Corte(rotulo, video, inicio_no_video, fim_no_video, trecho.velocidade))
    return cortes


def entradas(cortes: list[Corte]) -> list[str]:
    argumentos = []
    for corte in cortes:
        argumentos += ["-ss", f"{corte.inicio:.3f}", "-t", f"{corte.fim - corte.inicio:.3f}", "-i", str(corte.video)]
    return argumentos


def concatenar(cortes: list[Corte], quadros_por_segundo: int) -> str:
    """Filtro que ajusta a velocidade de cada corte e junta todos em [junto]."""
    filtros = [
        f"[{i}:v]setpts=(PTS-STARTPTS)/{corte.velocidade},fps={quadros_por_segundo},setsar=1[c{i}]"
        for i, corte in enumerate(cortes)
    ]
    rotulos = "".join(f"[c{i}]" for i in range(len(cortes)))
    return ";".join([*filtros, f"{rotulos}concat=n={len(cortes)}:v=1:a=0[junto]"])


def rodar_ffmpeg(argumentos: list[str]) -> None:
    resultado = subprocess.run([FFMPEG, "-y", "-hide_banner", "-loglevel", "error", *argumentos], capture_output=True, text=True)
    if resultado.returncode != 0:
        raise RuntimeError(f"ffmpeg falhou ({resultado.returncode}):\n{resultado.stderr}")


def conferir(arquivo: Path, duracao: float, faixa: tuple[float, float], tamanho_maximo: int) -> None:
    tamanho = arquivo.stat().st_size
    print(f"{arquivo.name}: {duracao:.1f}s, {tamanho / MB:.2f} MB")
    if not faixa[0] <= duracao <= faixa[1]:
        raise ValueError(f"{arquivo.name} tem {duracao:.1f}s; o combinado é de {faixa[0]} a {faixa[1]}s")
    if tamanho > tamanho_maximo:
        raise ValueError(f"{arquivo.name} tem {tamanho / MB:.2f} MB; o máximo é {tamanho_maximo / MB:.0f} MB")


def descrever(nome: str, cortes: list[Corte]) -> None:
    print(f"{nome}:")
    for corte in cortes:
        print(f"  {corte.rotulo:<52} {corte.inicio:6.2f}s a {corte.fim:6.2f}s ({corte.duracao:.2f}s)")


def gerar_mp4(linha: dict, alinhamentos: dict[str, Alinhamento]) -> None:
    cortes = cortar(ROTEIRO_MP4, linha, alinhamentos)
    descrever("MP4", cortes)
    total = sum(corte.duracao for corte in cortes)
    grafo = (
        concatenar(cortes, QUADROS_POR_SEGUNDO_MP4)
        + f";[junto]fade=t=in:st=0:d=0.5,fade=t=out:st={total - 0.8:.3f}:d=0.8,format=yuv420p[saida]"
    )
    rodar_ffmpeg([
        *entradas(cortes),
        "-filter_complex", grafo,
        "-map", "[saida]",
        "-c:v", "libx264", "-preset", "slow", "-crf", "20", "-pix_fmt", "yuv420p",
        "-movflags", "+faststart", "-an",
        str(MP4),
    ])
    conferir(MP4, total, DURACAO_MP4_S, TAMANHO_MAXIMO_MP4)


def gerar_gif(linha: dict, alinhamentos: dict[str, Alinhamento]) -> None:
    cortes = cortar(ROTEIRO_GIF, linha, alinhamentos)
    descrever("GIF", cortes)
    total = sum(corte.duracao for corte in cortes)
    grafo = (
        concatenar(cortes, QUADROS_POR_SEGUNDO_GIF)
        + f";[junto]scale={LARGURA_GIF}:-1:flags=lanczos,split[a][b]"
        + ";[a]palettegen=max_colors=128:stats_mode=diff[paleta]"
        + ";[b][paleta]paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle[saida]"
    )
    rodar_ffmpeg([*entradas(cortes), "-filter_complex", grafo, "-map", "[saida]", "-loop", "0", str(GIF)])
    conferir(GIF, total, DURACAO_GIF_S, TAMANHO_MAXIMO_GIF)


if __name__ == "__main__":
    linha_do_tempo = carregar_linha_do_tempo()
    alinhamentos_dos_videos = alinhar(linha_do_tempo)
    gerar_mp4(linha_do_tempo, alinhamentos_dos_videos)
    gerar_gif(linha_do_tempo, alinhamentos_dos_videos)
