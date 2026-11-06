// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package gp

import (
	"encoding/json/v2"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"testing"

	"gonum.org/v1/gonum/mat"

	"latere.ai/x/limen/engine/kernel"
)

// The fixtures are the research implementation's, written by trustcalib's
// go/testdata/gen/export_fixtures.py from the Python reference (see
// ../testdata/fixtures/README.md).
type kernelJSON struct {
	Sigma2  float64 `json:"sigma2"`
	SStatic float64 `json:"s_static"`
	SGlobal float64 `json:"s_global"`
	SInter  float64 `json:"s_inter"`
	LTool   float64 `json:"l_tool"`
	LCtx    float64 `json:"l_ctx"`
	Lam     float64 `json:"lam"`
}

func (k kernelJSON) kernel() kernel.Kernel {
	if k.Sigma2 > 0 {
		return kernel.Product{Sigma2: k.Sigma2, LTool: k.LTool, LCtx: k.LCtx, Lambda: k.Lam}
	}
	return kernel.Additive{SStatic: k.SStatic, SGlobal: k.SGlobal, SInter: k.SInter, LTool: k.LTool, LCtx: k.LCtx, Lambda: k.Lam}
}

type pointsJSON struct {
	PhiTool [][]float64 `json:"phi_tool"`
	PhiCtx  [][]float64 `json:"phi_ctx"`
	T       []float64   `json:"t"`
}

func (p pointsJSON) points() []kernel.Point {
	out := make([]kernel.Point, len(p.T))
	for i := range out {
		out[i] = kernel.Point{Tool: p.PhiTool[i], Ctx: p.PhiCtx[i], T: p.T[i]}
	}
	return out
}

func approve(y01 []int) []bool {
	out := make([]bool, len(y01))
	for i, y := range y01 {
		out[i] = y > 0
	}
	return out
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

// closeTo fails when any entry deviates from the reference by more than tol,
// and reports the largest deviation either way.
func closeTo(t *testing.T, name string, got, want []float64, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: length %d, want %d", name, len(got), len(want))
	}
	worst := 0.0
	for i := range got {
		worst = max(worst, math.Abs(got[i]-want[i]))
	}
	if worst > tol {
		t.Errorf("%s: largest deviation from the reference %.3g, tolerance %.3g", name, worst, tol)
	}
	t.Logf("%s: largest deviation from the reference %.3g", name, worst)
}

// The parity tolerances. The fitted mode is the reference's Newton iterate
// at the same stopping rule, so it agrees to rounding: the largest
// deviation measured is about 1e-15. 1e-12 leaves room for a different
// order of floating-point summation in the factorization and nothing more.
const (
	tolFit     = 1e-12
	tolPredict = 1e-12
)

func TestFitGolden(t *testing.T) {
	for _, name := range []string{"laplace_fit.json", "laplace_fit_additive.json"} {
		var fix struct {
			Kernel      kernelJSON `json:"kernel"`
			Train       pointsJSON `json:"train"`
			Y01         []int      `json:"y01"`
			FHat        []float64  `json:"f_hat"`
			Grad        []float64  `json:"grad"`
			LogMarginal float64    `json:"log_marginal"`
		}
		load(t, name, &fix)
		p, err := Fit(fix.Kernel.kernel(), fix.Train.points(), approve(fix.Y01), Options{})
		if err != nil {
			t.Fatal(err)
		}
		closeTo(t, name+" f_hat", p.fHat, fix.FHat, tolFit)
		closeTo(t, name+" grad", p.grad, fix.Grad, tolFit)
		closeTo(t, name+" log_marginal", []float64{p.LogMarginal()}, []float64{fix.LogMarginal}, tolFit)
	}
}

func TestPredictGolden(t *testing.T) {
	for _, name := range []string{"predict.json", "predict_additive.json"} {
		var fix struct {
			Kernel kernelJSON `json:"kernel"`
			Train  pointsJSON `json:"train"`
			Y01    []int      `json:"y01"`
			Query  pointsJSON `json:"query"`
			FBar   []float64  `json:"f_bar"`
			Var    []float64  `json:"var"`
			Pi     []float64  `json:"pi"`
		}
		load(t, name, &fix)
		p, err := Fit(fix.Kernel.kernel(), fix.Train.points(), approve(fix.Y01), Options{})
		if err != nil {
			t.Fatal(err)
		}
		preds := p.PredictAll(fix.Query.points())
		var mean, variance, pi []float64
		for _, pr := range preds {
			mean = append(mean, pr.Mean)
			variance = append(variance, pr.Var)
			pi = append(pi, pr.Approve)
		}
		closeTo(t, name+" f_bar", mean, fix.FBar, tolPredict)
		closeTo(t, name+" var", variance, fix.Var, tolPredict)
		closeTo(t, name+" pi", pi, fix.Pi, tolPredict)
	}
}

func TestLogCDFGolden(t *testing.T) {
	var fix struct {
		Z      []float64 `json:"z"`
		LogCDF []float64 `json:"log_cdf"`
		LogPDF []float64 `json:"log_pdf"`
	}
	load(t, "logcdf.json", &fix)
	for i, z := range fix.Z {
		if got := logCDF(z); math.Abs(got-fix.LogCDF[i]) > 1e-10 {
			t.Errorf("logCDF(%g) = %.15g, want %.15g", z, got, fix.LogCDF[i])
		}
		if got := logPDF(z); math.Abs(got-fix.LogPDF[i]) > 1e-12 {
			t.Errorf("logPDF(%g) = %.15g, want %.15g", z, got, fix.LogPDF[i])
		}
	}
}

// The analytic gradient of sum log Phi(y f) matches a central difference,
// and the extremes stay finite with W at or above its floor.
func TestProbit(t *testing.T) {
	f := []float64{0.3, -1.2, 2.1, -0.4, 0.05, -3.0}
	y := []float64{1, -1, 1, 1, -1, -1}
	_, grad, _ := probit(f, y, nil)
	sum := func(f []float64) float64 {
		s, _, _ := probit(f, y, nil)
		return s
	}
	const eps = 1e-6
	for i := range f {
		fp, fm := slices.Clone(f), slices.Clone(f)
		fp[i] += eps
		fm[i] -= eps
		if d := (sum(fp) - sum(fm)) / (2 * eps); math.Abs(d-grad[i]) > 1e-6 {
			t.Errorf("grad[%d] = %.8g, central difference %.8g", i, grad[i], d)
		}
	}
	_, grad, w := probit([]float64{50, -50}, []float64{1, 1}, nil)
	for i := range grad {
		if math.IsNaN(grad[i]) || math.IsInf(grad[i], 0) || w[i] < 1e-10 {
			t.Errorf("extreme %d: grad %v, W %v", i, grad[i], w[i])
		}
	}
}

func randTrain(rng *rand.Rand, n int) ([]kernel.Point, []bool) {
	pts := make([]kernel.Point, n)
	ys := make([]bool, n)
	for i := range n {
		tool, ctx := make([]float64, 11), make([]float64, 9)
		for j := range tool {
			tool[j] = rng.Float64()
		}
		for j := range ctx {
			ctx[j] = rng.Float64()
		}
		pts[i] = kernel.Point{Tool: tool, Ctx: ctx, T: float64(i)}
		ys[i] = ctx[0]+0.3*(tool[0]-0.5) > 0.5
	}
	return pts, ys
}

// At the mode, f_hat = K grad (Rasmussen and Williams, Algorithm 3.1), and
// the log marginal likelihood is finite.
func TestModeStationarity(t *testing.T) {
	pts, ys := randTrain(rand.New(rand.NewPCG(7, 7)), 40)
	k := kernel.DefaultAdditive()
	p, err := Fit(k, pts, ys, Options{})
	if err != nil {
		t.Fatal(err)
	}
	resid := 0.0
	for i := range pts {
		kg := 0.0
		for j := range pts {
			c := k.Cov(pts[i], pts[j])
			if i == j {
				c += 1e-6
			}
			kg += c * p.grad[j]
		}
		resid += (p.fHat[i] - kg) * (p.fHat[i] - kg)
	}
	if math.Sqrt(resid) > 1e-8 {
		t.Errorf("stationarity residual %.3g", math.Sqrt(resid))
	}
	if lz := p.LogMarginal(); math.IsNaN(lz) || math.IsInf(lz, 0) || lz >= 0 {
		t.Errorf("log marginal %v", lz)
	}
}

// With a one-dimensional probit ground truth f(x) = x, the posterior
// recovers a monotone boundary near zero.
func TestRecovery(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	line := func(x float64) kernel.Point { return kernel.Point{Tool: []float64{x}, Ctx: []float64{0}} }
	var pts []kernel.Point
	var ys []bool
	for range 300 {
		x := -3 + 6*rng.Float64()
		pts = append(pts, line(x))
		ys = append(ys, rng.Float64() < cdf(x))
	}
	p, err := Fit(kernel.DefaultProduct(), pts, ys, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var xs, pis []float64
	cross, best := 0.0, math.Inf(1)
	for i := range 41 {
		x := -2 + 4*float64(i)/40
		pi := p.Predict(line(x)).Approve
		xs, pis = append(xs, x), append(pis, pi)
		if d := math.Abs(pi - 0.5); d < best {
			best, cross = d, x
		}
	}
	if rho := spearman(xs, pis); rho < 0.98 {
		t.Errorf("rank correlation %.4f", rho)
	}
	if math.Abs(cross) > 0.6 {
		t.Errorf("boundary at %.3f", cross)
	}
}

func spearman(x, y []float64) float64 {
	rank := func(v []float64) []float64 {
		idx := make([]int, len(v))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
		r := make([]float64, len(v))
		for pos, i := range idx {
			r[i] = float64(pos)
		}
		return r
	}
	rx, ry := rank(x), rank(y)
	n := float64(len(x))
	d2 := 0.0
	for i := range rx {
		d2 += (rx[i] - ry[i]) * (rx[i] - ry[i])
	}
	return 1 - 6*d2/(n*(n*n-1))
}

// Proposition 4 on a fitted posterior. Past every answer, moving a query
// Delta decisions later scales the product kernel's mean by
// exp(-Delta/Lambda) and its variance reduction by exp(-2 Delta/Lambda), so
// the approval probability returns to one half; the additive kernel's mean
// tends to its static part, sum_i SStatic k_x(x_i, x) grad_i.
func TestForgetting(t *testing.T) {
	pts, ys := randTrain(rand.New(rand.NewPCG(11, 11)), 60)
	q := kernel.Point{Tool: pts[3].Tool, Ctx: pts[3].Ctx, T: 60}
	at := func(delta float64) kernel.Point { r := q; r.T += delta; return r }

	pk := kernel.DefaultProduct()
	pp, err := Fit(pk, pts, ys, Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := pp.Predict(q)
	for _, delta := range []float64{30, 90, 270} {
		got := pp.Predict(at(delta))
		if want := math.Exp(-delta/pk.Lambda) * base.Mean; math.Abs(got.Mean-want) > 1e-12 {
			t.Errorf("product mean at +%v: %v, want %v", delta, got.Mean, want)
		}
		want := pk.Var() - math.Exp(-2*delta/pk.Lambda)*(pk.Var()-base.Var)
		if math.Abs(got.Var-want) > 1e-9 {
			t.Errorf("product variance at +%v: %v, want %v", delta, got.Var, want)
		}
	}
	if pi := pp.Predict(at(5000)).Approve; math.Abs(pi-0.5) > 1e-9 {
		t.Errorf("product approval long after the last answer: %v, want 0.5", pi)
	}

	ak := kernel.DefaultAdditive()
	ap, err := Fit(ak, pts, ys, Options{})
	if err != nil {
		t.Fatal(err)
	}
	static := 0.0
	for i, x := range pts {
		static += ak.StaticCov(x, q) * ap.grad[i]
	}
	if got := ap.Predict(at(1e5)).Mean; math.Abs(got-static) > 1e-9 {
		t.Errorf("additive mean long after: %v, want the static part %v", got, static)
	}
	if math.Abs(static) < 1e-3 {
		t.Errorf("the static part %v is too small for the test to mean anything", static)
	}
}

func TestPrior(t *testing.T) {
	k := kernel.DefaultAdditive()
	for _, p := range []*Posterior{Prior(k), must(Fit(k, nil, nil, Options{}))} {
		got := p.Predict(kernel.Point{Tool: []float64{1}, Ctx: []float64{1}})
		if got != (Prediction{Mean: 0, Var: k.Var(), Approve: 0.5}) {
			t.Errorf("prior predicts %+v", got)
		}
		if p.Len() != 0 || p.LogMarginal() != 0 || p.Kernel() != kernel.Kernel(k) {
			t.Errorf("prior %+v", p)
		}
	}
}

func must(p *Posterior, err error) *Posterior {
	if err != nil {
		panic(err)
	}
	return p
}

// Fit keeps the most recent answers: a cap of n gives the same posterior as
// fitting the last n alone.
func TestMaxLabels(t *testing.T) {
	pts, ys := randTrain(rand.New(rand.NewPCG(5, 5)), 30)
	k := kernel.DefaultAdditive()
	capped := must(Fit(k, pts, ys, Options{MaxLabels: 10}))
	last := must(Fit(k, pts[20:], ys[20:], Options{MaxLabels: -1}))
	if capped.Len() != 10 || !slices.Equal(capped.fHat, last.fHat) {
		t.Errorf("capped %d answers, mode differs from the last ten", capped.Len())
	}
	if all := must(Fit(k, pts, ys, Options{MaxLabels: -1})); all.Len() != 30 {
		t.Errorf("uncapped kept %d", all.Len())
	}
	if def := must(Fit(k, pts, ys, Options{})); def.Len() != 30 {
		t.Errorf("default cap kept %d of 30", def.Len())
	}
}

// The same inputs give bitwise the same posterior, a caller's later writes
// do not reach it, and predictions from many goroutines agree.
func TestDeterministicImmutableConcurrent(t *testing.T) {
	pts, ys := randTrain(rand.New(rand.NewPCG(9, 9)), 120)
	k := kernel.DefaultAdditive()
	a := must(Fit(k, pts, ys, Options{}))
	b := must(Fit(k, pts, ys, Options{}))
	if !slices.Equal(a.fHat, b.fHat) || !slices.Equal(a.l, b.l) || a.LogMarginal() != b.LogMarginal() {
		t.Fatal("two fits of the same answers differ")
	}
	q := kernel.Point{Tool: slices.Clone(pts[0].Tool), Ctx: slices.Clone(pts[0].Ctx), T: 130}
	want := a.Predict(q)
	pts[0].Tool[0] += 10
	if got := a.Predict(q); got != want {
		t.Fatal("a write to the caller's points reached the posterior")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if got := a.Predict(q); got != want {
					t.Error("a concurrent prediction differs")
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestFitErrors(t *testing.T) {
	k := kernel.DefaultProduct()
	one := []kernel.Point{{Tool: []float64{0}, Ctx: []float64{0}}}
	if _, err := Fit(k, one, nil, Options{}); err == nil {
		t.Error("a missing answer was accepted")
	}
	if _, err := Fit(kernel.Product{}, one, []bool{true}, Options{}); err == nil {
		t.Error("an invalid kernel was accepted")
	}
	nan := []kernel.Point{{Tool: []float64{math.NaN()}, Ctx: []float64{0}}, {Tool: []float64{0}, Ctx: []float64{0}}}
	if _, err := Fit(k, nan, []bool{true, false}, Options{}); err == nil {
		t.Error("a point that is not finite was accepted")
	}
}

// The final factorization fails when the mode is not finite, which the
// loop's own factorization rules out for finite inputs; exercise it
// directly.
func TestFactorizeRefusesNaN(t *testing.T) {
	K := mat.NewSymDense(1, []float64{math.NaN()})
	var chol mat.Cholesky
	if factorize(&chol, K, []float64{1}) {
		t.Error("a NaN kernel matrix factorized")
	}
	if _, err := fit(kernel.DefaultProduct(), []kernel.Point{{Tool: []float64{0}, Ctx: []float64{0}}}, []bool{true}, nil, Options{MaxIter: 0, Jitter: math.NaN()}.withDefaults()); err == nil {
		t.Error("a NaN jitter fitted")
	}
}

func benchTrain(n int) ([]kernel.Point, []bool, kernel.Point) {
	pts, ys := randTrain(rand.New(rand.NewPCG(1, 1)), n)
	return pts, ys, kernel.Point{Tool: pts[0].Tool, Ctx: pts[0].Ctx, T: float64(n)}
}

func BenchmarkPredict512(b *testing.B) {
	pts, ys, q := benchTrain(512)
	p := must(Fit(kernel.DefaultAdditive(), pts, ys, Options{}))
	b.ReportAllocs()
	for b.Loop() {
		p.Predict(q)
	}
}

func BenchmarkFit512(b *testing.B) {
	pts, ys, _ := benchTrain(512)
	b.ReportAllocs()
	for b.Loop() {
		must(Fit(kernel.DefaultAdditive(), pts, ys, Options{}))
	}
}
