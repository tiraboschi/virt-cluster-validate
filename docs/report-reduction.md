# Composable report reduction

The validator publishes CTRF snapshots to a ConfigMap with a 900 kB budget.
The controller projects those snapshots into `VirtualizationValidation.status`
with a 700 kB budget. Each hop independently applies the same monotonic
reduction ladder and stops at the first level that fits its budget.

The CTRF extension `results.extra["validation.kubevirt.io/reduction"]` and the
matching `status.report.reduction` / `status.reduction` fields record the
cumulative result:

* `level`: highest applied rung, from 0 through 7.
* `totalExecutions`: exact number of executions before reduction.
* `pendingCollapsed` and `passedCollapsed`: exact omitted successful or pending
  execution counts.
* `requiredFailuresOmitted` and `advisoryFailuresOmitted`: exact omitted failed
  execution counts.
* `requiredInconclusiveOmitted` and `advisoryInconclusiveOmitted`: exact
  omitted timed out, skipped, cancelled, or errored execution counts.

| Level | Reduction |
| --- | --- |
| R0 | No reduction. |
| R1 | Drop diagnostic traces. |
| R2 | Collapse pending executions into a count. |
| R3 | Strip passed-execution diagnostics and timing. |
| R4 | Retain one marked passed exemplar and collapse the rest into a count. |
| R5 | Keep two failure exemplars for each check/reason pair. |
| R6 | Keep required failures before advisory failures, capped at 64 details. |
| R7 | Keep summary counters only. |

The status projection may advance to R4 before byte pressure when more than 32
passed executions are present. This intentionally keeps MTV-facing status
decision-oriented rather than a per-node success log. A required failure that
is omitted at either hop still makes the verdict `Invalid`; a required timed
out, skipped, cancelled, or errored execution remains `Inconclusive` even when
its detail is omitted. Advisory versions contribute a warning instead.
