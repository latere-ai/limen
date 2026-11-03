# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

# /// script
# requires-python = ">=3.12"
# dependencies = [
#   "numpy==2.5.4",
#   "pandas==3.1.0",
#   "seaborn==0.13.2",
#   "matplotlib==3.11.2",
# ]
# ///
"""Draw Limen's figures from the simulation in data/.

    uv run render.py                          # every figure
    uv run render.py teaser                   # one figure
    uv run render.py teaser --preview DIR     # also a PNG of each variant

`go run ./cmd/simulate` writes the files of data/ (see its doc comment):
the decisions of each simulated person, the false-allow estimates of the
audit runs, the calibration reports, Limen's thresholds over a grid of
costs, and the settings of the run in meta.json. Every mark is drawn from
those files and every number in a label is computed from them. Each
figure is drawn for a light and a dark page, as <name>.svg and
<name>-dark.svg next to this file.

A figure is a function (data, theme) -> Figure, listed in FIGURES.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from functools import cached_property
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import numpy as np  # noqa: E402
import pandas as pd  # noqa: E402
import seaborn as sns  # noqa: E402
from matplotlib import patheffects  # noqa: E402
from matplotlib.colors import BoundaryNorm, Colormap, to_hex  # noqa: E402
from matplotlib.patches import Rectangle  # noqa: E402

HERE = Path(__file__).resolve().parent
DATA = HERE / "data"
SEED = 7  # the subsample of dots, fixed so that a render is reproducible
# Layers below this z-order are rasterized in the SVG (densities, dots);
# lines and text above it stay vector.
RASTER_Z = 1


@dataclass(frozen=True)
class Theme:
    """The colors of one page background."""

    name: str
    suffix: str
    style: str
    bg: str
    fg: str
    muted: str
    dot: str
    fill: Colormap  # density, sequential: seaborn "Blues"
    line: str  # density contours
    signed: Colormap  # signed quantities: seaborn "vlag"
    red_at: float  # where on vlag the over-confident red is read
    edge: str


VLAG = sns.color_palette("vlag", as_cmap=True)
LIGHT = Theme(
    name="light", suffix="", style="white", bg="#ffffff", fg="#1f2328", muted="#59636e",
    dot="#1f2328", fill=sns.color_palette("Blues", as_cmap=True), line="#08306b",
    signed=VLAG, red_at=1.0, edge="#ffffff",
)
# On a dark page the lowest density must sink into the background, so the
# Blues run the other way, from a blue near the page up to Blues' palest.
BLUES = sns.color_palette("Blues", 9).as_hex()
DARK = Theme(
    name="dark", suffix="-dark", style="dark", bg="#0d1117", fg="#e6edf3", muted="#9198a1",
    dot="#e6edf3", fill=sns.blend_palette(["#0e2244", *BLUES[::-1][1:]], as_cmap=True), line="#c6dbef",
    signed=VLAG, red_at=0.82, edge="#0d1117",
)
THEMES = (LIGHT, DARK)


class Data:
    """The files of data/, each read once, when a figure first asks."""

    def __init__(self, root: Path = DATA) -> None:
        self.root = root

    @cached_property
    def meta(self) -> dict:
        return json.loads((self.root / "meta.json").read_text())

    def _csv(self, name: str) -> pd.DataFrame:
        return pd.read_csv(self.root / name, dtype={"verdict": str, "applied": str, "state": str})

    @cached_property
    def decisions(self) -> pd.DataFrame:
        return self._csv("decisions.csv.gz")

    @cached_property
    def product(self) -> pd.DataFrame:
        return self._csv("decisions-product.csv.gz")

    @cached_property
    def audits(self) -> pd.DataFrame:
        return self._csv("audits.csv")

    @cached_property
    def calibration(self) -> pd.DataFrame:
        return self._csv("calibration.csv")

    @cached_property
    def thresholds(self) -> pd.DataFrame:
        return self._csv("thresholds.csv.gz")

    @cached_property
    def profiles(self) -> pd.DataFrame:
        return self._csv("profiles.csv")

    @cached_property
    def cover(self) -> pd.DataFrame:
        return pd.read_csv(self.root / "cover.csv.gz", dtype={"verdict": str, "ask_kind": str},
                           keep_default_na=False)


def apply_theme(th: Theme) -> None:
    sns.set_theme(style=th.style, rc={
        "figure.facecolor": th.bg, "axes.facecolor": th.bg, "savefig.facecolor": th.bg,
        "text.color": th.fg, "axes.labelcolor": th.fg, "axes.edgecolor": th.muted,
        "xtick.color": th.muted, "ytick.color": th.muted,
        "font.family": "DejaVu Sans", "font.size": 10,
        "axes.titlesize": 13, "axes.labelsize": 11,
        "xtick.labelsize": 9.5, "ytick.labelsize": 9.5,
        "xtick.bottom": True, "ytick.left": True,
        "xtick.major.size": 3, "ytick.major.size": 3,
        "xtick.major.width": 0.8, "ytick.major.width": 0.8,
        "svg.fonttype": "path", "svg.hashsalt": "limen-figures",
    })


def pct(x: float) -> str:
    """A share as a reader reads it: 88%, 9.1%, 0.7%, <0.1%, 0%."""
    if x == 0:
        return "0%"
    if x < 0.001:
        return "<0.1%"
    s = f"{100 * x:.1f}" if x < 0.1 else f"{100 * x:.0f}"
    return s.removesuffix(".0") + "%"


def density(ax, x, y, th: Theme, *, levels: int = 10, thresh: float = 0.03, z: float = 0.1,
            clip=((0, 1), (0, 1)), bw_adjust: float = 1.1, alpha: float = 1.0) -> None:
    """The joint density of (x, y) inside clip, filled and outlined.

    seaborn draws the fill at iso-proportion levels, so each band holds the
    same share of the points; the colors step evenly by band, not by the
    density's height, or the peak would wash the rest out.
    """
    kw = dict(clip=clip, gridsize=200, bw_adjust=bw_adjust, thresh=thresh)
    sns.kdeplot(x=x, y=y, ax=ax, fill=True, levels=levels, cmap=th.fill, zorder=z, alpha=alpha, **kw)
    cs = ax.collections[-1]
    cs.set_norm(BoundaryNorm(cs.levels, th.fill.N))
    cs.changed()
    sns.kdeplot(x=x, y=y, ax=ax, levels=5, color=th.line, linewidths=0.6, alpha=0.75, zorder=2.5, **kw)


def halo(th: Theme) -> list:
    """A stroke of the page color under a line drawn over data."""
    return [patheffects.withStroke(linewidth=2.6, foreground=th.bg, alpha=0.8)]


def backing(th: Theme) -> dict:
    """A rounded patch of the page color behind text set over data, so
    dots and contours do not run through the numbers."""
    return dict(boxstyle="round,pad=0.3,rounding_size=0.35", facecolor=th.bg, edgecolor="none", alpha=0.75)


def unit_axes(ax, th: Theme) -> None:
    ticks = [0, 0.25, 0.5, 0.75, 1]
    ax.set(xlim=(0, 1), ylim=(0, 1), aspect="equal", xticks=ticks, yticks=ticks)
    names = ["0", ".25", ".5", ".75", "1"]
    ax.set_xticklabels(names)
    ax.set_yticklabels(names)
    for s in ax.spines.values():
        s.set_color(th.muted)
        s.set_linewidth(0.8)


# --------------------------------------------------------------------------
# teaser
# --------------------------------------------------------------------------

def teaser(data: Data, th: Theme) -> plt.Figure:
    """Limen's predicted approval against the person's true approval, in
    three phases of the history, pooled across persons."""
    d, meta = data.decisions, data.meta
    lo, hi, cp = meta["block_below"], meta["allow_above"], meta["changepoint"]
    n, persons = meta["decisions"], meta["persons"]
    # t counts the decisions before this one, from 0, so decision k
    # (counted from 1) is t = k - 1, and the trust reset at t = cp falls on
    # decision cp + 1: the third phase starts there.
    phases = [
        (0, 150, "Limen knows little"),
        (150, cp, "Trust built up"),
        (cp, n, "After the trust reset"),
    ]
    n_dots = min(int(((d.t >= a) & (d.t < b)).sum()) for a, b, _ in phases)
    rng = np.random.default_rng(SEED)
    red = to_hex(th.signed(th.red_at))

    fig, axes = plt.subplots(1, 3, figsize=(10, 5.15), sharey=True)
    fig.subplots_adjust(left=0.065, right=0.99, bottom=0.25, top=0.8, wspace=0.08)
    total_fa = 0
    for ax, (a, b, title) in zip(axes, phases):
        p = d[(d.t >= a) & (d.t < b)]
        x, y = p.p_hat.to_numpy(), p.p_star.to_numpy()

        # Where a false allow falls: an allow the person would more likely
        # deny. Drawn first, so the density shows through it.
        box = dict(xy=(hi, 0), width=1 - hi, height=0.5)
        ax.add_patch(Rectangle(**box, facecolor=red, alpha=0.12, edgecolor="none", zorder=0.05))
        ax.add_patch(Rectangle(**box, facecolor="none", edgecolor=red, lw=0.7, ls=(0, (2, 1.5)), zorder=2.6))
        ax.text(hi + (1 - hi) / 2, 0.25, "false allow", rotation=90, ha="center", va="center",
                fontsize=8, color=red, zorder=5)

        density(ax, x, y, th)
        pick = rng.choice(len(p), size=n_dots, replace=False) if len(p) > n_dots else slice(None)
        ax.scatter(x[pick], y[pick], s=1.5, color=th.dot, alpha=0.3, linewidths=0, zorder=0.5)
        ax.set_rasterization_zorder(RASTER_Z)

        ax.plot([0, 1], [0, 1], ls=(0, (4, 3)), lw=0.8, color=th.muted, zorder=3)
        for v in (lo, hi):
            ax.axvline(v, lw=0.8, color=th.fg, alpha=0.6, zorder=3)

        # The verdict Limen's thresholds gave, as a share of the phase.
        share = p.verdict.value_counts(normalize=True)
        for x0, x1, v in ((0, lo, "block"), (lo, hi, "ask"), (hi, 1, "allow")):
            ax.text((x0 + x1) / 2, 1.02, f"{v}\n{pct(share.get(v, 0.0))}", ha="center", va="bottom",
                    fontsize=9.5, color=th.fg, linespacing=1.2, clip_on=False)

        # The person's veto: an irreversible action on a sensitive target
        # while trust is low (experiment/oracle.py _veto_active), the blob
        # at the bottom. The note sits just above it, the arrow at its
        # middle.
        veto = p[p.vetoed == 1]
        fa_y = 0.09
        if len(veto):
            vc = veto.verdict.value_counts()
            allowed = int(vc.get("allow", 0))
            parts = [f"{int(vc[v]):,} {w}" for v, w in (("ask", "asked"), ("block", "blocked")) if vc.get(v, 0)]
            ax.annotate(
                f"the person's veto: {len(veto):,} of {len(p):,}\n"
                f"{f'{allowed:,}' if allowed else 'none'} allowed ({', '.join(parts)})",
                xy=(veto.p_hat.median(), 0.015), xytext=(0.035, 0.1), ha="left", va="bottom",
                fontsize=8.3, color=th.fg, linespacing=1.35, zorder=5, bbox=backing(th),
                arrowprops=dict(arrowstyle="-|>", path_effects=halo(th), color=th.fg, lw=0.8, shrinkA=0, shrinkB=1, mutation_scale=8),
            )
            fa_y = 0.245

        # False allows: an allow the person would more likely deny.
        allows = p[p.verdict == "allow"]
        fa = allows[allows.p_star < 0.5]
        total_fa += len(fa)
        ax.scatter(fa.p_hat, fa.p_star, s=36, marker="X", color=red, edgecolors=th.edge, linewidths=0.6, zorder=6)
        lines = [f"false allows: {len(fa):,} of {len(allows):,}"]
        if len(allows):
            lines[0] += f" ({pct(len(fa) / len(allows))})"
            lines.append(f"lowest true approval allowed: {allows.p_star.min():.2f}")
        ax.text(hi - 0.03, fa_y, "\n".join(lines), ha="right", va="bottom", fontsize=8.3,
                color=th.fg, linespacing=1.35, zorder=5, bbox=backing(th))

        # Above the diagonal, Limen's probability is below the person's:
        # where it errs, it errs toward asking.
        above = float((p.p_star > p.p_hat).mean())
        note = f"above the diagonal: {pct(above)}"
        if b == n:
            note += "\nLimen's probability is below the\nperson's: it errs toward asking"
        ax.text(0.035, 0.965, note, ha="left", va="top", fontsize=8.3, color=th.fg, linespacing=1.35,
                zorder=5, bbox=backing(th))

        unit_axes(ax, th)
        ax.set_xlabel("Limen's approval probability")
        ax.text(0.5, 1.225, title, transform=ax.transAxes, ha="center", va="bottom", fontsize=13,
                fontweight="bold", color=th.fg)
        ax.text(0.5, 1.16, f"decisions {a + 1:,} to {b:,}, {len(p):,} pooled",
                transform=ax.transAxes, ha="center", va="bottom", fontsize=9, color=th.muted)
    axes[0].set_ylabel("The person's true approval probability")

    k = meta["kernel"]
    caption = [
        f"Limen's GP model (engine/gp, additive kernel, \u03bb = {k['Lambda']:g} decisions) on the paper's "
        f"simulated person: {persons} persons \u00d7 {n:,} decisions, seeds {meta['seeds'].replace('-', ' to ')}.",
        f"Careful profile: allow above {hi:.2f}, block below {lo:.2f}, {pct(meta['audit_rate'])} of automatic "
        f"verdicts reviewed; the model is refit {meta['refit']}.",
        f"Above each panel: the share of decisions per verdict. Shading: density, each shade an equal share. "
        f"Dots: {n_dots:,} random decisions per panel.",
        "Dashed: perfect calibration. Red box: an allow whose true approval is below 0.5, a false allow; "
        + ("none occurred." if total_fa == 0 else "\u2715 marks each one."),
    ]
    fig.text(0.5, 0.012, "\n".join(caption), ha="center", va="bottom", fontsize=8.5, color=th.muted,
             linespacing=1.45)
    return fig


def caption(fig, lines: list[str], th: Theme, *, size: float = 8.5) -> None:
    """The provenance under a figure: what ran, so a reader can trust it."""
    fig.text(0.5, 0.012, "\n".join(lines), ha="center", va="bottom", fontsize=size, color=th.muted,
             linespacing=1.45)


def head(ax, title: str, sub: str, th: Theme, *, y: float = 1.225, gap: float = 0.065) -> None:
    """A panel's title in plain words and, under it, what it holds."""
    ax.text(0.5, y, title, transform=ax.transAxes, ha="center", va="bottom", fontsize=13,
            fontweight="bold", color=th.fg)
    ax.text(0.5, y - gap, sub, transform=ax.transAxes, ha="center", va="bottom", fontsize=9, color=th.muted)


def violin(ax, x0: float, ys: np.ndarray, th: Theme, *, half: float = 0.036) -> None:
    """The density of ys along a vertical line at x0, filled in two
    shades of the density palette: the whole spread, and the middle half.
    A Gaussian kernel at Silverman's bandwidth, cut to [0, 1]."""
    sd = float(np.std(ys))
    bw = max(1.06 * sd * len(ys) ** -0.2, 0.01)
    grid = np.linspace(max(0.0, ys.min() - 2 * bw), min(1.0, ys.max() + 2 * bw), 240)
    dens = np.exp(-0.5 * ((grid[:, None] - ys[None, :]) / bw) ** 2).sum(axis=1)
    w = half * dens / dens.max()
    ax.fill_betweenx(grid, x0 - w, x0 + w, color=th.fill(0.3), lw=0, zorder=0.1)
    q1, q3 = np.quantile(ys, [0.25, 0.75])
    m = (grid >= q1) & (grid <= q3)
    ax.fill_betweenx(grid[m], x0 - w[m], x0 + w[m], color=th.fill(0.6), lw=0, zorder=0.15)
    ax.plot(x0 - w, grid, color=th.line, lw=0.6, alpha=0.75, zorder=2.5)
    ax.plot(x0 + w, grid, color=th.line, lw=0.6, alpha=0.75, zorder=2.5)


def ring(ax, x: float, y: float, th: Theme) -> None:
    """A ring that marks a computed point over dense data: the page color
    under the ink, so it shows on any shade."""
    ax.scatter([x], [y], s=70, facecolors="none", edgecolors=th.bg, linewidths=3.2, zorder=6)
    ax.scatter([x], [y], s=70, facecolors="none", edgecolors=th.fg, linewidths=1.4, zorder=6.1)


def seeds(meta: dict) -> str:
    return meta["seeds"].replace("-", " to ")


# --------------------------------------------------------------------------
# forgetting
# --------------------------------------------------------------------------

def forgetting(data: Data, th: Theme) -> plt.Figure:
    """Proposition 4: after the reset, the product kernel forgets what it
    learned about the risk of an action and asks again; the additive
    kernel keeps it."""
    meta = data.meta
    lo, hi, cp, n = meta["block_below"], meta["allow_above"], meta["changepoint"], meta["decisions"]
    kernels = [
        (data.product, "Product kernel", "everything decays with time"),
        (data.decisions, "Additive kernel", "the static risk does not decay"),
    ]
    parts = [k[cp <= k.t] for k, _, _ in kernels]
    n_dots = min(len(p) for p in parts)
    n_dots = min(n_dots, 3000)
    rng = np.random.default_rng(SEED)
    red = to_hex(th.signed(th.red_at))

    fig, axes = plt.subplots(1, 2, figsize=(7.4, 5.35), sharey=True)
    fig.subplots_adjust(left=0.09, right=0.985, bottom=0.235, top=0.8, wspace=0.08)
    for ax, p, (_, title, sub) in zip(axes, parts, kernels):
        x, y = p.p_hat.to_numpy(), p.p_star.to_numpy()
        box = dict(xy=(hi, 0), width=1 - hi, height=0.5)
        ax.add_patch(Rectangle(**box, facecolor=red, alpha=0.12, edgecolor="none", zorder=0.05))
        ax.add_patch(Rectangle(**box, facecolor="none", edgecolor=red, lw=0.7, ls=(0, (2, 1.5)), zorder=2.6))
        ax.text(hi + (1 - hi) / 2, 0.25, "false allow", rotation=90, ha="center", va="center",
                fontsize=8, color=red, zorder=5)
        density(ax, x, y, th)
        pick = rng.choice(len(p), size=n_dots, replace=False) if len(p) > n_dots else slice(None)
        ax.scatter(x[pick], y[pick], s=1.5, color=th.dot, alpha=0.3, linewidths=0, zorder=0.5)
        ax.set_rasterization_zorder(RASTER_Z)
        ax.plot([0, 1], [0, 1], ls=(0, (4, 3)), lw=0.8, color=th.muted, zorder=3)
        for v in (lo, hi):
            ax.axvline(v, lw=0.8, color=th.fg, alpha=0.6, zorder=3)
        share = p.verdict.value_counts(normalize=True)
        for x0, x1, v in ((0, lo, "block"), (lo, hi, "ask"), (hi, 1, "allow")):
            ax.text((x0 + x1) / 2, 1.02, f"{v}\n{pct(share.get(v, 0.0))}", ha="center", va="bottom",
                    fontsize=9.5, color=th.fg, linespacing=1.2, clip_on=False)

        # The actions the person nearly always approves: where the kernel
        # puts them tells what it kept of their risk.
        safe = p[p.p_star >= 0.9]
        mx, my = float(safe.p_hat.median()), float(safe.p_star.median())
        allowed = float((safe.verdict == "allow").mean())
        ring(ax, mx, my, th)
        ax.annotate(
            f"the {len(safe):,} actions the person\napproves at 0.9 or more:\n"
            f"Limen's median {mx:.2f}, {pct(allowed)} allowed",
            xy=(mx, my), xytext=(0.035, 0.965), ha="left", va="top", fontsize=8.5, color=th.fg,
            linespacing=1.35, zorder=5, bbox=backing(th),
            arrowprops=dict(arrowstyle="-|>", path_effects=halo(th), color=th.fg, lw=0.8, shrinkA=0, shrinkB=6, mutation_scale=9,
                            connectionstyle="arc3,rad=0.3"),
        )
        shown = float(p.shown.mean())
        gap = float((p.p_hat - p.p_star).abs().mean())
        ax.text(hi - 0.03, 0.05, f"shown to the person: {pct(shown)}\nmean |Limen \u2212 person|: {gap:.2f}",
                ha="right", va="bottom", fontsize=8.5, color=th.fg, linespacing=1.35, zorder=5, bbox=backing(th))
        unit_axes(ax, th)
        ax.set_xlabel("Limen's approval probability")
        head(ax, title, sub, th)
    axes[0].set_ylabel("The person's true approval probability")

    pk, ak = meta["product"], meta["kernel"]
    caption(fig, [
        f"Limen's GP model (engine/gp) on the paper's simulated person, decisions {cp + 1:,} to {n:,} "
        f"(after the trust reset),",
        f"{meta['persons']} persons, seeds {seeds(meta)} for each kernel, λ = {pk['Lambda']:g} decisions "
        f"for both. Product: σ² = {pk['Sigma2']:g}.",
        f"Additive: scales {ak['SStatic']:g}, {ak['SGlobal']:g}, {ak['SInter']:g}. Careful profile, "
        f"{pct(meta['audit_rate'])} of automatic verdicts reviewed, refit {meta['refit']}.",
        f"Shading: density, each shade an equal share. Dots: {n_dots:,} random decisions per panel. "
        f"Dashed: perfect calibration.",
    ], th)
    return fig


# --------------------------------------------------------------------------
# thresholds
# --------------------------------------------------------------------------

def thresholds(data: Data, th: Theme) -> plt.Figure:
    """limen.Costs.Thresholds over the plane of costs: the width of the ask
    range, the two thresholds, and the named profiles."""
    g, prof = data.thresholds, data.profiles
    xs = np.sort(g.false_allow.unique())
    ys = np.sort(g.false_block.unique())
    grid = g.pivot(index="false_block", columns="false_allow")
    ok = grid["ok"].to_numpy() == 1
    allow = np.where(ok, grid["allow_above"].to_numpy(), np.nan)
    block = np.where(ok, grid["block_below"].to_numpy(), np.nan)
    width = allow - block
    red = to_hex(th.signed(th.red_at))

    fig, ax = plt.subplots(figsize=(6.6, 6.9))
    fig.subplots_adjust(left=0.12, right=0.83, bottom=0.22, top=0.88)
    cf = ax.contourf(xs, ys, width, levels=np.linspace(0, 1, 11), cmap=th.fill, zorder=0.1)
    # The costs Limen refuses: the ask band would not hold one half.
    refused = (~ok).astype(float)
    ax.contourf(xs, ys, refused, levels=[0.5, 1.5], colors=[red], alpha=0.16, zorder=0.2)
    ax.set_rasterization_zorder(RASTER_Z)
    ax.contour(xs, ys, refused, levels=[0.5], colors=[red], linewidths=0.9, zorder=2.6)

    # Contours of each threshold, labelled on the line. The allow
    # threshold depends on the cost of a false allow alone, so its
    # contours are vertical; the block threshold's are horizontal.
    a_levels, b_levels = [0.65, 0.8, 0.9, 0.95, 0.98], [0.35, 0.25, 0.1, 0.05]
    ax.contour(xs, ys, allow, levels=a_levels, colors=[th.fg], linewidths=0.7, alpha=0.8, zorder=3)
    ax.contour(xs, ys, block, levels=sorted(b_levels), colors=[th.fg], linewidths=0.7, alpha=0.8,
               linestyles=[(0, (3, 2))], zorder=3)
    label_y = ys[-1] / 1.45
    for v in a_levels:
        x = xs[np.nanargmin(np.abs(allow[-1] - v))]
        ax.text(x, label_y, f"{v:g}", ha="center", va="center", fontsize=8.5, color=th.fg, zorder=5,
                bbox=backing(th))
    ax.text(xs[np.nanargmin(np.abs(allow[-1] - a_levels[0]))] / 1.12, label_y * 1.22, "allow above", ha="left",
            va="center", fontsize=8.5, color=th.fg, zorder=5, bbox=backing(th))
    label_x = 78.0
    col = int(np.searchsorted(xs, label_x))
    for v in b_levels:
        y = ys[np.nanargmin(np.abs(block[:, col] - v))]
        ax.text(label_x, y, f"{v:g}", ha="center", va="center", fontsize=8.5, color=th.fg, zorder=5,
                bbox=backing(th))
    ax.text(96, ys[np.nanargmin(np.abs(block[:, col] - b_levels[-1]))] * 1.3, "block below", ha="right",
            va="bottom", fontsize=8.5, color=th.fg, zorder=5, bbox=backing(th))

    for _, r in prof.iterrows():
        ax.scatter([r.false_allow], [r.false_block], s=48, color=th.fg, edgecolors=th.bg, linewidths=1.2, zorder=6)
        # The strictest profile sits at the right edge: its label goes
        # below and to the left, the others above and to the right.
        below = r.false_allow > 20
        ax.annotate(f"{r['name']}\nblock below {r.block_below:g}\nallow above {r.allow_above:g}",
                    xy=(r.false_allow, r.false_block), xytext=(-8, -8) if below else (8, 8),
                    textcoords="offset points", ha="right" if below else "left", va="top" if below else "bottom",
                    fontsize=8.5, color=th.fg, linespacing=1.3, zorder=6, bbox=backing(th))
    ax.text(1.33, 1.38, "refused: the ask range\nwould not contain 1/2", fontsize=8.5, color=red, ha="left",
            va="bottom", linespacing=1.3, zorder=5)

    ax.set(xscale="log", yscale="log", xlim=(xs[0], xs[-1]), ylim=(ys[0], ys[-1]), aspect="equal")
    ticks = [1.25, 2, 3, 5, 10, 20, 50, 100]
    ax.set_xticks(ticks, [f"{t:g}" for t in ticks])
    ax.set_yticks(ticks, [f"{t:g}" for t in ticks])
    ax.minorticks_off()
    for sp in ax.spines.values():
        sp.set_color(th.muted)
        sp.set_linewidth(0.8)
    ax.set_xlabel("Cost of a false allow, in asks")
    ax.set_ylabel("Cost of a false block, in asks")
    pos = ax.get_position()
    cax = fig.add_axes([pos.x1 + 0.025, pos.y0, 0.025, pos.height])
    cb = fig.colorbar(cf, cax=cax, ticks=[0, 0.25, 0.5, 0.75, 1])
    cb.set_label("Width of the ask range", color=th.fg, fontsize=10)
    cb.outline.set_edgecolor(th.muted)
    cb.ax.tick_params(colors=th.muted, labelsize=9)
    head(ax, "From costs to thresholds", "limen.Costs.Thresholds over the plane of costs", th, y=1.07, gap=0.042)
    caption(fig, [
        f"Each point of the plane is one call of limen.Costs.Thresholds with an ask of cost 1:",
        f"{len(xs)} × {len(ys)} calls, log-spaced from {xs[0]:g} to {xs[-1]:g}.",
        "Allow above 1 − ask / false allow, block below ask / false block (Chow's reject option).",
        "Solid lines: allow thresholds. Dashed lines: block thresholds. Shading: allow above minus block below.",
        "Red: costs Limen refuses, because an action it knows nothing about (1/2) would not be asked.",
        "Dots: the named profiles of specs/010; Careful is limen.DefaultCosts.",
    ], th, size=8)
    return fig


# --------------------------------------------------------------------------
# audits
# --------------------------------------------------------------------------

def audits(data: Data, th: Theme) -> plt.Figure:
    """The ledger's Horvitz-Thompson false-allow estimate beside the true
    rate, per person, at four audit rates."""
    a, meta = data.audits.copy(), data.meta
    am = meta["audits"]
    rates = sorted(a.rate.unique())
    xlim = (np.floor(a.true_rate.min() * 100) / 100 - 0.01, np.ceil(a.true_rate.max() * 100) / 100 + 0.01)
    ylim = (0, np.ceil(a.estimate.max() * 20) / 20 + 0.05)
    rng = np.random.default_rng(SEED)

    fig, axes = plt.subplots(1, len(rates), figsize=(11, 4.9), sharey=True)
    fig.subplots_adjust(left=0.06, right=0.995, bottom=0.25, top=0.83, wspace=0.07)
    for ax, rate in zip(axes, rates):
        p = a[a.rate == rate]
        x, y = p.true_rate.to_numpy(), p.estimate.to_numpy()
        density(ax, x, y, th, clip=(xlim, ylim), thresh=0.05, bw_adjust=1.0)
        ax.scatter(x, y, s=9, color=th.dot, alpha=0.55, linewidths=0, zorder=0.5)
        ax.set_rasterization_zorder(RASTER_Z)
        ax.plot([0, 1], [0, 1], ls=(0, (4, 3)), lw=0.8, color=th.muted, zorder=3)

        # One typical person: the run whose interval width is the median.
        wd = p.hi - p.lo
        t = p.loc[(wd - wd.median()).abs().idxmin()]
        ax.errorbar([t.true_rate], [t.estimate], yerr=[[t.estimate - t.lo], [t.hi - t.estimate]], fmt="o",
                    ms=5, color=th.fg, mec=th.bg, mew=0.8, elinewidth=1.1, capsize=3, zorder=6)
        ax.annotate(f"one person: {t.estimate:.3f}\n95% interval\n{t.lo:.3f} to {t.hi:.3f}",
                    xy=(t.true_rate, t.hi), xytext=(7, 0), textcoords="offset points", ha="left", va="center",
                    fontsize=7.8, color=th.fg, linespacing=1.3, zorder=5, bbox=backing(th))

        err = p.estimate - p.true_rate
        holds = float(((p.lo <= p.true_rate) & (p.true_rate <= p.hi)).mean())
        ax.text(0.04, 0.965, f"mean error {err.mean():+.4f}\nspread (sd) {err.std():.3f}\n"
                f"interval holds the truth: {pct(holds)}\nmedian: {int(p.audits.median()):,} audits "
                f"of {int(p.allows.median()):,} allows",
                transform=ax.transAxes, ha="left", va="top", fontsize=7.8, color=th.fg, linespacing=1.35,
                zorder=5, bbox=backing(th))
        ax.set(xlim=xlim, ylim=ylim)
        ax.set_xlabel("True false-allow rate")
        for sp in ax.spines.values():
            sp.set_color(th.muted)
            sp.set_linewidth(0.8)
        head(ax, f"{pct(rate)} of allows audited",
             f"{p.seed.nunique()} persons \u00d7 {p.draw.nunique()} draws", th, y=1.09, gap=0.055)
    axes[0].set_ylabel("Limen's estimate of the false-allow rate")
    caption(fig, [
        f"Limen's GP model (engine/gp, additive kernel) on the paper's simulated person under the {am['profile']} "
        f"profile (allow above {am['allow_above']:.2f}, block below {am['block_below']:.2f}),",
        f"{meta['persons']} persons × {am['draws']} draws of reviews and answers × {meta['decisions']:,} "
        f"decisions at each audit rate. True rate: the mean of 1 − p* over Limen's allows.",
        "Estimate and 95% interval: estimate.Rate over the ledger's rows (Horvitz-Thompson, Wilson at the Kish "
        "effective size). Dashed: estimate = truth.",
    ], th, size=8)
    return fig


# --------------------------------------------------------------------------
# calibration
# --------------------------------------------------------------------------

STATE_LABELS = {
    "calibrated": "calibrated",
    "underconfident": "underconfident",
    "overconfident": "overconfident",
    "not_estimable": "not estimable",
}


def calibration(data: Data, th: Theme) -> plt.Figure:
    """Each simulated person's calibration report: stated against observed
    approval rate per bin, by the bin's state."""
    c, meta = data.calibration, data.meta
    has = c[c.cases > 0]
    est = has[has.state != "not_estimable"]
    ne = has[has.state == "not_estimable"]
    style = {
        "calibrated": (th.fg, "o"),
        "underconfident": (to_hex(th.signed(0.0)), "s"),
        "overconfident": (to_hex(th.signed(th.red_at)), "D"),
    }
    strip = -0.09  # the not estimable bins sit below 0, which no rate reaches

    fig, ax = plt.subplots(figsize=(6.2, 7.75))
    fig.subplots_adjust(left=0.13, right=0.97, bottom=0.235, top=0.885)
    # Under the points, each bin's observed rates across persons as a
    # density along the bin (a violin), the middle half in the stronger
    # shade. Stated
    # rates of one bin differ little across persons, so a joint density
    # over both axes would draw streaks, not the spread across persons.
    for _, q in est.groupby("bin"):
        if len(q) >= 3:
            violin(ax, float(q.stated.mean()), q.observed.to_numpy(), th)
    ax.set_rasterization_zorder(RASTER_Z)
    ax.plot([0, 1], [0, 1], ls=(0, (4, 3)), lw=0.8, color=th.muted, zorder=3)
    for state, (color, marker) in style.items():
        q = est[est.state == state]
        ax.vlines(q.stated, q.lo, q.hi, color=color, lw=0.6, alpha=0.4, zorder=3.5)
        ax.scatter(q.stated, q.observed, s=20, marker=marker, color=color, edgecolors=th.bg, linewidths=0.6,
                   zorder=4, label=f"{STATE_LABELS[state]}: {len(q)}")
    leg = ax.legend(loc="upper left", fontsize=8.5, frameon=True, handletextpad=0.3, borderpad=0.6,
                    title=f"{len(has):,} bins with decisions", title_fontsize=8.5, alignment="left")
    leg.get_frame().set(facecolor=th.bg, alpha=0.75, edgecolor="none")
    leg.get_title().set_color(th.fg)
    for t in leg.get_texts():
        t.set_color(th.fg)

    # The bins below 1/2 hold few reviews: blocks are reviewed only when
    # audited. They are not estimable and have no observed rate.
    ax.axhline(0, color=th.muted, lw=0.8, zorder=3)
    jit = np.random.default_rng(SEED).uniform(-0.022, 0.022, len(ne))
    ax.scatter(ne.stated, strip / 2 + jit, s=14, facecolors="none", edgecolors=th.muted, linewidths=0.8, zorder=4)
    ax.text(0.985, strip / 2, f"not estimable: {len(ne)}", ha="right", va="center", fontsize=8.5, color=th.fg,
            zorder=5)

    # The bin the person approves more often than Limen says.
    under = est[est.state == "underconfident"]
    if len(under):
        top = under.bin.value_counts().idxmax()
        q = under[under.bin == top]
        f, t_ = q["from"].iloc[0], q["to"].iloc[0]
        mx, my = q.stated.mean(), q.observed.mean()
        ring(ax, mx, my, th)
        ax.annotate(f"bin {f:.1f} to {t_:.1f}: underconfident for\n{len(q)} of {meta['persons']} persons. "
                    f"Limen says {mx:.2f},\nthe reviews approve {my:.2f}:\nit errs toward asking",
                    xy=(mx, my), xytext=(0.985, 0.33), ha="right", va="bottom", fontsize=8.5, color=th.fg,
                    linespacing=1.35, zorder=5, bbox=backing(th),
                    arrowprops=dict(arrowstyle="-|>", path_effects=halo(th), color=th.fg, lw=0.8, shrinkA=0, shrinkB=6, mutation_scale=9,
                                    connectionstyle="arc3,rad=0.2"))
    ticks = [0, 0.25, 0.5, 0.75, 1]
    names = ["0", ".25", ".5", ".75", "1"]
    ax.set(xlim=(0, 1), ylim=(strip, 1), aspect="equal")
    ax.set_xticks(ticks, names)
    ax.set_yticks(ticks, names)
    for sp in ax.spines.values():
        sp.set_color(th.muted)
        sp.set_linewidth(0.8)
    ax.set_xlabel("Approval probability Limen stated (mean of the bin)")
    ax.set_ylabel("Approval rate the reviews observed")
    head(ax, "Calibration report, per person and bin",
         f"{meta['persons']} persons, {meta['calibration']['bins']} bins each", th, y=1.06, gap=0.04)
    empty = int((c.cases == 0).sum())
    caption(fig, [
        f"Limen's GP model (engine/gp, additive kernel) on the paper's simulated person:",
        f"{meta['persons']} persons × {meta['decisions']:,} decisions, seeds {seeds(meta)}, Careful profile, "
        f"{pct(meta['audit_rate'])} of automatic verdicts reviewed.",
        "Each person's report is estimate.Calibrate over the whole history, the arithmetic of GET /v1/calibration.",
        f"A bin whose reviews have a Kish effective size below {meta['calibration']['min_effective']} is not "
        f"estimable; {empty} empty bins are left out.",
        "Lines: the 95% interval of each bin. Shading: the density of a bin's observed rate",
        "across persons, the middle half in the stronger shade. Dashed: stated = observed.",
    ], th, size=8)
    return fig


# --------------------------------------------------------------------------
# cover
# --------------------------------------------------------------------------

def cover(data: Data, th: Theme) -> plt.Figure:
    """Limen's engine with its cover rule: the approval probability against
    the weight of the approvals that cover the action, by what the engine
    suggested."""
    c, cm = data.cover, data.meta["cover"]
    hi, min_w, min_s = cm["allow_above"], cm["min_weight"], cm["min_sessions"]

    def yt(w):
        return np.log10(1 + np.asarray(w, dtype=float))

    ymax = yt(c.weight.max() * 1.15)
    rng = np.random.default_rng(SEED)
    out = np.where(c.verdict == "allow", "allow", np.where(c.ask_kind.isin(["few", "wider"]), c.ask_kind, "other"))
    c = c.assign(out=out, y=yt(c.weight))

    fig, ax = plt.subplots(figsize=(6.8, 7.1))
    fig.subplots_adjust(left=0.12, right=0.97, bottom=0.255, top=0.87)
    density(ax, c.approve.to_numpy(), c.y.to_numpy(), th, clip=((0, 1), (0, ymax)), thresh=0.04)
    n_dots = min(len(c), 3000)
    pick = rng.choice(len(c), size=n_dots, replace=False)
    d = c.iloc[pick]
    jit = rng.uniform(-0.015, 0.015, len(d))
    ax.scatter(d.approve, d.y + jit, s=1.6, color=th.dot, alpha=0.3, linewidths=0, zorder=0.5)
    ax.set_rasterization_zorder(RASTER_Z)

    wy = float(yt(min_w))
    ax.axvline(hi, lw=0.8, color=th.fg, alpha=0.6, zorder=3)
    ax.axhline(wy, lw=0.8, color=th.fg, alpha=0.6, ls=(0, (4, 3)), zorder=3)
    few = c[c.out == "few"]
    wider = c[c.out == "wider"]
    blue = to_hex(th.signed(0.0))
    red = to_hex(th.signed(th.red_at))
    ax.scatter(few.approve, few.y + rng.uniform(-0.015, 0.015, len(few)), s=16, marker="s", facecolors="none",
               edgecolors=blue, linewidths=0.8, zorder=4)
    ax.scatter(wider.approve, wider.y, s=46, marker="^", color=red, edgecolors=th.bg, linewidths=0.6, zorder=4.1)

    above = c[c.approve > hi]
    allow = above[above.out == "allow"]
    one = int((few.sessions < min_s).sum())
    ax.text(0.985, 0.985, f"above {hi:.2f}: {len(above):,} decisions\n"
            f"allow: {len(allow):,} ({pct(len(allow) / len(above))}), covered",
            transform=ax.transAxes, ha="right", va="top", fontsize=8.5, color=th.fg, linespacing=1.35,
            zorder=5, bbox=backing(th))
    if len(few):
        ax.annotate(f"few: {len(few):,}\n{one:,} of them from\none session only",
                    xy=(few.approve.median(), few.y.median()), xytext=(0.6, 0.58), textcoords="axes fraction",
                    ha="right", va="center", fontsize=8.5, color=th.fg, linespacing=1.35, zorder=5, bbox=backing(th),
                    arrowprops=dict(arrowstyle="-|>", path_effects=halo(th), color=blue, lw=0.8, shrinkA=0, shrinkB=4, mutation_scale=8))
    if len(wider):
        ax.annotate(f"wider: {len(wider):,}\nno approval of this reach",
                    xy=(wider.approve.iloc[0], wider.y.iloc[0]), xytext=(0.6, 0.12), textcoords="axes fraction",
                    ha="right", va="center", fontsize=8.5, color=th.fg, linespacing=1.35, zorder=5, bbox=backing(th),
                    arrowprops=dict(arrowstyle="-|>", path_effects=halo(th), color=red, lw=0.8, shrinkA=0, shrinkB=4, mutation_scale=8))
    rest = c[c.approve <= hi]
    asks = int((rest.verdict == "ask").sum())
    blocks = int((c.verdict == "block").sum())
    ax.text(0.035, 0.985, f"at {hi:.2f} or below: {len(rest):,}\nask: {asks:,}, block: {blocks:,}",
            transform=ax.transAxes, ha="left", va="top", fontsize=8.5, color=th.fg, linespacing=1.35, zorder=5,
            bbox=backing(th))
    ax.text(0.02, wy, f"weight {min_w}", transform=ax.get_yaxis_transform(), ha="left", va="bottom",
            fontsize=8, color=th.fg, zorder=5)

    ticks = [0, 1, 3, 10, 30, 100]
    ax.set(xlim=(0, 1), ylim=(0, ymax))
    ax.set_yticks(yt(ticks), [str(t) for t in ticks])
    ax.set_xticks([0, 0.25, 0.5, 0.75, 1], ["0", ".25", ".5", ".75", "1"])
    for sp in ax.spines.values():
        sp.set_color(th.muted)
        sp.set_linewidth(0.8)
    ax.set_xlabel("Approval probability")
    ax.set_ylabel("Weight of the approvals that cover the action")
    head(ax, "The cover rule", f"{c.seed.nunique()} persons × {cm['decisions']} decisions, "
         f"a new session each {cm['session']}", th, y=1.07, gap=0.042)
    kinds = {k["name"]: k["approve"] for k in cm["kinds"]}
    caption(fig, [
        "Limen itself: the engine (learned/1) with its feature extractor and cover rule, one engine.Predict call",
        f"per new action, verdict.Decide with {pct(cm['audit_rate'])} audits, each shown action answered. "
        "Careful profile.",
        "The simulated person approves " + ", ".join(f"{n} {v:g}" for n, v in list(kinds.items())[:3]) + ",",
        ", ".join(f"{n} {v:g}" for n, v in list(kinds.items())[3:]) + ". Half the answers open the details.",
        f"Answer times are log-normal around 3 s: an approval under {cm['answer_floor_ms'] / 1000:g} s with "
        "the details closed has weight 0.",
        f"Dots: {n_dots:,} random decisions, moved a little up or down. Squares: each few ask. "
        "Triangles: each wider ask.",
    ], th, size=7.6)
    return fig


FIGURES = {
    "teaser": teaser,
    "forgetting": forgetting,
    "thresholds": thresholds,
    "audits": audits,
    "calibration": calibration,
    "cover": cover,
}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("names", nargs="*", metavar="name",
                    help=f"figures to draw, of {', '.join(FIGURES)}; all when none is named")
    ap.add_argument("--preview", type=Path, help="also write a PNG of each variant into this directory")
    args = ap.parse_args()
    if unknown := [n for n in args.names if n not in FIGURES]:
        ap.error(f"no figure named {', '.join(unknown)}")
    data = Data()
    for name in args.names or FIGURES:
        for th in THEMES:
            apply_theme(th)
            fig = FIGURES[name](data, th)
            out = HERE / f"{name}{th.suffix}.svg"
            fig.savefig(out, dpi=220, metadata={"Date": None})
            print(f"{out.name}: {out.stat().st_size / 1024:.0f} KiB")
            if args.preview:
                args.preview.mkdir(parents=True, exist_ok=True)
                fig.savefig(args.preview / f"{name}-preview-{th.name}.png", dpi=200)
            plt.close(fig)


if __name__ == "__main__":
    main()
