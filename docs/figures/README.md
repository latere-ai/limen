# Figures

The figures in the README and in the documents come from simulations that
run the code of Limen. A program draws each mark from the data of a run.
A program calculates each number in a label. Nobody draws a point or
types a number by hand.

## The figures

Each figure has two files: `<name>.svg` for a light page and
`<name>-dark.svg` for a dark page.

| Figure | Page | What it shows | What ran |
|---|---|---|---|
| `teaser` | [README](../../README.md) | The approval probability of the model against the true approval probability of the person, in three parts of the history: decisions 1 to 150, 151 to 750, and 751 to 1,500. The trust of the person resets at decision 751. | The model of Limen |
| `thresholds` | [Theory](../theory.md#the-decision-rule) | The two thresholds and the width of the ask range for each cost of a false allow and of a false block, and three cost profiles. | `limen.Costs.Thresholds` |
| `cover` | [Theory](../theory.md#the-cover) | The approval probability against the weight of the approvals that cover the action, and which actions got `allow`, a `few` ask, or a `wider` ask. | Limen: the engine with its cover rule |
| `forgetting` | [Theory](../theory.md#the-cost-of-forgetting) | Decisions 751 to 1,500 with the product kernel and with the additive kernel (Proposition 4). | The model of Limen |
| `audits` | [The ledger](../ledger.md#reading-a-report) | The false allow rate that the ledger calculates from audits, against the true rate, at audit rates of 2%, 5%, 10%, and 20%. | The model of Limen, and `estimate.Rate` |
| `calibration` | [The ledger](../ledger.md#the-calibration-report) | The calibration report of each person, bin by bin. | The model of Limen, and `estimate.Calibrate` |

"The model of Limen" is the Gaussian-process model of `engine/gp` with
`kernel.DefaultAdditive`, or `kernel.DefaultProduct` where the figure
says so. "Limen" is the engine of `engine`, with its feature extractor,
its model version `learned/1`, and its cover rule.

## What ran

`cmd/simulate` runs the simulations. `render.py` draws the figures from
the data in `data/`.

- **The person.** For all figures except `cover`, the person is the
  simulated supervisor of the paper. Package `person` ports
  `experiment/data.py` and `experiment/oracle.py` of
  [github.com/changkun/trustcalib](https://github.com/changkun/trustcalib)
  with the same constants. The person has a tolerance that grows with
  trust, a trust reset at decision 751, a veto on irreversible actions on
  sensitive targets while trust is low, and an approval probability
  $\Phi(f^*)$. A test compares the port with the values of the reference.
- **The protocol.** Package `protocol` does what a decision point does,
  as `experiment/gateway.py` does it. For each action, the model predicts
  first. The thresholds of the cost profile give the suggested verdict.
  `verdict.Decide` makes the review selection one time. The person
  answers each action that they see. The model is fitted again after
  each answer, on the 512 most recent answers.
- **The persons.** There are 20 persons, seeds 0 to 19. Each person has
  1,500 decisions. The actions of person `s` come from seed `1000 + s`,
  as in the paper.
- **The cost profiles.** The default profile, Careful, is
  `limen.DefaultCosts`. The `audits` figure uses Balanced, because it
  allows enough actions that the person would deny. Its runs repeat each
  person with 5 seeds of the review selection and the answers.
- **The cover rule.** Package `consent` runs the engine itself. A
  simulated person sees shell commands and `publish` actions of
  different scopes, in sessions of 40 decisions. Each decision is one
  `engine.Predict` call for a new action. Half the answers open the
  details. An approval that comes in less than 2 seconds with the details
  closed has weight 0. The paper has no person with a scope, so this
  person is not the person of the paper.

## Make the figures again

You need Go 1.27 or later and [uv](https://docs.astral.sh/uv/). From the
root of the repository, run:

```sh
make figures
```

The command runs the simulations and then draws the figures. On a
computer with 18 cores, the simulations take approximately 7 minutes and
the drawings approximately 40 seconds. To draw the figures again from
the data in `data/`, run `uv run render.py` in this directory. To draw
one figure and a PNG preview of it, run
`uv run render.py teaser --preview /tmp/previews`.

The seeds are fixed. The same data give the same SVG files, byte for
byte. On a computer of a different architecture, the linear algebra can
give different values in the last digits, and the data can change a
little.

## The limits

- These are simulations with a known person. They show that the method
  does what the theory says when the person behaves as the model
  assumes. They do not show how real persons behave.
- The port draws its random numbers with Go, not with NumPy. A person of
  a given seed is a different sample of the same person than in the
  paper.
- The model of Limen is not the model of the paper in three details. It
  keeps the 512 most recent answers. It is fitted after each answer, not
  after each eighth step. It predicts from the first answer, not from the
  second.

## The data

| File | Contents |
|---|---|
| `decisions.csv.gz` | Each decision of each person, with the additive kernel and the default profile. |
| `decisions-product.csv.gz` | The same persons with the product kernel. |
| `audits.csv` | For each run of the `audits` figure: the true false allow rate, the estimate, and its interval. |
| `calibration.csv` | The calibration report of each person in `decisions.csv.gz`, bin by bin. |
| `thresholds.csv.gz` | `limen.Costs.Thresholds` at 161 × 161 costs. |
| `profiles.csv` | The three cost profiles and their thresholds. |
| `cover.csv.gz` | Each prediction of the engine in the `cover` runs. |
| `meta.json` | The settings of the runs. The figures read their thresholds and captions from it. |
