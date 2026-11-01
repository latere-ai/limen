# Terms

This page gives the meaning of each term that the Limen documents use. Each
term has one meaning only. The documents do not use a term with a different
meaning. The third column gives the name of the related field in the API,
if there is one.

## The parts

| Term | Meaning | In the API |
|---|---|---|
| action | One operation that software wants to do for a person. Examples: run a shell command, merge a branch, send a network request, deploy a service, make a payment. | `Action` |
| agent | Software that proposes actions. An AI coding agent is an example. | `actor` |
| agent harness | The program that runs an agent and its tools. An agent harness is the most frequent type of decision point. | |
| decision point | The software that decides if an action runs. The decision point calls Limen, applies a verdict, and records the decision. Limen does not decide. | |
| Limen | The service that predicts which actions a person approves, keeps the ledger, and calculates error rates. | |
| `limend` | The program that supplies the Limen API over HTTP. | |
| `limen` | The command that replays recorded decisions through Limen. | |
| person | The person for whom an action runs. Limen learns the answers of this person. | `subject` |
| organization | The organization that the records belong to. The records of one organization are separate from the records of all other organizations. | `org` |
| context of a person | The records of a person that have no organization. Their `org` is the subject of the person. | `org` |

## Verdicts

A verdict is the result of a decision about one action. There are four
verdicts. The list starts with the verdict that permits the most.

| Verdict | Meaning |
|---|---|
| `allow` | The action runs. No person examines it. |
| `flag` | The action runs. A person examines it after it runs. |
| `ask` | The action waits. A person approves or denies it before it can run. |
| `block` | The action does not run. |

| Term | Meaning | In the API |
|---|---|---|
| automatic verdict | An `allow` or a `block` that the decision point applies without a person. | |
| rules | The fixed rules of a decision point, for example a list of permitted commands. Rules give the same verdict each time for the same action. | |
| ceiling | The verdict that permits the most and that the rules of the decision point accept for an action. The applied verdict never permits more than the ceiling. | `ceiling` in `verdict.Decide` |
| judge | A language model that examines an action and gives a verdict or a score. | |
| signal | A verdict or a score from a different source, for example a judge, that the decision point attaches to an action. | `signals` |
| source | The origin of a verdict. Examples: the suggestions of Limen, the rules of a decision point, a judge. | `source`, `of` |

## Predictions

| Term | Meaning | In the API |
|---|---|---|
| prediction | The estimate of Limen for one action. It has the approval probability, the uncertainty, and the suggested verdict. | `Prediction` |
| approval probability | The probability that the person approves the action. It is a number from 0 to 1. | `approve` in a prediction |
| uncertainty | A measure of how little the model knows about an action. Two actions can have the same approval probability and a different uncertainty: one action is familiar and one action is new. | `uncertainty` |
| suggested verdict | The verdict that Limen suggests for an action. It is `allow`, `ask`, or `block`. Limen never suggests `flag`. | `verdict` in a prediction |
| model | The statistical model that Limen keeps for each person in each organization. Limen calculates it again from the answers of the person. | |
| tolerance | The amount of risk that a person accepts. The model gives each person a tolerance that changes with time. | |
| risk | A property of an action. The model lets tolerance change with time. It does not let risk change with time. | |
| features | Numbers that Limen calculates from an action. The model uses the features. Limen keeps the features. | |
| feature extractor | A fixed program that calculates features from the input of an action. A feature extractor is not a language model. | |
| input | The arguments of an action, for example the text of a shell command. Limen reads the input to calculate the features. Limen does not keep the input. | `input` |
| label | A short name for the type of an action, from a fixed list of words in the feature extractor. Examples: `bash: go test`, `bash: git push --force`. A label never contains the input. | `label` |
| explanation | The parts of one prediction: the answers that changed it most, and what the model learned about the type of action and about the recent tolerance of the person. | `Explanation` |
| share | The part of one answer in an explanation, from 0 to 1. | `share` |
| evidence ratio | The part of the uncertainty before any answer that the answers of the person removed, from 0 to 1. | `evidence_ratio` |
| unsure | An `ask` with an evidence ratio less than 0.5. The person has few answers about this type of action. More answers change the suggestion. | `ask_kind` |
| split | An `ask` with an evidence ratio of 0.5 or more. The answers of the person about this type of action disagree. More answers do not change the suggestion. | `ask_kind` |
| preview | What a different cost profile would do with the recorded predictions of a person. | `Preview` |

## Costs and thresholds

| Term | Meaning | In the API |
|---|---|---|
| false allow | An action that a source allowed and that the person would deny. | `false_allow` |
| false block | An action that a source blocked and that the person would approve. | `false_block` |
| cost profile | The costs that an organization states: the cost of a false allow, the cost of a false block, and the cost of one `ask`. It also has the audit rate. A cost profile applies to an organization, a kind of action, or an agent. | `Profile`, `costs` |
| thresholds | Two approval probabilities that Limen calculates from the cost profile. Above the upper threshold, Limen suggests `allow`. Below the lower threshold, Limen suggests `block`. | `allow_above`, `block_below` |
| ask range | The approval probabilities between the two thresholds. In this range, Limen suggests `ask`. | |

## The ledger

| Term | Meaning | In the API |
|---|---|---|
| ledger | The record of all actions, predictions, decisions, and answers. Limen keeps the ledger. | |
| case | The record of one action in the ledger: the action, the prediction, the decision, and the answers. | `Case` |
| decision | The record of what the decision point did with one action: the applied verdict, the review probability, and the source. | `Decision` |
| answer | The approval or the denial that a person gives for one action. | `Answer`, `approve` in an answer |
| review queue | The actions that a person saw and that have no answer yet. | `pending=true` in `GET /v1/cases` |
| audit rate | The probability that the decision point shows an automatic verdict to a person. The default is 5%. | `audit_rate` |
| review probability | The probability that a person sees the action. The decision point sets it before the action runs. | `review_probability` |
| review | The first answer to an action that a person saw because of the decision (a `flag` or an `ask`). Only reviews go into error rates. | `reviews` |
| audit | A review of an automatic verdict that the random selection showed to a person. Its review probability is less than 1. | `audits` |
| error rate | The false allow rate of a source is the part of its allows that the person would deny. The false block rate is the part of its blocks that the person would approve. | `value` |
| reliability report | The error rates of one source, for one person, in one period of time. | `Reliability` |
| certificate | A test that the false allow rate of a source is below a limit, with a stated confidence. | `certificate` |
| replay | A run of recorded decisions through Limen in time order. A replay shows what Limen would suggest. | `Replay` |
| ledger directory | The directory where `limend` keeps the ledger on disk. Only one process can use a ledger directory at a time. | `LIMEN_DATA_DIR` |
| regret | The mean cost of the decisions, minus the mean cost of the best possible decisions. The unit is the cost of one `ask`. The paper uses it to compare methods. | |
