// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package person

import (
	"math"
	"slices"
	"testing"
)

func tool(name string) Tool {
	for _, t := range Tools {
		if t.Name == name {
			return t
		}
	}
	panic(name)
}

// The oracle reproduces the research reference. The want values are
// experiment/oracle.py latent_f and approve_prob with
// OracleConfig(changepoint=750), and the blocks are DecisionPoint.featurize,
// printed by the reference's own interpreter for these decisions.
func TestOracleMatchesReference(t *testing.T) {
	cases := []struct {
		tool, target, task string
		arg                bool
		t                  int
		latent, approve    float64
	}{
		{"git_force_push", "prod_infra", "ops_maintenance", true, 10, -4.920337725827706, 4.319750244706924e-07},
		{"git_force_push", "prod_infra", "ops_maintenance", true, 700, -0.44613856649843775, 0.32774857794778556},
		{"git_force_push", "prod_infra", "ops_maintenance", true, 760, -4.920337725827706, 4.319750244706924e-07},
		{"read_file", "workspace_src", "bugfix", false, 0, 0.974, 0.8349717303880123},
		{"apply_patch", "build_config", "feature_dev", false, 300, 1.4672150712082264, 0.928841220828956},
		{"execute_sql", "production_db", "data_migration", true, 749, -0.3422340279386584, 0.3660873895157969},
		{"execute_sql", "production_db", "data_migration", true, 750, -4.937200000000001, 3.9626092549708954e-07},
		{"deploy", "ci_config", "security_hardening", false, 1499, 0.7105659720613416, 0.7613233821053644},
	}
	for _, c := range cases {
		d := Decision{T: c.t, Tool: tool(c.tool), Target: c.target, Task: c.task, ArgRisk: c.arg}
		if got := Paper.Latent(d); math.Abs(got-c.latent) > 1e-12 {
			t.Errorf("%s at %d: latent %v, want %v", d.Name(), c.t, got, c.latent)
		}
		if got := Paper.Approve(d); math.Abs(got-c.approve) > 1e-12*math.Max(1, c.approve)+1e-15 {
			t.Errorf("%s at %d: approve %v, want %v", d.Name(), c.t, got, c.approve)
		}
	}
	d := Decision{T: 300, Tool: tool("apply_patch"), Target: "build_config", Task: "feature_dev"}
	p := d.Point()
	if want := []float64{0.5, 0.4, 1.0 / 3, 0, 0, 0, 0, 0, 0, 0, 1}; !slices.Equal(p.Tool, want) {
		t.Errorf("tool block %v, want %v", p.Tool, want)
	}
	if want := []float64{0.55, 0, 1, 0, 0, 0, 0, 0, 0}; !slices.Equal(p.Ctx, want) || p.T != 300 {
		t.Errorf("context block %v at %v, want %v at 300", p.Ctx, p.T, want)
	}
}

// A stream draws only plausible targets, and its mix follows the task
// propensities: a seed is reproducible and two seeds differ.
func TestStream(t *testing.T) {
	a, b := Stream(2000, 1000), Stream(2000, 1000)
	if !slices.Equal(a, b) {
		t.Fatal("one seed gives two streams")
	}
	if slices.Equal(a, Stream(2000, 1001)) {
		t.Fatal("two seeds give one stream")
	}
	db := 0
	for i, d := range a {
		if d.T != i {
			t.Fatalf("decision %d at time %d", i, d.T)
		}
		if !slices.Contains(categoryTargets[d.Tool.Category], d.Target) {
			t.Fatalf("%s acts on %s", d.Tool.Name, d.Target)
		}
		if d.Tool.Category == "db" {
			db++
		}
		if d.ArgRisk && d.Tool.ArgRiskProb == 0 {
			t.Fatalf("%s drew a destructive argument", d.Tool.Name)
		}
	}
	// Only data_migration draws db, with weight 4 of 8: 1/7 * 1/2 of all.
	if share := float64(db) / float64(len(a)); math.Abs(share-1.0/14) > 0.02 {
		t.Errorf("db share %v, want about %v", share, 1.0/14)
	}
}
