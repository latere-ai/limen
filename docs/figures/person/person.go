// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package person is the simulated person of the paper (Ou, "Learning When
// to Ask", arXiv:2605.19151, Definition 1): the stream of agent actions
// they are asked about, and the latent tolerance f*(x, t) behind their
// answers. It is a port of the research reference,
// github.com/changkun/trustcalib, experiment/data.py (the action and
// context taxonomy and make_stream) and experiment/oracle.py
// (OracleConfig, trust, latent_f, approve_prob), with every constant kept.
//
// The port keeps the generative process and its constants, not NumPy's
// random draws: a stream here is drawn with math/rand/v2, so a person of a
// given seed is a different realization of the same process than the
// paper's person of that seed.
package person

import (
	"math"
	"math/rand/v2"
	"sort"

	"latere.ai/x/limen/engine/kernel"
)

// Reversibility classes, experiment/data.py REVERSIBLE, HARD, IRREVERSIBLE.
const (
	Reversible   = 0
	Hard         = 1
	Irreversible = 2
)

// Tool is an agent tool with its decision-time risk attributes,
// experiment/data.py Tool: the category, the reversibility class, the base
// sensitivity, the blast radius (0 one file to 3 external), and the
// probability that a destructive argument comes with it.
type Tool struct {
	Name            string
	Category        string
	Reversibility   int
	BaseSensitivity float64
	Blast           int
	ArgRiskProb     float64
}

// Tools is experiment/data.py TOOLS, in its order.
var Tools = []Tool{
	// read-only, low risk
	{"read_file", "read", Reversible, 0.10, 0, 0.00},
	{"list_dir", "read", Reversible, 0.08, 1, 0.00},
	{"grep_search", "search", Reversible, 0.10, 2, 0.00},
	{"git_status", "vcs", Reversible, 0.05, 2, 0.00},
	{"run_tests", "exec", Reversible, 0.20, 2, 0.02},
	{"web_fetch", "network", Reversible, 0.30, 3, 0.05},
	// moderate, hard to reverse
	{"write_file", "write", Hard, 0.30, 0, 0.05},
	{"apply_patch", "write", Hard, 0.40, 1, 0.08},
	{"format_code", "write", Hard, 0.25, 2, 0.03},
	{"install_package", "exec", Hard, 0.50, 2, 0.15},
	{"git_commit", "vcs", Hard, 0.30, 2, 0.04},
	// high, irreversible
	{"delete_file", "write", Irreversible, 0.60, 0, 0.30},
	{"git_push", "vcs", Irreversible, 0.60, 3, 0.10},
	{"git_force_push", "vcs", Irreversible, 0.85, 3, 0.60},
	{"execute_sql", "db", Irreversible, 0.70, 2, 0.35},
	{"shell_exec", "exec", Irreversible, 0.70, 2, 0.30},
	{"send_email", "network", Irreversible, 0.50, 3, 0.10},
	{"deploy", "deploy", Irreversible, 0.90, 3, 0.20},
}

// Targets is experiment/data.py TARGETS: each target's sensitivity in
// [0, 1].
var Targets = map[string]float64{
	"sandbox_tmp":     0.00,
	"workspace_tests": 0.20,
	"workspace_src":   0.40,
	"build_config":    0.55,
	"ci_config":       0.65,
	"secrets_env":     0.90,
	"production_db":   0.95,
	"prod_infra":      1.00,
}

// Tasks is experiment/data.py TASKS; the order fixes the one-hot index.
var Tasks = []string{
	"feature_dev",
	"bugfix",
	"refactor",
	"ops_maintenance",
	"data_migration",
	"security_hardening",
	"exploration",
}

// Categories is sorted({t.category for t in TOOLS}) of experiment/data.py;
// the order fixes the one-hot index.
var Categories = func() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range Tools {
		if !seen[t.Category] {
			seen[t.Category] = true
			out = append(out, t.Category)
		}
	}
	sort.Strings(out)
	return out
}()

// categoryTargets is experiment/data.py _CATEGORY_TARGETS: the targets a
// tool of each category plausibly acts on.
var categoryTargets = map[string][]string{
	"read":    {"sandbox_tmp", "workspace_tests", "workspace_src", "build_config"},
	"search":  {"workspace_tests", "workspace_src", "build_config", "ci_config"},
	"vcs":     {"workspace_src", "build_config", "ci_config", "prod_infra"},
	"exec":    {"sandbox_tmp", "workspace_tests", "workspace_src", "ci_config"},
	"write":   {"sandbox_tmp", "workspace_tests", "workspace_src", "build_config", "ci_config"},
	"db":      {"workspace_tests", "production_db"},
	"network": {"sandbox_tmp", "workspace_src", "prod_infra"},
	"deploy":  {"ci_config", "prod_infra"},
}

// weight is one entry of a task's category propensity.
type weight struct {
	category string
	w        float64
}

// taskCategoryWeights is experiment/data.py _TASK_CATEGORY_WEIGHTS, each
// list in the dict's insertion order, which make_stream samples in.
var taskCategoryWeights = map[string][]weight{
	"feature_dev":        {{"write", 3}, {"read", 3}, {"search", 2}, {"exec", 2}, {"vcs", 2}},
	"bugfix":             {{"read", 3}, {"search", 3}, {"write", 2}, {"exec", 2}, {"vcs", 1}},
	"refactor":           {{"write", 3}, {"read", 2}, {"search", 2}, {"vcs", 2}},
	"ops_maintenance":    {{"exec", 3}, {"deploy", 2}, {"vcs", 2}, {"network", 2}, {"read", 1}},
	"data_migration":     {{"db", 4}, {"exec", 2}, {"write", 1}, {"read", 1}},
	"security_hardening": {{"search", 3}, {"read", 3}, {"write", 1}, {"vcs", 1}},
	"exploration":        {{"read", 4}, {"search", 3}, {"vcs", 1}},
}

// Decision is one action x_t = (a_t, c_t) arriving at time T, the count of
// the person's decisions before it, experiment/data.py DecisionPoint.
type Decision struct {
	T       int
	Tool    Tool
	Target  string
	Task    string
	ArgRisk bool // a destructive argument pattern is present
}

// TargetSens is the sensitivity of the decision's target.
func (d Decision) TargetSens() float64 { return Targets[d.Target] }

// Name is the action as a reader sees it, tool -> target.
func (d Decision) Name() string { return d.Tool.Name + " -> " + d.Target }

// Point is the decision as Limen's model sees it, the feature blocks of
// experiment/data.py DecisionPoint.featurize, which the research's Go
// featurizer (tag v3, go/featurizer/trustcalib/featurizer.go) and Limen's
// golden fixtures (engine/testdata/fixtures, read by engine/gp/gp_test.go
// as kernel.Point{Tool: phi_tool, Ctx: phi_ctx, T: t}) use:
//
//	Tool = [reversibility/2, base_sensitivity, blast/3] ++ category one-hot (8)   11 entries
//	Ctx  = [target_sensitivity, arg_risk]               ++ task one-hot (7)        9 entries
//	T    = the decision's time, in decisions
//
// The blocks omit the tool's name, so similar tools share evidence, and
// never carry the oracle's veto or its trust.
func (d Decision) Point() kernel.Point {
	tool := make([]float64, 3+len(Categories))
	tool[0] = float64(d.Tool.Reversibility) / 2
	tool[1] = d.Tool.BaseSensitivity
	tool[2] = float64(d.Tool.Blast) / 3
	tool[3+index(Categories, d.Tool.Category)] = 1
	ctx := make([]float64, 2+len(Tasks))
	ctx[0] = d.TargetSens()
	if d.ArgRisk {
		ctx[1] = 1
	}
	ctx[2+index(Tasks, d.Task)] = 1
	return kernel.Point{Tool: tool, Ctx: ctx, T: float64(d.T)}
}

func index(xs []string, x string) int {
	for i, y := range xs {
		if y == x {
			return i
		}
	}
	panic("person: unknown name " + x)
}

// Stream returns n decisions at times 0 to n-1, experiment/data.py
// make_stream: a task uniformly, a tool category by the task's
// propensity, a tool of that category uniformly, a plausible target of
// the category uniformly, and a destructive argument with the tool's
// probability. The draws come from PCG seeded with (seed, 0).
func Stream(n int, seed uint64) []Decision {
	rng := rand.New(rand.NewPCG(seed, 0))
	byCat := map[string][]Tool{}
	for _, t := range Tools {
		byCat[t.Category] = append(byCat[t.Category], t)
	}
	out := make([]Decision, n)
	for t := range n {
		task := Tasks[rng.IntN(len(Tasks))]
		cat := choose(rng, taskCategoryWeights[task])
		tool := byCat[cat][rng.IntN(len(byCat[cat]))]
		targets := categoryTargets[cat]
		target := targets[rng.IntN(len(targets))]
		out[t] = Decision{T: t, Tool: tool, Target: target, Task: task, ArgRisk: rng.Float64() < tool.ArgRiskProb}
	}
	return out
}

// choose draws a category with probability proportional to its weight,
// as rng.choice(len(cats), p=probs) does.
func choose(rng *rand.Rand, ws []weight) string {
	total := 0.0
	for _, w := range ws {
		total += w.w
	}
	u := rng.Float64() * total
	for _, w := range ws {
		if u < w.w {
			return w.category
		}
		u -= w.w
	}
	return ws[len(ws)-1].category
}

// Oracle holds the ground-truth parameters, experiment/oracle.py
// OracleConfig with changepoint 750, the setting of the paper's main runs
// (experiment/run.py ORACLES["changepoint"]).
type Oracle struct {
	A0          float64 // baseline lean toward approval
	WRisk       float64 // how strongly action risk lowers tolerance
	Rho         float64 // weight on accumulated trust
	Theta       float64 // trust ceiling
	Kappa       float64 // trust accumulation timescale, in decisions
	Veto        float64 // strength of the safety conjunction
	VetoSens    float64 // target sensitivity from which the veto applies
	Changepoint int     // time at which trust resets; negative is never

	WRev, WTSens, WBlast, WArg, WBase float64 // risk aggregation weights
}

// Paper is the oracle of the paper's main runs.
var Paper = Oracle{
	A0: 1.4, WRisk: 3.4, Rho: 1.0, Theta: 1.6, Kappa: 130.0, Veto: 3.0, VetoSens: 0.65, Changepoint: 750,
	WRev: 0.28, WTSens: 0.32, WBlast: 0.12, WArg: 0.16, WBase: 0.12,
}

// taskOffset is experiment/oracle.py _TASK_OFFSET: the task context shifts
// the person's tolerance.
var taskOffset = map[string]float64{
	"feature_dev":        0.00,
	"bugfix":             0.05,
	"refactor":           -0.05,
	"ops_maintenance":    -0.10,
	"data_migration":     -0.25,
	"security_hardening": 0.15,
	"exploration":        0.10,
}

// Risk is the composite risk of the action in [0, about 1],
// experiment/oracle.py raw_risk.
func (o Oracle) Risk(d Decision) float64 {
	arg := 0.0
	if d.ArgRisk {
		arg = 1
	}
	return o.WRev*(float64(d.Tool.Reversibility)/2) + o.WTSens*d.TargetSens() +
		o.WBlast*(float64(d.Tool.Blast)/3) + o.WArg*arg + o.WBase*d.Tool.BaseSensitivity
}

// Trust is the person's accumulated trust at time t, experiment/oracle.py
// trust: theta (1 - exp(-(t - t0) / kappa)), with t0 the changepoint once
// t reaches it and 0 before, so trust resets to zero at the changepoint
// and builds again.
func (o Oracle) Trust(t int) float64 {
	t0 := 0
	if o.Changepoint >= 0 && t >= o.Changepoint {
		t0 = o.Changepoint
	}
	return o.Theta * (1 - math.Exp(-float64(t-t0)/o.Kappa))
}

// Vetoed reports the three-way conjunction of experiment/oracle.py
// _veto_active: an irreversible tool, a target at least VetoSens
// sensitive, and trust below half its ceiling.
func (o Oracle) Vetoed(d Decision) bool {
	return d.Tool.Reversibility == Irreversible && d.TargetSens() >= o.VetoSens && o.Trust(d.T) < o.Theta/2
}

// Latent is f*(x, t), experiment/oracle.py latent_f: the static
// acceptability a0 - w_risk r(x), plus rho trust(t), plus the task's
// offset, less Veto when the conjunction holds.
func (o Oracle) Latent(d Decision) float64 {
	f := o.A0 - o.WRisk*o.Risk(d) + o.Rho*o.Trust(d.T) + taskOffset[d.Task]
	if o.Vetoed(d) {
		f -= o.Veto
	}
	return f
}

// Approve is the person's true approval probability Phi(f*(x, t)),
// experiment/oracle.py approve_prob.
func (o Oracle) Approve(d Decision) float64 {
	return 0.5 * math.Erfc(-o.Latent(d)/math.Sqrt2)
}
