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

data/decisions.csv.gz holds one row per decision of each simulated person
and data/meta.json the settings of the run; `go run ./cmd/simulate` writes
both. Every mark is drawn from those rows and every number in a label is
computed from them. Each figure is drawn for a light and a dark page, as
<name>.svg and <name>-dark.svg next to this file.

A figure is a function (rows, settings, theme) -> Figure, listed in FIGURES.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import numpy as np  # noqa: E402
import pandas as pd  # noqa: E402
import seaborn as sns  # noqa: E402
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


def load() -> tuple[pd.DataFrame, dict]:
    d = pd.read_csv(DATA / "decisions.csv.gz", dtype={"verdict": str, "applied": str})
    meta = json.loads((DATA / "meta.json").read_text())
    return d, meta


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


def density(ax, x, y, th: Theme, *, levels: int = 10, thresh: float = 0.03, z: float = 0.1) -> None:
    """The joint density of (x, y) on the unit square, filled and outlined.

    seaborn draws the fill at iso-proportion levels, so each band holds the
    same share of the points; the colors step evenly by band, not by the
    density's height, or the peak would wash the rest out.
    """
    kw = dict(clip=((0, 1), (0, 1)), gridsize=200, bw_adjust=1.1, thresh=thresh)
    sns.kdeplot(x=x, y=y, ax=ax, fill=True, levels=levels, cmap=th.fill, zorder=z, **kw)
    cs = ax.collections[-1]
    cs.set_norm(BoundaryNorm(cs.levels, th.fill.N))
    cs.changed()
    sns.kdeplot(x=x, y=y, ax=ax, levels=5, color=th.line, linewidths=0.6, alpha=0.75, zorder=2.5, **kw)


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

def teaser(d: pd.DataFrame, meta: dict, th: Theme) -> plt.Figure:
    """Limen's predicted approval against the person's true approval, in
    three phases of the history, pooled across persons."""
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
                arrowprops=dict(arrowstyle="-|>", color=th.fg, lw=0.8, shrinkA=0, shrinkB=1, mutation_scale=8),
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


FIGURES = {"teaser": teaser}


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("names", nargs="*", metavar="name",
                    help=f"figures to draw, of {', '.join(FIGURES)}; all when none is named")
    ap.add_argument("--preview", type=Path, help="also write a PNG of each variant into this directory")
    args = ap.parse_args()
    if unknown := [n for n in args.names if n not in FIGURES]:
        ap.error(f"no figure named {', '.join(unknown)}")
    d, meta = load()
    for name in args.names or FIGURES:
        for th in THEMES:
            apply_theme(th)
            fig = FIGURES[name](d, meta, th)
            out = HERE / f"{name}{th.suffix}.svg"
            fig.savefig(out, dpi=220, metadata={"Date": None})
            print(f"{out.name}: {out.stat().st_size / 1024:.0f} KiB")
            if args.preview:
                args.preview.mkdir(parents=True, exist_ok=True)
                fig.savefig(args.preview / f"{name}-preview-{th.name}.png", dpi=200)
            plt.close(fig)


if __name__ == "__main__":
    main()
