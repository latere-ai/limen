// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package kernel

import (
	"encoding/json/v2"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// fixture mirrors the research implementation's kernel fixtures, written
// by trustcalib's go/testdata/gen/export_fixtures.py from the Python
// reference (see ../testdata/fixtures/README.md).
type fixture struct {
	Kernel struct {
		Sigma2  float64 `json:"sigma2"`
		SStatic float64 `json:"s_static"`
		SGlobal float64 `json:"s_global"`
		SInter  float64 `json:"s_inter"`
		LTool   float64 `json:"l_tool"`
		LCtx    float64 `json:"l_ctx"`
		Lam     float64 `json:"lam"`
	} `json:"kernel"`
	Train struct {
		PhiTool [][]float64 `json:"phi_tool"`
		PhiCtx  [][]float64 `json:"phi_ctx"`
		T       []float64   `json:"t"`
	} `json:"train"`
	K [][]float64 `json:"K"`
}

func load(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func (f fixture) points() []Point {
	out := make([]Point, len(f.Train.T))
	for i := range out {
		out[i] = Point{Tool: f.Train.PhiTool[i], Ctx: f.Train.PhiCtx[i], T: f.Train.T[i]}
	}
	return out
}

// The kernels reproduce the Python reference's matrices.
func TestGolden(t *testing.T) {
	var prod, add fixture
	load(t, "kernel_full.json", &prod)
	load(t, "kernel_additive.json", &add)
	cases := []struct {
		name string
		k    Kernel
		fix  fixture
	}{
		{"product", Product{Sigma2: prod.Kernel.Sigma2, LTool: prod.Kernel.LTool, LCtx: prod.Kernel.LCtx, Lambda: prod.Kernel.Lam}, prod},
		{"additive", Additive{SStatic: add.Kernel.SStatic, SGlobal: add.Kernel.SGlobal, SInter: add.Kernel.SInter,
			LTool: add.Kernel.LTool, LCtx: add.Kernel.LCtx, Lambda: add.Kernel.Lam}, add},
	}
	for _, c := range cases {
		pts := c.fix.points()
		worst := 0.0
		for i := range pts {
			for j := range pts {
				worst = max(worst, math.Abs(c.k.Cov(pts[i], pts[j])-c.fix.K[i][j]))
			}
		}
		if worst > 1e-12 {
			t.Errorf("%s: largest deviation from the reference %.3g", c.name, worst)
		}
	}
}

func randPoints(rng *rand.Rand, n, dTool, dCtx int, spread float64) []Point {
	out := make([]Point, n)
	for i := range out {
		tool := make([]float64, dTool)
		ctx := make([]float64, dCtx)
		for j := range tool {
			tool[j] = rng.Float64()
		}
		for j := range ctx {
			ctx[j] = rng.Float64()
		}
		out[i] = Point{Tool: tool, Ctx: ctx, T: spread * float64(i)}
	}
	return out
}

// Both kernels are symmetric, positive semidefinite, and have the prior
// variance on the diagonal.
func TestPSD(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	pts := randPoints(rng, 30, 11, 9, 17)
	for name, k := range map[string]Kernel{"product": DefaultProduct(), "additive": DefaultAdditive()} {
		n := len(pts)
		K := mat.NewSymDense(n, nil)
		for i := range pts {
			if v := k.Cov(pts[i], pts[i]); math.Abs(v-k.Var()) > 1e-12 {
				t.Errorf("%s: k(x, x) = %v, Var = %v", name, v, k.Var())
			}
			for j := range pts {
				if k.Cov(pts[i], pts[j]) != k.Cov(pts[j], pts[i]) {
					t.Fatalf("%s: not symmetric at (%d, %d)", name, i, j)
				}
				if j >= i {
					K.SetSym(i, j, k.Cov(pts[i], pts[j]))
				}
			}
		}
		var eig mat.EigenSym
		if !eig.Factorize(K, false) {
			t.Fatalf("%s: eigendecomposition failed", name)
		}
		for _, v := range eig.Values(nil) {
			if v < -1e-9 {
				t.Fatalf("%s: eigenvalue %v", name, v)
			}
		}
	}
}

// Proposition 4 at the level of the kernel: past both points, moving one of
// them Delta decisions later scales the product kernel by exp(-Delta /
// Lambda), and the additive kernel tends to its static term.
func TestForgetting(t *testing.T) {
	a := Point{Tool: []float64{0.3, 0.7, 0.1}, Ctx: []float64{0.5}, T: 10}
	b := Point{Tool: []float64{0.2, 0.6, 0.4}, Ctx: []float64{0.4}, T: 4}
	pk, ak := DefaultProduct(), DefaultAdditive()
	for _, delta := range []float64{0, 1, 45, 90, 400} {
		later := a
		later.T += delta
		want := math.Exp(-delta/pk.Lambda) * pk.Cov(a, b)
		if got := pk.Cov(later, b); math.Abs(got-want) > 1e-12 {
			t.Errorf("product at +%v: %v, want %v", delta, got, want)
		}
	}
	far := a
	far.T += 1e6
	if got, want := ak.Cov(far, b), ak.StaticCov(a, b); math.Abs(got-want) > 1e-12 {
		t.Errorf("additive far apart: %v, want its static term %v", got, want)
	}
	if got := pk.Cov(far, b); got > 1e-300 {
		t.Errorf("product far apart: %v, want 0", got)
	}
	// Unrelated actions at the same moment share the tolerance term.
	u := Point{Tool: []float64{100, 100, 100}, Ctx: []float64{100}, T: a.T}
	if got := ak.Cov(a, u); math.Abs(got-ak.SGlobal) > 1e-12 {
		t.Errorf("additive, unrelated actions: %v, want SGlobal %v", got, ak.SGlobal)
	}
	if got := pk.Cov(a, u); got > 1e-300 {
		t.Errorf("product, unrelated actions: %v, want 0", got)
	}
}

// A block missing an entry compares it against zero.
func TestUnequalBlocks(t *testing.T) {
	a := Point{Tool: []float64{1, 2}, Ctx: []float64{3}}
	b := Point{Tool: []float64{1}, Ctx: []float64{3, 0}}
	want := Static(Point{Tool: []float64{1, 2}, Ctx: []float64{3}}, Point{Tool: []float64{1, 0}, Ctx: []float64{3}}, 1.1, 1.2)
	if got := Static(a, b, 1.1, 1.2); got != want {
		t.Errorf("Static = %v, want %v", got, want)
	}
	if Static(b, a, 1.1, 1.2) != want {
		t.Error("Static is not symmetric for unequal blocks")
	}
}

func TestValidate(t *testing.T) {
	for _, k := range []Kernel{DefaultProduct(), DefaultAdditive(), Additive{SStatic: 1, LTool: 1, LCtx: 1, Lambda: 1}} {
		if err := k.Validate(); err != nil {
			t.Errorf("%+v: %v", k, err)
		}
	}
	bad := []Kernel{
		Product{Sigma2: 0, LTool: 1, LCtx: 1, Lambda: 1},
		Product{Sigma2: 1, LTool: 0, LCtx: 1, Lambda: 1},
		Product{Sigma2: 1, LTool: 1, LCtx: math.Inf(1), Lambda: 1},
		Product{Sigma2: 1, LTool: 1, LCtx: 1, Lambda: math.NaN()},
		Additive{LTool: 1, LCtx: 1, Lambda: 1},
		Additive{SStatic: -1, SGlobal: 2, LTool: 1, LCtx: 1, Lambda: 1},
		Additive{SStatic: 1, LTool: 1, LCtx: 1, Lambda: 0},
	}
	for _, k := range bad {
		if k.Validate() == nil {
			t.Errorf("%+v is valid", k)
		}
	}
}
